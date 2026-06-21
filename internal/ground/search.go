package ground

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/cache"
	"github.com/MikkoParkkola/trvl/internal/fareintel"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/searchctx"
	"golang.org/x/sync/singleflight"
	"golang.org/x/time/rate"
)

// groundGroup deduplicates concurrent in-flight searches with identical parameters.
var groundGroup singleflight.Group

// groundCache caches ground transport search results.
var groundCache = cache.New()

// groundCacheTTL is the TTL for cached ground transport results.
const groundCacheTTL = 10 * time.Minute

const sharedGroundSearchTimeout = 30 * time.Second

// --- Negative-result cache + speculative prefetch (innovation #4) ---------

// groundNegCacheTTL bounds how long a clean "no ground service" for a route+month
// suppresses re-running the full provider fan-out for that route.
const groundNegCacheTTL = 30 * time.Minute

// groundNegCache records routes for which the whole provider fan-out returned no
// routes CLEANLY (every provider that ran was not-applicable or definitively
// empty — no hard errors, timeouts, rate-limits). That is a genuine "nothing
// runs here", safe to cache briefly. A run with any error is never cached, so
// transient failures always retry. On by default; set TRVL_NEGCACHE=0 to disable.
var groundNegCache = cache.NewNegativeCache(groundNegCacheTTL)

func negCacheEnabled() bool {
	return groundEnvBool("TRVL_NEGCACHE", true)
}

// groundPrefetchEnabled gates speculative prefetch. Off by default: prefetch adds
// provider load (extra background searches), so it is opt-in via TRVL_PREFETCH=1.
func groundPrefetchEnabled() bool {
	return groundEnvBool("TRVL_PREFETCH", false)
}

func groundEnvBool(name string, def bool) bool {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return def
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return def
	}
	return v
}

// groundNegKey returns the negative-cache key for a search and whether the search
// is eligible for negative caching. Only unfiltered searches qualify: a MaxPrice
// or Type filter can make a served route look empty, which is NOT a "no service"
// fact and must never be negative-cached. The provider set is folded into the key
// so restricting providers cannot poison the all-providers result.
func groundNegKey(from, to, date string, opts SearchOptions, providerKey string) (string, bool) {
	if opts.MaxPrice > 0 || opts.Type != "" {
		return "", false
	}
	return cache.NegativeKey("ground|"+providerKey, from, to, date), true
}

// groundPrefetchCtxKey marks a context as a prefetch (or nested) search so
// speculative prefetch does not recurse.
type groundPrefetchCtxKey struct{}

func groundPrefetchDisabled(ctx context.Context) bool {
	return ctx.Value(groundPrefetchCtxKey{}) != nil
}

// groundPrefetchTarget is one likely-next search to warm.
type groundPrefetchTarget struct {
	from string
	to   string
	date string
}

// groundPrefetchTargets computes the likely-next searches to warm: the return
// leg (reverse route, +7 days) plus the two adjacent days. Pure and
// deterministic; an unparseable date yields no targets.
func groundPrefetchTargets(from, to, date string) []groundPrefetchTarget {
	t, err := models.ParseDate(date)
	if err != nil {
		return nil
	}
	plus1 := t.AddDate(0, 0, 1).Format("2006-01-02")
	minus1 := t.AddDate(0, 0, -1).Format("2006-01-02")
	ret := t.AddDate(0, 0, 7).Format("2006-01-02")
	return []groundPrefetchTarget{
		{from: to, to: from, date: ret}, // return leg
		{from: from, to: to, date: plus1},
		{from: from, to: to, date: minus1},
	}
}

// groundPrefetchConcurrency bounds simultaneous prefetch searches.
const groundPrefetchConcurrency = 2

// groundPrefetchFn warms a single target. Seam for tests; the default calls the
// normal search with prefetch disabled to prevent recursion.
var groundPrefetchFn func(ctx context.Context, t groundPrefetchTarget, opts SearchOptions)

func init() {
	groundPrefetchFn = func(ctx context.Context, t groundPrefetchTarget, opts SearchOptions) {
		_, _ = SearchByName(ctx, t.from, t.to, t.date, opts)
	}
}

// maybePrefetchGround dispatches best-effort speculative prefetch. It returns
// immediately; a prefetch failure (panic included) can never affect the primary
// search.
func maybePrefetchGround(ctx context.Context, from, to, date string, opts SearchOptions) {
	if !groundPrefetchEnabled() || groundPrefetchDisabled(ctx) {
		return
	}
	targets := groundPrefetchTargets(from, to, date)
	if len(targets) == 0 {
		return
	}
	go runGroundPrefetch(ctx, targets, opts)
}

