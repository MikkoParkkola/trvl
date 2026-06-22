// Package providers accesses third-party provider APIs on behalf of the
// local user for personal, noncommercial travel search. It is licensed
// under PolyForm Noncommercial 1.0.0 (see LICENSE at repo root).
// Commercial use, redistribution of scraped data, or operation as a
// service is prohibited by this license.
//
// Rate limits are intentionally conservative (0.5 req/s default per
// provider) to make request patterns behaviorally indistinguishable
// from manual human browsing. Cookie persistence is capped at 24h.
// Per-provider browser escape hatches require explicit opt-in via
// AuthConfig.BrowserEscapeHatch AND WithInteractive context.
package providers

import (
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MikkoParkkola/trvl/internal/preflightttl"
	"golang.org/x/time/rate"
)

const (
	// Why 0.5: one request every 2 seconds per provider. Keeps aggregate
	// traffic indistinguishable from relaxed human browsing even when
	// multiple parallel searches run (e.g. 5 providers × 0.5 rps = 2.5 rps
	// total, well below bot-detection thresholds of major travel sites).
	defaultRPS = 0.5

	// Why 1: no burst beyond the steady rate. A burst > 1 would allow
	// back-to-back requests at startup — detectable as non-human traffic.
	defaultBurst = 1

	// Why 10 minutes: WAF session tokens (Akamai bm_sz, AWS awsalb) and
	// preflight-extracted auth tokens (X-Auth-Token, csrfToken) typically
	// expire in 10-30 minutes. Caching for 10 min avoids redundant preflight
	// round-trips within a session while safely refreshing before tokens go
	// stale. Cookie persistence is capped at 24 h overall (see package doc).
	authCacheDuration = 10 * time.Minute

	// Why 0.15: 0.15° latitude ≈ 16.7 km; 0.15° longitude ≈ 11-13 km at
	// mid-latitudes. This gives a ~33 × 26 km bounding box (NE/SW corners)
	// centered on the searched location — wide enough to cover an entire city
	// center for providers that take a bbox parameter (Hostelworld, some
	// Booking endpoints) without spilling into adjacent cities.
	boundingBoxOffset = 0.15

	// Why 10 MB: largest observed real response is ~3 MB (Booking SSR with
	// full Apollo cache). 10 MB gives 3× headroom for future growth while
	// preventing a runaway provider from consuming unbounded memory.
	maxResponseBytes = 10 * 1024 * 1024

	// Circuit breaker: skip providers with N+ consecutive errors and no
	// success within the cooldown window. Prevents wasting 15-30s per
	// search on providers that are consistently blocked or down.
	//
	// Why 5: fewer than 5 lets transient network blips (1-2 failures) silence
	// a provider. More than 5 wastes search cycles on a provider that is
	// genuinely down. Five consecutive failures without any success is a
	// reliable signal of a systematic problem (WAF block, API change, outage).
	circuitBreakerThreshold = 5
	circuitBreakerCooldown  = 5 * time.Minute
)

// perProviderTimeout caps any single provider's full execution:
// preflight → cookie read → WAF solve → search → parse. Without it, a
// provider wedged in a browser cookie lookup or a WAF retry cascade holds up
// the whole batch — discovery runs providers in parallel but still waits for
// the slowest, so one blocked provider (Booking WAF, Airbnb dial failures)
// sets the floor, and a freshly-spawned process pays it in full because the
// circuit breaker is process-local and starts closed.
//
// Default 18s: API-first providers (the default, no-key paths) answer in ≤8s,
// so 18s is generous for them while halving the penalty a dead/blocked
// provider imposes on a cold process. The optional browser-assisted cookie
// path (kooky cold start ≤15s) still fits. Override via TRVL_PROVIDER_TIMEOUT
// (e.g. "30s") when relying on slow browser WAF solving.
var perProviderTimeout = providerTimeoutFromEnv()

func providerTimeoutFromEnv() time.Duration {
	const def = 18 * time.Second
	raw := strings.TrimSpace(os.Getenv("TRVL_PROVIDER_TIMEOUT"))
	if raw == "" {
		return def
	}
	if d, err := time.ParseDuration(raw); err == nil && d > 0 {
		return d
	}
	return def
}

// HotelFilterParams carries search filter values that should be passed through
// to external provider URL templates and query parameters via ${var} substitution.
type HotelFilterParams struct {
	MinPrice         float64
	MaxPrice         float64
	PropertyType     string   // normalized: "hotel", "apartment", "hostel", etc.
	Sort             string   // "price", "rating", "distance", "stars"
	Stars            int      // minimum star rating, 0 = no filter
	MinRating        float64  // minimum guest rating, 0 = no filter
	Amenities        []string // required amenities
	FreeCancellation bool
	Refundable       bool
	ChildrenAges     []int
	Rooms            int

	// Extended filters — wired to providers that support them.
	MinBedrooms    int    // minimum bedrooms (Airbnb)
	MinBathrooms   int    // minimum bathrooms (Airbnb)
	MinBeds        int    // minimum beds (Airbnb)
	RoomType       string // "entire_home", "private_room", "shared_room" (Airbnb)
	Superhost      bool   // Superhost-only filter (Airbnb)
	InstantBook    bool   // instant-bookable only (Airbnb)
	MaxDistanceM   int    // max distance from center in meters (Booking)
	Sustainable    bool   // eco/sustainable properties (Booking)
	MealPlan       bool   // breakfast/meal included (Booking)
	IncludeSoldOut bool   // include sold-out properties in results (Booking)

	MustHaveKitchen   bool
	MustHaveWifi      bool
	MustHaveWorkspace bool
}