// runGroundPrefetch warms targets with bounded concurrency. Each warm is
// isolated; a panic in one never affects its peers nor the caller.
func runGroundPrefetch(parent context.Context, targets []groundPrefetchTarget, opts SearchOptions) {
	detached, cancel := searchctx.DetachedWithin(parent, sharedGroundSearchTimeout)
	defer cancel()
	prefetchCtx := context.WithValue(detached, groundPrefetchCtxKey{}, struct{}{})

	sem := make(chan struct{}, groundPrefetchConcurrency)
	var wg sync.WaitGroup
	for _, t := range targets {
		wg.Add(1)
		sem <- struct{}{}
		go func(t groundPrefetchTarget) {
			defer wg.Done()
			defer func() { <-sem }()
			defer func() { _ = recover() }()
			groundPrefetchFn(prefetchCtx, t, opts)
		}(t)
	}
	wg.Wait()
}

// httpClient is a shared HTTP client with sensible timeouts for FlixBus/RegioJet.
var httpClient = &http.Client{
	Timeout: 30 * time.Second,
}

// Shared rate limiters for FlixBus and RegioJet (used by the shared httpClient).
var (
	flixbusLimiter  = newProviderLimiter(100 * time.Millisecond) // 10 req/s
	regiojetLimiter = newProviderLimiter(100 * time.Millisecond) // 10 req/s
)

// rateLimitedDo executes an HTTP request through the shared client after
// waiting on the provided rate limiter.
func rateLimitedDo(ctx context.Context, limiter *rate.Limiter, req *http.Request) (*http.Response, error) {
	if err := limiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter: %w", err)
	}
	return httpClient.Do(req)
}

// providerResult holds the outcome of a single provider search goroutine.
type providerResult struct {
	routes []models.GroundRoute
	err    error
	name   string
}

// launchProvider starts a provider search in a new goroutine, sending the
// result to the results channel when done.
func launchProvider(wg *sync.WaitGroup, results chan<- providerResult, name string, fn func() ([]models.GroundRoute, error)) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		routes, err := fn()
		results <- providerResult{routes: routes, err: err, name: name}
	}()
}

// SearchOptions configures a ground transport search.
type SearchOptions struct {
	Currency              string   // Default: EUR
	Providers             []string // Filter to specific providers; empty = all
	ExcludeProviders      []string // Skip these providers even when otherwise enabled
	MaxPrice              float64  // 0 = no limit
	Type                  string   // "bus", "train", or empty for all
	NoCache               bool     // bypass response cache
	AllowBrowserFallbacks bool     // opt in to browser/curl/cookie-assisted providers

	// NoHacks opts out of the auto-composed travel-hacks savings engine. The
	// engine is ON by default: a normal search also surfaces the single best
	// cheaper synthesized option (multimodal, cross-border rail, …) in
	// result.HackSaving. Set NoHacks to run a pure naive search.
	NoHacks bool
}

// providerEnabled reports whether a provider should run given an optional
// allow-list (include; empty means all) and a deny-list (exclude, which always
// wins). Pure and case-insensitive so it is unit-tested without the network.
func providerEnabled(name string, include, exclude []string) bool {
	for _, p := range exclude {
		if strings.EqualFold(p, name) {
			return false
		}
	}
	if len(include) == 0 {
		return true
	}
	for _, p := range include {
		if strings.EqualFold(p, name) {
			return true
		}
	}
	return false
}

// SearchByName searches all providers for ground transport between two cities
// given by name. Resolves city names to provider-specific IDs automatically.
func SearchByName(ctx context.Context, from, to, date string, opts SearchOptions) (*models.GroundSearchResult, error) {
	if opts.Currency == "" {
		opts.Currency = "EUR"
	}
	allowBrowserFallbacks := browserFallbacksEnabled(opts)

	// Build cache key from search parameters.
	providerKey := groundProviderKey(opts)
	cacheKey := cache.Key("ground", fmt.Sprintf("%s|%s|%s|%s|%s|%.2f|%s|%t", from, to, date, opts.Currency, providerKey, opts.MaxPrice, opts.Type, allowBrowserFallbacks))

	negKey, negEligible := groundNegKey(from, to, date, opts, providerKey)

	// Check cache unless bypassed.
	if !opts.NoCache {
		if data, ok := groundCache.Get(cacheKey); ok {
			var cached models.GroundSearchResult
			if err := json.Unmarshal(data, &cached); err == nil {
				slog.Debug("ground cache hit", "from", from, "to", to, "date", date)
				maybePrefetchGround(ctx, from, to, date, opts)
				return &cached, nil
			}
		}

		// Negative-result cache: a recent CLEAN "no service" short-circuits the
		// whole provider fan-out for this route+month. Only unfiltered searches
		// participate (negEligible).
		if negEligible && negCacheEnabled() && groundNegCache.Seen(negKey) {
			slog.Debug("ground negative cache hit", "from", from, "to", to, "date", date)
			maybePrefetchGround(ctx, from, to, date, opts)
			return &models.GroundSearchResult{Success: false, Count: 0}, nil
		}
	}

	// Deduplicate concurrent identical in-flight searches. The cache check above
	// already handles TTL-based reuse; singleflight only coalesces truly concurrent
	// requests that both missed the cache.
	result, err := doGroundSearchSingleflight(ctx, cacheKey, func(sharedCtx context.Context) (*models.GroundSearchResult, error) {
		return searchByNameCore(sharedCtx, from, to, date, opts, cacheKey, allowBrowserFallbacks)
	})

	// Speculative prefetch: warm the cache for the likely next query. Best-effort
	// and non-blocking.
	maybePrefetchGround(ctx, from, to, date, opts)

	return result, err
}

func doGroundSearchSingleflight(ctx context.Context, key string, fn func(context.Context) (*models.GroundSearchResult, error)) (*models.GroundSearchResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	ch := groundGroup.DoChan(key, func() (any, error) {
		sharedCtx, cancel := searchctx.DetachedWithin(ctx, sharedGroundSearchTimeout)
		defer cancel()
		return fn(sharedCtx)
	})

	select {
	case <-ctx.Done():
		// Forget timed-out work eagerly so the next caller can launch a fresh
		// execution instead of inheriting the prior caller's deadline-bound run.
		groundGroup.Forget(key)
		return nil, ctx.Err()
	case res := <-ch:
		return sharedGroundResult(res.Val, res.Err)
	}
}

func sharedGroundResult(v any, err error) (*models.GroundSearchResult, error) {
	if err != nil {
		if r, ok := v.(*models.GroundSearchResult); ok {
			return cloneGroundSearchResult(r), err
		}
		return nil, err
	}
	return cloneGroundSearchResult(v.(*models.GroundSearchResult)), nil
}

func cloneGroundSearchResult(shared *models.GroundSearchResult) *models.GroundSearchResult {
	if shared == nil {
		return nil
	}

	// singleflight.Do shares the winner's *GroundSearchResult pointer across
	// concurrent callers. MCP handlers can post-filter Routes and rewrite Count,
	// so each caller needs a private copy of the result header and nested mutable
	// slices/pointers to avoid racing on shared state.
	cp := *shared
	if shared.Routes != nil {
		cp.Routes = make([]models.GroundRoute, len(shared.Routes))
		for i, route := range shared.Routes {
			routeCopy := route
			if route.Amenities != nil {
				routeCopy.Amenities = append([]string(nil), route.Amenities...)
			}
			if route.Legs != nil {
				routeCopy.Legs = make([]models.GroundLeg, len(route.Legs))
				for j, leg := range route.Legs {
					legCopy := leg
					if leg.Amenities != nil {
						legCopy.Amenities = append([]string(nil), leg.Amenities...)
					}
					routeCopy.Legs[j] = legCopy
				}
			}
			if route.SeatsLeft != nil {
				seatsLeft := *route.SeatsLeft
				routeCopy.SeatsLeft = &seatsLeft
			}
			cp.Routes[i] = routeCopy
		}
	}
	return &cp
}