// Runtime is the generic HTTP execution engine for configured providers.
type Runtime struct {
	registry *Registry
	clients  map[string]*providerClient
	mu       sync.RWMutex
	inflight atomic.Int64
}

// providerClient holds per-provider HTTP state.
type providerClient struct {
	config     *ProviderConfig
	client     *http.Client
	limiter    *rate.Limiter
	authMu     sync.RWMutex
	authValues map[string]string
	authExpiry time.Time
	// lastPreflightURL tracks the fully-resolved preflight URL used for the
	// current auth cache entry. When the preflight URL contains ${city_id} or
	// other search-specific vars, switching cities produces a different URL
	// and the auth cache must be invalidated — WAF cookies obtained for one
	// dest_id are rejected for a different one.
	lastPreflightURL string

	// ttlState is the AIMD adaptive TTL controller for the auth cache.
	// Accessed under authMu (same lock that protects authExpiry).
	ttlState preflightttl.State

	// defaultRPS is the configured rate for this provider; recordRateLimitSuccess
	// uses it to restore the limiter after the cooldown period elapses.
	defaultRPS float64
	// consecutive429 counts uninterrupted 429 responses; reset on success.
	consecutive429 int
	// last429 records when the most recent 429 was received.
	last429 time.Time
	rl429Mu sync.Mutex
}

// effectiveCacheTTL returns the adaptive TTL when the AIMD controller has
// accumulated a positive value; otherwise falls back to authCacheDuration.
// Must be called with pc.authMu held (read or write).
func (pc *providerClient) effectiveCacheTTL() time.Duration {
	if pc.ttlState.CurrentTTL > 0 {
		return pc.ttlState.CurrentTTL
	}
	return authCacheDuration
}

// NewRuntime creates a Runtime backed by the given registry.
// It eagerly pre-warms browser cookies for all providers that use
// cookies.source = "browser", so the first search doesn't block on
// the macOS Keychain cold-start (6-10s on first access).
func NewRuntime(registry *Registry) *Runtime {
	rt := &Runtime{
		registry: registry,
		clients:  make(map[string]*providerClient),
	}

	// Eager cookie warm-up: start background kooky reads for all
	// browser-cookie providers immediately. By the time the user's
	// first search arrives (typically 1-5s later), the warm cache
	// will have the cookies ready.
	if registry == nil {
		return rt
	}
	for _, cfg := range registry.List() {
		if cfg.Cookies.Source == "browser" {
			warmURL := cfg.Endpoint
			if cfg.Auth != nil && cfg.Auth.PreflightURL != "" {
				warmURL = cfg.Auth.PreflightURL
			}
			WarmBrowserCookies(warmURL, cfg.Cookies.Browser)
		}
	}

	return rt
}

// getOrCreateClient returns the providerClient for the given config,
// creating it on first access.
func (rt *Runtime) getOrCreateClient(cfg *ProviderConfig) *providerClient {
	rt.mu.RLock()
	pc, ok := rt.clients[cfg.ID]
	rt.mu.RUnlock()
	if ok {
		return pc
	}

	rt.mu.Lock()
	defer rt.mu.Unlock()

	// Double-check after acquiring write lock.
	if pc, ok := rt.clients[cfg.ID]; ok {
		return pc
	}

	var httpClient *http.Client
	if cfg.TLS.Fingerprint == "chrome" && cfg.Cookies.Source != "browser" {
		// Use fhttp-based client that sends Chrome-like HTTP/2 SETTINGS,
		// WINDOW_UPDATE, and PRIORITY frames. Combined with utls Chrome146
		// TLS fingerprint, this makes requests indistinguishable from Chrome
		// at both the TLS and HTTP/2 layers — bypassing Akamai bot detection
		// that flags Go's x/net/http2 framing as "b_bot".
		//
		// When cookies.source is "browser", the real browser session cookies
		// already authenticate the request and the standard Go TLS transport
		// produces better results — some providers (Booking.com) SSR fewer
		// results through the fhttp/utls pipeline despite identical cookies,
		// likely due to subtle HTTP/2 framing differences that trigger a
		// different server-side rendering path.
		httpClient = newChromeH2Client()
	} else {
		httpClient = &http.Client{
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
				TLSHandshakeTimeout: 10 * time.Second,
				ForceAttemptHTTP2:   true,
			},
			Timeout: 30 * time.Second,
		}
	}
	if httpClient.Jar == nil {
		jar, _ := cookiejar.New(nil)
		httpClient.Jar = jar
	}

	rps := cfg.RateLimit.RequestsPerSecond
	if rps <= 0 {
		rps = defaultRPS
	}
	burst := cfg.RateLimit.Burst
	if burst <= 0 {
		burst = defaultBurst
	}

	pc = &providerClient{
		config:     cfg,
		client:     httpClient,
		limiter:    rate.NewLimiter(rate.Limit(rps), burst),
		authValues: make(map[string]string),
		defaultRPS: rps,
	}
	rt.clients[cfg.ID] = pc

	// Pre-warm browser cookies in the background so the first search
	// doesn't block on the macOS Keychain lookup (2-8s cold start).
	// The warm cache is checked by browserCookiesForURL/WithHint before
	// falling through to a synchronous kooky read.
	if cfg.Cookies.Source == "browser" {
		warmURL := cfg.Endpoint
		if cfg.Auth != nil && cfg.Auth.PreflightURL != "" {
			warmURL = cfg.Auth.PreflightURL
		}
		WarmBrowserCookies(warmURL, cfg.Cookies.Browser)
	}

	return pc
}

// InflightProviders returns the number of provider goroutines currently
// executing inside SearchHotels. Useful in tests to assert clean shutdown.
func (rt *Runtime) InflightProviders() int {
	return int(rt.inflight.Load())
}