// searchByNameCore performs the actual ground search without singleflight wrapping.
func searchByNameCore(ctx context.Context, from, to, date string, opts SearchOptions, cacheKey string, allowBrowserFallbacks bool) (*models.GroundSearchResult, error) {
	var wg sync.WaitGroup
	results := make(chan providerResult, searchResultBufferCapacity())

	useProvider := func(name string) bool {
		return providerEnabled(name, opts.Providers, opts.ExcludeProviders)
	}

	// Distribusion — ground transport GDS covering bus, ferry, train, airport transfers.
	// Placed first (before individual providers) since it aggregates 2,000+ carriers.
	// Requires DISTRIBUSION_API_KEY to be set; silently skipped otherwise.
	if useProvider("distribusion") && HasDistribusionKey() {
		launchProvider(&wg, results, "distribusion", func() ([]models.GroundRoute, error) {
			return SearchDistribusion(ctx, from, to, date, opts.Currency)
		})
	}

	// FlixBus
	if useProvider("flixbus") {
		launchProvider(&wg, results, "flixbus", func() ([]models.GroundRoute, error) {
			return searchFlixBusByName(ctx, from, to, date, opts)
		})
	}

	// RegioJet
	if useProvider("regiojet") {
		launchProvider(&wg, results, "regiojet", func() ([]models.GroundRoute, error) {
			return searchRegioJetByName(ctx, from, to, date, opts)
		})
	}

	// Rome2Rio — multimodal route DISCOVERY (not bookable fares). Surfaces the
	// set of ways to travel A->B, including multi-leg combinations (e.g. "ferry
	// to a hub, then fly") that single-mode providers never produce, with an
	// indicative price range per option. Real per-leg prices still come from the
	// dedicated providers above; failure is isolated by launchProvider so a
	// Rome2Rio bot-wall never breaks the bookable results.
	if useProvider("rome2rio") {
		launchProvider(&wg, results, "rome2rio", func() ([]models.GroundRoute, error) {
			return SearchRome2Rio(ctx, from, to, allowBrowserFallbacks)
		})
	}

	// Eurostar — only if both cities have Eurostar stations.
	// Search both Snap (last-minute deals) and regular fares in parallel so the
	// user sees both options (e.g. "eurostar snap GBP 39" and "eurostar GBP 130").
	if (useProvider("eurostar") || useProvider("eurostar snap")) && HasEurostarRoute(from, to) {
		// Eurostar cheapestFaresSearch needs a date range (not a single day).
		// Use the requested date as start, +7 days as end.
		endDate := date // fallback
		if t, err := models.ParseDate(date); err == nil {
			endDate = t.AddDate(0, 0, 7).Format("2006-01-02")
		}

		// Snap fares goroutine — Snap fares are released up to 14 days
		// before travel and only on specific routes (see eurostarSnapRoutes).
		// Search today→today+14 so Snap deals surface regardless of the
		// user's search date.
		if HasEurostarSnapRoute(from, to) {
			snapStart := time.Now().Format("2006-01-02")
			snapEnd := time.Now().AddDate(0, 0, 14).Format("2006-01-02")
			launchProvider(&wg, results, "eurostar snap", func() ([]models.GroundRoute, error) {
				return SearchEurostar(ctx, from, to, snapStart, snapEnd, opts.Currency, true)
			})
		}

		// Regular fares goroutine.
		launchProvider(&wg, results, "eurostar", func() ([]models.GroundRoute, error) {
			return SearchEurostar(ctx, from, to, date, endDate, opts.Currency, false)
		})
	}

	// NS (Dutch Railways) — only if at least one city has an NS station.
	if useProvider("ns") && (HasNSStation(from) || HasNSStation(to)) {
		launchProvider(&wg, results, "ns", func() ([]models.GroundRoute, error) {
			return SearchNS(ctx, from, to, date, opts.Currency)
		})
	}

	// Deutsche Bahn — if at least one city has a DB station (covers most European rail).
	if useProvider("db") && (HasDBStation(from) || HasDBStation(to)) {
		launchProvider(&wg, results, "db", func() ([]models.GroundRoute, error) {
			return SearchDeutscheBahn(ctx, from, to, date, opts.Currency)
		})
	}

	// SNCF — only if at least one city is French.
	if useProvider("sncf") && HasSNCFRoute(from, to) {
		launchProvider(&wg, results, "sncf", func() ([]models.GroundRoute, error) {
			return SearchSNCF(ctx, from, to, date, opts.Currency, allowBrowserFallbacks)
		})
	}

	// Trainline — train aggregator (covers SNCF, Eurostar, DB, Trenitalia, etc.)
	if useProvider("trainline") && HasTrainlineStation(from) && HasTrainlineStation(to) {
		launchProvider(&wg, results, "trainline", func() ([]models.GroundRoute, error) {
			return SearchTrainline(ctx, from, to, date, opts.Currency, allowBrowserFallbacks)
		})
	}

	// ÖBB (Austrian Federal Railways) — Austria and neighbouring countries.
	if useProvider("oebb") && HasOebbRoute(from, to) {
		launchProvider(&wg, results, "oebb", func() ([]models.GroundRoute, error) {
			return SearchOebb(ctx, from, to, date, opts.Currency)
		})
	}

	// Digitransit (VR Finnish Railways) — only if at least one city has a Finnish station.
	if (useProvider("digitransit") || useProvider("vr")) && (HasDigitransitStation(from) || HasDigitransitStation(to)) {
		launchProvider(&wg, results, "vr", func() ([]models.GroundRoute, error) {
			return SearchDigitransit(ctx, from, to, date, opts.Currency)
		})
	}

	// Renfe (Spain) — only if at least one city has a Renfe station (Spanish rail).
	if useProvider("renfe") && HasRenfeRoute(from, to) {
		launchProvider(&wg, results, "renfe", func() ([]models.GroundRoute, error) {
			return SearchRenfe(ctx, from, to, date, opts.Currency)
		})
	}

	// Tallink/Silja Line — ferry routes in the Baltic Sea.
	if useProvider("tallink") && HasTallinkRoute(from, to) {
		launchProvider(&wg, results, "tallink", func() ([]models.GroundRoute, error) {
			return SearchTallink(ctx, from, to, date, opts.Currency)
		})
	}

	// Stena Line — ferry routes across the North Sea and Baltic Sea.
	if useProvider("stenaline") && HasStenaLineRoute(from, to) {
		launchProvider(&wg, results, "stenaline", func() ([]models.GroundRoute, error) {
			return SearchStenaLine(ctx, from, to, date, opts.Currency)
		})
	}

	// DFDS — ferry routes across the North Sea and Baltic Sea.
	if useProvider("dfds") && HasDFDSRoute(from, to) {
		launchProvider(&wg, results, "dfds", func() ([]models.GroundRoute, error) {
			return SearchDFDS(ctx, from, to, date, opts.Currency)
		})
	}

	// Viking Line — ferry routes in the Baltic Sea (Helsinki–Tallinn, Helsinki–Stockholm,
	// Turku–Stockholm, Stockholm–Mariehamn).
	if useProvider("vikingline") && HasVikingLineRoute(from, to) {
		launchProvider(&wg, results, "vikingline", func() ([]models.GroundRoute, error) {
			return SearchVikingLine(ctx, from, to, date, opts.Currency)
		})
	}

	// Eckerö Line — Helsinki ↔ Tallinn ferry (M/S Finlandia).
	if useProvider("eckeroline") && HasEckeroLineRoute(from, to) {
		launchProvider(&wg, results, "eckeroline", func() ([]models.GroundRoute, error) {
			return SearchEckeroLine(ctx, from, to, date, opts.Currency)
		})
	}

	// Finnlines — Helsinki ↔ Travemünde, Naantali ↔ Kapellskär, Malmö ↔ Świnoujście.
	if useProvider("finnlines") && HasFinnlinesRoute(from, to) {
		launchProvider(&wg, results, "finnlines", func() ([]models.GroundRoute, error) {
			return SearchFinnlines(ctx, from, to, date, opts.Currency)
		})
	}

	// Ferryhopper — Greek ferry aggregator (Aegean, Ionian, Adriatic seas).
	// Uses the public Ferryhopper MCP endpoint; no API key required.
	// Accepts free-form location names so it is always attempted.
	if useProvider("ferryhopper") {
		launchProvider(&wg, results, "ferryhopper", func() ([]models.GroundRoute, error) {
			return SearchFerryhopper(ctx, from, to, date, opts.Currency)
		})
	}

	// European Sleeper — night train Brussels→Amsterdam→Berlin→Dresden→Prague.
	if useProvider("european_sleeper") && HasEuropeanSleeperRoute(from, to) {
		launchProvider(&wg, results, "european_sleeper", func() ([]models.GroundRoute, error) {
			return SearchEuropeanSleeper(ctx, from, to, date, opts.Currency)
		})
	}

	// Snälltåget — Swedish night trains (Stockholm→Malmö, Stockholm→Åre, Stockholm→Berlin).
	if useProvider("snalltaget") && HasSnalltagetRoute(from, to) {
		launchProvider(&wg, results, "snalltaget", func() ([]models.GroundRoute, error) {
			return SearchSnalltaget(ctx, from, to, date, opts.Currency)
		})
	}

	// Transitous — coordinate-based, always available as a fallback.
	// Requires geocoding city names to coordinates; skipped if geocoding fails.
	if useProvider("transitous") {
		launchProvider(&wg, results, "transitous", func() ([]models.GroundRoute, error) {
			return searchTransitousByName(ctx, from, to, date)
		})
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	var allRoutes []models.GroundRoute
	var errors []string
	for r := range results {
		if r.err != nil {
			if isProviderNotApplicable(r.err) {
				slog.Debug("ground provider not applicable", "provider", r.name, "reason", r.err)
			} else {
				slog.Warn("ground provider error", "provider", r.name, "error", r.err)
				errors = append(errors, fmt.Sprintf("%s: %v", r.name, r.err))
			}
			continue
		}
		allRoutes = append(allRoutes, r.routes...)
	}

	// Filter/deduplicate in one pass while preserving the current semantics:
	// unavailable routes are removed first, then duplicate routes are suppressed
	// before MaxPrice and Type filters are applied.
	allRoutes = filterGroundRoutes(allRoutes, opts)

	// Collapse the same physical connection returned by multiple providers into
	// one route carrying every provider as a PriceSource (cheapest headline).
	allRoutes = models.ResolveGroundSources(allRoutes)

	// Sort by price
	sort.Slice(allRoutes, func(i, j int) bool {
		return allRoutes[i].Price < allRoutes[j].Price
	})

	annotateGroundConfidence(allRoutes, time.Now())

	result := &models.GroundSearchResult{
		Success: len(allRoutes) > 0,
		Count:   len(allRoutes),
		Routes:  allRoutes,
	}
	if len(allRoutes) == 0 && len(errors) > 0 {
		result.Error = strings.Join(errors, "; ")
	}

	// Auto-compose the savings engine: surface the single best cheaper
	// synthesized option alongside the naive routes (before caching so cache
	// hits carry it too), unless opted out or this is a nested detector search.
	attachGroundHackSaving(ctx, result, from, to, date, opts)

	// Cache successful results.
	if result.Success && !opts.NoCache {
		if data, err := json.Marshal(result); err == nil {
			groundCache.Set(cacheKey, data, groundCacheTTL)
		}
	}

	// Negative-result cache: mark a route as "no service" only on a CLEAN empty —
	// zero routes AND no provider errors/timeouts. A run with any error is left
	// uncached so it retries. Filtered searches are excluded by groundNegKey.
	if !opts.NoCache && len(allRoutes) == 0 && len(errors) == 0 && negCacheEnabled() {
		if negKey, ok := groundNegKey(from, to, date, opts, groundProviderKey(opts)); ok {
			groundNegCache.Mark(negKey)
		}
	}

	return result, nil
}

// groundProviderKey returns the canonical provider-set component of the cache
// key (sorted provider list, or "all").
func groundProviderKey(opts SearchOptions) string {
	if len(opts.Providers) == 0 {
		return "all"
	}
	sorted := make([]string, len(opts.Providers))
	copy(sorted, opts.Providers)
	sort.Strings(sorted)
	return strings.Join(sorted, ",")
}

// isProviderNotApplicable returns true when the error indicates that a provider
// simply does not serve the requested route (e.g. "no DB station for Helsinki")
// or that the provider was throttled during a burst of calls. Both are expected
// during broad multi-provider searches and should be logged at DEBUG level
// annotateGroundConfidence attaches an honest bookability Confidence to every
// ground route (innovation #3), scored from the just-fetched quote freshness,
// provider reliability, and multi-source corroboration via the shared fareintel
// scorer. Routes with no usable signal are left unrated, never faked.
func annotateGroundConfidence(routes []models.GroundRoute, now time.Time) {
	for i := range routes {
		r := &routes[i]
		c := fareintel.ScoreConfidence(fareintel.ConfidenceInput{
			Price:           r.Price,
			Currency:        r.Currency,
			Provider:        r.Provider,
			RetrievedAt:     now,
			Now:             now,
			Sources:         r.Sources,
			SeparateTickets: r.Transfers > 0 && r.Type == "mixed",
		})
		r.Confidence = &c
	}
}

// rather than WARN to avoid polluting normal operation output.
func isProviderNotApplicable(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, " station for ") ||
		strings.Contains(msg, " city found for ") ||
		strings.Contains(msg, " port for ") ||
		strings.Contains(msg, "no route for ") ||
		strings.Contains(msg, "no Tallink route") ||
		strings.Contains(msg, "no Eurostar route") ||
		strings.Contains(msg, "no DFDS route") ||
		strings.Contains(msg, "no Stena Line route") ||
		strings.Contains(msg, "rate limiter: rate: Wait") ||
		strings.Contains(msg, "would exceed context deadline") ||
		strings.Contains(msg, "context deadline exceeded")
}

func browserFallbacksEnabled(opts SearchOptions) bool {
	if opts.AllowBrowserFallbacks {
		return true
	}

	raw := strings.TrimSpace(os.Getenv("TRVL_ALLOW_BROWSER_FALLBACKS"))
	if raw == "" {
		return false
	}

	enabled, err := strconv.ParseBool(raw)
	return err == nil && enabled
}

type groundRouteKey struct {
	provider      string
	departureTime string
	arrivalTime   string
	priceCents    int64
}

func filterGroundRoutes(routes []models.GroundRoute, opts SearchOptions) []models.GroundRoute {
	filtered := routes[:0]
	seen := make(map[groundRouteKey]struct{}, len(routes))
	hasTypeFilter := opts.Type != ""

	for _, route := range routes {
		if !shouldKeepGroundRoute(route) {
			continue
		}

		key := groundRouteDedupKey(route)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}

		if opts.MaxPrice > 0 && route.Price > opts.MaxPrice {
			continue
		}
		if hasTypeFilter && !strings.EqualFold(route.Type, opts.Type) {
			continue
		}

		filtered = append(filtered, route)
	}

	return filtered
}

func groundRouteDedupKey(route models.GroundRoute) groundRouteKey {
	return groundRouteKey{
		provider:      route.Provider,
		departureTime: route.Departure.Time,
		arrivalTime:   route.Arrival.Time,
		priceCents:    roundedPriceCents(route.Price),
	}
}

func roundedPriceCents(price float64) int64 {
	return int64(math.Round(price * 100))
}

func deduplicateGroundRoutes(routes []models.GroundRoute) []models.GroundRoute {
	seen := make(map[groundRouteKey]struct{}, len(routes))
	result := routes[:0]
	for _, r := range routes {
		key := groundRouteDedupKey(r)
		if _, exists := seen[key]; !exists {
			seen[key] = struct{}{}
			result = append(result, r)
		}
	}
	return result
}

// scheduleOnlyProviders is the set of providers whose results are kept even
// when price is 0 (they provide schedule data without live pricing).
var scheduleOnlyProviders = map[string]bool{
	"distribusion": true, "transitous": true, "db": true, "ns": true,
	"oebb": true, "vr": true, "european_sleeper": true, "snalltaget": true,
	"tallink": true, "stenaline": true,
	"dfds": true, "vikingline": true, "eckeroline": true, "finnlines": true,
	"ferryhopper": true,
}

func shouldKeepGroundRoute(route models.GroundRoute) bool {
	return route.Price > 0 || scheduleOnlyProviders[strings.ToLower(route.Provider)]
}

func filterUnavailableGroundRoutes(routes []models.GroundRoute) []models.GroundRoute {
	filtered := routes[:0]
	for _, route := range routes {
		if shouldKeepGroundRoute(route) {
			filtered = append(filtered, route)
		}
	}
	return filtered
}

// resolveAndSearch is a generic helper that resolves city names via an
// autocomplete function, then delegates to a search function that receives the
// resolved from/to cities. It eliminates the identical resolve-from / resolve-to
// / check-empty / call-search boilerplate shared by FlixBus and RegioJet.
func resolveAndSearch[T any](
	ctx context.Context,
	from, to string,
	providerName string,
	autoComplete func(ctx context.Context, query string) ([]T, error),
	search func(fromCity, toCity T) ([]models.GroundRoute, error),
) ([]models.GroundRoute, error) {
	fromCities, err := autoComplete(ctx, from)
	if err != nil {
		return nil, fmt.Errorf("resolve from city: %w", err)
	}
	if len(fromCities) == 0 {
		return nil, fmt.Errorf("no %s city found for %q", providerName, from)
	}

	toCities, err := autoComplete(ctx, to)
	if err != nil {
		return nil, fmt.Errorf("resolve to city: %w", err)
	}
	if len(toCities) == 0 {
		return nil, fmt.Errorf("no %s city found for %q", providerName, to)
	}

	return search(fromCities[0], toCities[0])
}

// searchFlixBusByName resolves city names and searches FlixBus.
func searchFlixBusByName(ctx context.Context, from, to, date string, opts SearchOptions) ([]models.GroundRoute, error) {
	routes, err := resolveAndSearch(ctx, from, to, "FlixBus",
		FlixBusAutoComplete,
		func(fromCity, toCity FlixBusCity) ([]models.GroundRoute, error) {
			results, err := SearchFlixBus(ctx, fromCity.ID, toCity.ID, date, opts)
			if err != nil {
				return nil, err
			}
			// Enrich city names
			for i := range results {
				if results[i].Departure.City == "" {
					results[i].Departure.City = fromCity.Name
				}
				if results[i].Arrival.City == "" {
					results[i].Arrival.City = toCity.Name
				}
			}
			return results, nil
		},
	)
	return routes, err
}

// searchRegioJetByName resolves city names and searches RegioJet.
func searchRegioJetByName(ctx context.Context, from, to, date string, opts SearchOptions) ([]models.GroundRoute, error) {
	return resolveAndSearch(ctx, from, to, "RegioJet",
		RegioJetAutoComplete,
		func(fromCity, toCity RegioJetCity) ([]models.GroundRoute, error) {
			return SearchRegioJet(ctx, fromCity.ID, toCity.ID, date, opts)
		},
	)
}

// searchTransitousByName geocodes city names to coordinates and searches Transitous.
func searchTransitousByName(ctx context.Context, from, to, date string) ([]models.GroundRoute, error) {
	fromGeo, err := geocodeCity(ctx, from)
	if err != nil {
		return nil, fmt.Errorf("geocode from city: %w", err)
	}
	toGeo, err := geocodeCity(ctx, to)
	if err != nil {
		return nil, fmt.Errorf("geocode to city: %w", err)
	}
	return SearchTransitous(ctx, fromGeo.lat, fromGeo.lon, toGeo.lat, toGeo.lon, date)
}

// geoCoord holds a latitude/longitude pair from geocoding.
type geoCoord struct {
	lat float64
	lon float64
}

// geoCityCache caches city name to coordinate lookups.
var geoCityCache = struct {
	sync.RWMutex
	entries map[string]geoCoord
}{entries: make(map[string]geoCoord)}

// geocodeCity resolves a city name to coordinates using Nominatim.
func geocodeCity(ctx context.Context, city string) (geoCoord, error) {
	key := strings.ToLower(strings.TrimSpace(city))

	geoCityCache.RLock()
	if entry, ok := geoCityCache.entries[key]; ok {
		geoCityCache.RUnlock()
		return entry, nil
	}
	geoCityCache.RUnlock()

	params := url.Values{
		"q":      {city},
		"format": {"json"},
		"limit":  {"1"},
	}
	apiURL := "https://nominatim.openstreetmap.org/search?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return geoCoord{}, err
	}
	req.Header.Set("User-Agent", "trvl/1.0 (travel agent; github.com/MikkoParkkola/trvl)")

	resp, err := httpClient.Do(req)
	if err != nil {
		return geoCoord{}, fmt.Errorf("nominatim: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return geoCoord{}, fmt.Errorf("nominatim: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if err != nil {
		return geoCoord{}, fmt.Errorf("nominatim read: %w", err)
	}

	var results []struct {
		Lat string `json:"lat"`
		Lon string `json:"lon"`
	}
	if err := json.Unmarshal(body, &results); err != nil {
		return geoCoord{}, fmt.Errorf("nominatim decode: %w", err)
	}
	if len(results) == 0 {
		return geoCoord{}, fmt.Errorf("no geocoding results for %q", city)
	}

	var lat, lon float64
	if _, err := fmt.Sscanf(results[0].Lat, "%f", &lat); err != nil {
		return geoCoord{}, fmt.Errorf("parse lat %q: %w", results[0].Lat, err)
	}
	if _, err := fmt.Sscanf(results[0].Lon, "%f", &lon); err != nil {
		return geoCoord{}, fmt.Errorf("parse lon %q: %w", results[0].Lon, err)
	}

	coord := geoCoord{lat: lat, lon: lon}
	geoCityCache.Lock()
	geoCityCache.entries[key] = coord
	geoCityCache.Unlock()

	return coord, nil
}
