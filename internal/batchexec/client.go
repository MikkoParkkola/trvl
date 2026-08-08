// Package batchexec provides an HTTP client for Google's internal batchexecute API.
//
// Google's travel frontends (Flights, Hotels) communicate via a protocol that
// POSTs form-encoded "f.req" payloads and returns JSON with an anti-XSSI prefix.
// This package handles TLS fingerprint impersonation (Chrome via utls), request
// encoding, and response decoding.
package batchexec

import (
	"context"
	cryptorand "crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	neturl "net/url"
	"strings"
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/cache"
	"github.com/MikkoParkkola/trvl/internal/logredact"
	"github.com/MikkoParkkola/trvl/internal/stealth"
	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"
	"golang.org/x/time/rate"
)

// Endpoint constants for Google Travel APIs.
// The hl (language) and gl (country) parameters control the currency and locale
// of results. Without them, Google uses IP-based geolocation which may return
// unexpected currencies (e.g., PLN when connecting from Poland).
const (
	FlightsURL       = "https://www.google.com/_/FlightsFrontendUi/data/travel.frontend.flights.FlightsFrontendService/GetShoppingResults?hl=en"
	ExploreURL       = "https://www.google.com/_/FlightsFrontendUi/data/travel.frontend.flights.FlightsFrontendService/GetExploreDestinations?hl=en"
	CalendarGraphURL = "https://www.google.com/_/FlightsFrontendUi/data/travel.frontend.flights.FlightsFrontendService/GetCalendarGraph?hl=en"
	CalendarGridURL  = "https://www.google.com/_/FlightsFrontendUi/data/travel.frontend.flights.FlightsFrontendService/GetCalendarGrid?hl=en"
	HotelsURL        = "https://www.google.com/_/TravelFrontendUi/data/batchexecute?hl=en"
)

// chromeUA is a recent Chrome User-Agent string.
const chromeUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

// Default retry configuration.
const (
	defaultMaxRetries  = 3
	defaultBaseBackoff = 1 * time.Second
)

// Cache TTL constants for different endpoint types.
const (
	FlightCacheTTL      = 5 * time.Minute
	HotelCacheTTL       = 10 * time.Minute
	DestinationCacheTTL = 1 * time.Hour
)

// Client wraps an http.Client with Chrome TLS fingerprint impersonation via utls.
// It includes a token bucket rate limiter, retry with exponential backoff,
// and an in-memory response cache.
type Client struct {
	http    *http.Client
	limiter *rate.Limiter
	cache   *cache.Cache
	noCache bool

	// stealthHTTP is the opt-in, operator-authorized stealth transport. It is
	// the full Chrome 146 HTTP/2 fingerprint client (ChromeHTTPClient), which
	// advertises native ALPN ["h2","http/1.1"] and speaks HTTP/2 — the profile
	// that clears Datadome-class bot detection. It is built lazily on first
	// authorized use so the default (non-stealth) path is unaffected, and it is
	// reused thereafter for connection pooling. Access is guarded by stealthOnce.
	stealthHTTP *http.Client
	stealthOnce sync.Once

	// reuseTransportForStealth makes stealthClient reuse this client's existing
	// transport instead of building the real Chrome fingerprint client.
	//
	// It exists so the stealth path can be exercised offline and
	// deterministically. It replaces a type assertion on a test-only transport
	// type, which forced that type to live in this production file and made the
	// exported test constructor reachable from production code (trvl#539).
	// A behaviour flag says what the branch actually decides; the type
	// assertion said "am I in a test", which production has no business asking.
	reuseTransportForStealth bool
}

// NewClient creates a Client that impersonates Chrome's TLS fingerprint.
//
// Chrome's ClientHello is used for TLS fingerprinting, but we force HTTP/1.1
// via ALPN to avoid the complexity of HTTP/2 framing with custom TLS connections.
// Google's servers support HTTP/1.1 and this is sufficient for API access.
//
// The client includes a token bucket rate limiter at 10 requests/second with
// burst of 1, and automatic retry with exponential backoff for 429/5xx errors.
func NewClient() *Client {
	transport := &http.Transport{
		DialTLSContext:      dialTLSChromeHTTP1,
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
		// Force HTTP/1.1 — we handle TLS ourselves and net/http can't do HTTP/2
		// on externally-provided TLS connections without extra wiring.
		ForceAttemptHTTP2: false,
	}
	return &Client{
		http: &http.Client{
			Transport: transport,
			Timeout:   20 * time.Second,
		},
		limiter: rate.NewLimiter(rate.Limit(10), 1),
		cache:   cache.New(),
	}
}

// SetNoCache disables the response cache for this client.
func (c *Client) SetNoCache(disable bool) {
	c.noCache = disable
}

// getCached returns a cached response if available and caching is enabled.
func (c *Client) getCached(endpoint, payload string) ([]byte, bool) {
	if c.noCache || c.cache == nil {
		return nil, false
	}
	return c.cache.Get(cache.Key(endpoint, payload))
}

// setCached stores a response in the cache with the appropriate TTL.
func (c *Client) setCached(endpoint, payload string, data []byte, ttl time.Duration) {
	if c.noCache || c.cache == nil {
		return
	}
	c.cache.Set(cache.Key(endpoint, payload), data, ttl)
}

// SetRateLimit changes the rate limiter to allow rps requests per second.
// A burst of 1 is used to enforce strict spacing between requests.
func (c *Client) SetRateLimit(rps float64) {
	c.limiter = rate.NewLimiter(rate.Limit(rps), 1)
}

// dialTLSChromeHTTP1 dials a TCP connection and wraps it with a utls client
// that impersonates Chrome 146's TLS ClientHello but forces HTTP/1.1 via ALPN.
//
// We start from Chrome146Spec() and override the ALPN extension to only advertise
// "http/1.1". The UClient is created with HelloCustom so ApplyPreset installs our
// modified spec rather than ignoring it in favour of a built-in profile.
//
// Coverage exclusion: creates a raw TLS connection with Chrome fingerprint.
// Not unit-testable: requires real TCP connection + TLS handshake with remote server.
// Covered by integration tests (proof_test.go).
func dialTLSChromeHTTP1(ctx context.Context, network, addr string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("split host: %w", err)
	}

	return dialTLSChromeHTTP1WithConfig(ctx, network, addr, &utls.Config{ServerName: host})
}

func dialTLSChromeHTTP1WithConfig(ctx context.Context, network, addr string, tlsConfig *utls.Config) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	rawConn, err := dialer.DialContext(ctx, network, addr)
	if err != nil {
		return nil, fmt.Errorf("dial tcp: %w", err)
	}

	// Build a Chrome 146 spec but with ALPN forced to HTTP/1.1.
	spec := Chrome146Spec()
	for _, ext := range spec.Extensions {
		if alpn, ok := ext.(*utls.ALPNExtension); ok {
			alpn.AlpnProtocols = []string{"http/1.1"}
			break
		}
	}

	// HelloCustom tells utls to use our spec verbatim instead of a preset.
	uConn := utls.UClient(rawConn, tlsConfig, utls.HelloCustom)

	if err := uConn.ApplyPreset(&spec); err != nil {
		_ = uConn.Close()
		return nil, fmt.Errorf("apply preset: %w", err)
	}

	if err := uConn.HandshakeContext(ctx); err != nil {
		_ = uConn.Close()
		return nil, fmt.Errorf("utls handshake: %w", err)
	}

	return uConn, nil
}

// Get performs a GET request with Chrome headers.
// The request is subject to rate limiting and automatic retry on 429/5xx.
func (c *Client) Get(ctx context.Context, url string) (int, []byte, error) {
	return c.GetStealth(ctx, url, false)
}

// GetStealth is Get with an explicit, operator-authorized stealth toggle. When
// stealthRequested is true AND the request host is on the operator allowlist,
// the request uses the Chrome HTTP/2 stealth transport; otherwise it runs the
// normal path (with a single refusal log line for a requested-but-unauthorized
// host). stealthRequested=false is byte-identical to Get.
func (c *Client) GetStealth(ctx context.Context, url string, stealthRequested bool) (int, []byte, error) {
	httpClient, _ := c.transportForStealth(stealthRequested, url)
	return c.doWithRetryVia(ctx, httpClient, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", chromeUA)
		return req, nil
	})
}

// GetWithCookie performs a GET request like Get but appends the given raw
// cookie string to the Cookie header. This is used to bypass Google's EU
// consent page by pre-seeding SOCS/CONSENT cookies on a retry.
func (c *Client) GetWithCookie(ctx context.Context, url, cookieHeader string) (int, []byte, error) {
	return c.doWithRetry(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", chromeUA)
		if cookieHeader != "" {
			req.Header.Set("Cookie", cookieHeader)
		}
		return req, nil
	})
}

// GetWithHeaders performs a GET request like Get but applies caller-supplied
// request headers on top of the Chrome User-Agent. Empty header values are
// skipped. Used by providers that need a precise Accept header and/or a
// pre-seeded Cookie (e.g. Booking.com's aws-waf-token) to clear a WAF.
func (c *Client) GetWithHeaders(ctx context.Context, url string, headers map[string]string) (int, []byte, error) {
	return c.doWithRetry(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", chromeUA)
		for k, v := range headers {
			if v != "" {
				req.Header.Set(k, v)
			}
		}
		return req, nil
	})
}

// PostForm sends a POST with form-encoded body to the given URL. It sets the
// Content-Type to application/x-www-form-urlencoded and uses a Chrome User-Agent.
// The request is subject to rate limiting and automatic retry on 429/5xx.
func (c *Client) PostForm(ctx context.Context, url, formBody string) (int, []byte, error) {
	return c.PostFormStealth(ctx, url, formBody, false)
}

// PostFormStealth is PostForm with an explicit, operator-authorized stealth
// toggle. When stealthRequested is true AND the request host is on the operator
// allowlist (TRVL_STEALTH_ALLOWLIST), the request uses the Chrome HTTP/2 stealth
// transport; otherwise it runs the normal path (logging a single refusal line
// for a requested-but-unauthorized host). stealthRequested=false is
// byte-identical to PostForm — stealth is strictly opt-in and scope-fenced.
func (c *Client) PostFormStealth(ctx context.Context, url, formBody string, stealthRequested bool) (int, []byte, error) {
	httpClient, _ := c.transportForStealth(stealthRequested, url)
	return c.doWithRetryVia(ctx, httpClient, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(formBody))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=UTF-8")
		req.Header.Set("User-Agent", chromeUA)
		req.Header.Set("Accept", "*/*")
		req.Header.Set("Accept-Language", "en-US,en;q=0.9")
		req.Header.Set("Origin", "https://www.google.com")
		req.Header.Set("Referer", "https://www.google.com/travel/flights")
		return req, nil
	})
}

// doWithRetry executes an HTTP request with rate limiting and retry logic.
// It retries up to 3 times on 429 (rate limit) and 5xx (server error) responses,
// with exponential backoff (1s, 2s, 4s) plus jitter (+-25%).
// Client errors (4xx except 429) are not retried.
func (c *Client) doWithRetry(ctx context.Context, buildReq func() (*http.Request, error)) (int, []byte, error) {
	return c.doWithRetryVia(ctx, c.http, buildReq)
}

// doWithRetryVia is doWithRetry parameterized by the underlying *http.Client to
// use for the round-trip. The default fetch path passes c.http (Chrome HTTP/1.1
// fingerprint); the authorized stealth path passes the lazily-built HTTP/2
// Chrome 146 client. Rate limiting, retry, and logging are identical regardless
// of transport so behavior is observable and consistent.
func (c *Client) doWithRetryVia(ctx context.Context, httpClient *http.Client, buildReq func() (*http.Request, error)) (int, []byte, error) {
	var lastStatus int
	var lastBody []byte
	var lastErr error

	for attempt := range defaultMaxRetries + 1 {
		// Wait for rate limiter before each attempt.
		if err := c.limiter.Wait(ctx); err != nil {
			return 0, nil, fmt.Errorf("rate limiter: %w", err)
		}
		slog.Debug("rate_limit", "waiting", true)

		req, err := buildReq()
		if err != nil {
			return 0, nil, err
		}

		slog.Debug("request", "method", req.Method, "url", logredact.URL(req.URL.String()), "payload_len", req.ContentLength)
		start := time.Now()

		resp, err := httpClient.Do(req)
		if err != nil {
			lastErr = err
			if attempt < defaultMaxRetries {
				backoff := defaultBaseBackoff << attempt
				// net/http wraps transport failures in *url.Error, whose
				// Error() embeds the full request URL, query string included.
				slog.Warn("retry", "attempt", attempt, "error", logredact.Err(err), "backoff_ms", backoff.Milliseconds())
				if sleepErr := backoffSleep(ctx, attempt); sleepErr != nil {
					return 0, nil, sleepErr
				}
				continue
			}
			return 0, nil, lastErr
		}

		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024)) // 10MB limit
		_ = resp.Body.Close()
		elapsed := time.Since(start)

		if readErr != nil {
			lastErr = readErr
			if attempt < defaultMaxRetries {
				backoff := defaultBaseBackoff << attempt
				slog.Warn("retry", "attempt", attempt, "error", logredact.Err(readErr), "backoff_ms", backoff.Milliseconds())
				if sleepErr := backoffSleep(ctx, attempt); sleepErr != nil {
					return 0, nil, sleepErr
				}
				continue
			}
			return 0, nil, lastErr
		}

		slog.Debug("response", "status", resp.StatusCode, "body_len", len(body), "duration_ms", elapsed.Milliseconds())

		lastStatus = resp.StatusCode
		lastBody = body
		lastErr = nil

		// Don't retry on success or non-retryable client errors.
		if !isRetryable(resp.StatusCode) {
			return lastStatus, lastBody, nil
		}

		// Retryable error — backoff before next attempt (unless this was the last).
		if attempt < defaultMaxRetries {
			backoff := defaultBaseBackoff << attempt
			slog.Warn("retry", "attempt", attempt, "status", resp.StatusCode, "backoff_ms", backoff.Milliseconds())
			if sleepErr := backoffSleep(ctx, attempt); sleepErr != nil {
				return 0, nil, sleepErr
			}
		}
	}

	// All retries exhausted.
	if lastErr != nil {
		return 0, nil, lastErr
	}
	return lastStatus, lastBody, nil
}

// isRetryable returns true for HTTP status codes that should trigger a retry:
// 429 (Too Many Requests) and 5xx (server errors).
func isRetryable(statusCode int) bool {
	return statusCode == 429 || statusCode >= 500
}

// backoffSleep sleeps for exponential backoff duration with jitter.
// Base delay is 1s, doubling each attempt: 1s, 2s, 4s.
// Jitter adds +-25% randomness to prevent thundering herd.
func backoffSleep(ctx context.Context, attempt int) error {
	base := defaultBaseBackoff << attempt // 1s, 2s, 4s
	// Add cryptographically sourced jitter: +-25%. When the OS random source is
	// unavailable, use the midpoint rather than falling back to a weak PRNG.
	var random [8]byte
	unit := 0.5
	if _, err := cryptorand.Read(random[:]); err == nil {
		unit = float64(binary.LittleEndian.Uint64(random[:])>>11) / (1 << 53)
	}
	jitter := time.Duration(float64(base) * (0.75 + unit*0.5))

	timer := time.NewTimer(jitter)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// SearchFlights posts an encoded flight search payload to the Flights endpoint
// and returns the raw response body.
//
// Results are cached for 5 minutes. Use SetNoCache(true) to bypass.
//
// Coverage exclusion: thin wrapper around doWithRetry with endpoint-specific URL.
// doWithRetry is thoroughly tested (client_test.go, client_extra_test.go).
// Covered by integration proof tests.
func (c *Client) SearchFlights(ctx context.Context, encodedFilters string) (int, []byte, error) {
	return c.SearchFlightsGL(ctx, encodedFilters, "")
}

// SearchFlightsGL is like SearchFlights but optionally appends gl= (geolocation)
// and/or curr= (currency) query parameters.
//   - gl controls the country context (affects which fares are shown)
//   - curr controls the display currency (ISO 4217, e.g. "USD", "EUR")
//
// When both are empty, the request uses the default FlightsURL (IP-based defaults).
// Inspired by @Alorse's contribution in PR #33.
func (c *Client) SearchFlightsGL(ctx context.Context, encodedFilters, gl string) (int, []byte, error) {
	return c.SearchFlightsGLCurr(ctx, encodedFilters, gl, "")
}

// SearchFlightsGLStealth is SearchFlightsGL with an explicit, operator-authorized
// stealth toggle. The flight search path passes opts.Stealth here; the gate
// (host allowlist) is enforced inside the POST. stealthRequested=false is
// byte-identical to SearchFlightsGL.
func (c *Client) SearchFlightsGLStealth(ctx context.Context, encodedFilters, gl string, stealthRequested bool) (int, []byte, error) {
	return c.SearchFlightsGLCurrStealth(ctx, encodedFilters, gl, "", stealthRequested)
}

// SearchFlightsGLCurr is the full variant with both gl= and curr= parameters.
func (c *Client) SearchFlightsGLCurr(ctx context.Context, encodedFilters, gl, curr string) (int, []byte, error) {
	return c.SearchFlightsGLCurrStealth(ctx, encodedFilters, gl, curr, false)
}

// SearchFlightsGLCurrStealth is SearchFlightsGLCurr with an explicit,
// operator-authorized stealth toggle threaded through to the underlying POST.
// The response cache is shared with the non-stealth path (the transport does not
// change the response body), so a cached entry is returned regardless of the
// stealth flag. stealthRequested=false is byte-identical to SearchFlightsGLCurr.
func (c *Client) SearchFlightsGLCurrStealth(ctx context.Context, encodedFilters, gl, curr string, stealthRequested bool) (int, []byte, error) {
	url := FlightsURL
	if gl != "" {
		url += "&gl=" + gl
	}
	if curr != "" {
		url += "&curr=" + curr
	}
	payload := "f.req=" + encodedFilters
	if data, ok := c.getCached(url, payload); ok {
		return 200, data, nil
	}
	status, body, err := c.PostFormStealth(ctx, url, payload, stealthRequested)
	if err == nil && status == 200 {
		c.setCached(url, payload, body, FlightCacheTTL)
	}
	return status, body, err
}

// BatchExecute posts an encoded batchexecute payload to the Hotels/Travel endpoint
// and returns the raw response body.
//
// Results are cached for 10 minutes. Use SetNoCache(true) to bypass.
//
// Coverage exclusion: thin wrapper around doWithRetry with endpoint-specific URL.
// doWithRetry is thoroughly tested (client_test.go, client_extra_test.go).
// Covered by integration proof tests.
func (c *Client) BatchExecute(ctx context.Context, encodedPayload string) (int, []byte, error) {
	payload := "f.req=" + encodedPayload
	if data, ok := c.getCached(HotelsURL, payload); ok {
		return 200, data, nil
	}
	status, body, err := c.PostForm(ctx, HotelsURL, payload)
	if err == nil && status == 200 {
		c.setCached(HotelsURL, payload, body, HotelCacheTTL)
	}
	return status, body, err
}

// PostExplore posts an encoded payload to the GetExploreDestinations endpoint.
//
// Results are cached for 1 hour. Use SetNoCache(true) to bypass.
//
// Coverage exclusion: thin wrapper around PostForm with endpoint-specific URL.
// PostForm and doWithRetry are thoroughly tested. Covered by integration proof tests.
func (c *Client) PostExplore(ctx context.Context, encodedPayload string) (int, []byte, error) {
	payload := "f.req=" + encodedPayload
	if data, ok := c.getCached(ExploreURL, payload); ok {
		return 200, data, nil
	}
	status, body, err := c.PostForm(ctx, ExploreURL, payload)
	if err == nil && status == 200 {
		c.setCached(ExploreURL, payload, body, DestinationCacheTTL)
	}
	return status, body, err
}

// PostCalendarGraph posts an encoded payload to the GetCalendarGraph endpoint.
//
// Results are cached for 5 minutes. Use SetNoCache(true) to bypass.
//
// Coverage exclusion: thin wrapper around PostForm with endpoint-specific URL.
// PostForm and doWithRetry are thoroughly tested. Covered by integration proof tests.
func (c *Client) PostCalendarGraph(ctx context.Context, encodedPayload string) (int, []byte, error) {
	payload := "f.req=" + encodedPayload
	if data, ok := c.getCached(CalendarGraphURL, payload); ok {
		return 200, data, nil
	}
	status, body, err := c.PostForm(ctx, CalendarGraphURL, payload)
	if err == nil && status == 200 {
		c.setCached(CalendarGraphURL, payload, body, FlightCacheTTL)
	}
	return status, body, err
}

// PostCalendarGrid posts an encoded payload to the GetCalendarGrid endpoint.
//
// Results are cached for 5 minutes. Use SetNoCache(true) to bypass.
//
// Coverage exclusion: thin wrapper around PostForm with endpoint-specific URL.
// PostForm and doWithRetry are thoroughly tested. Covered by integration proof tests.
func (c *Client) PostCalendarGrid(ctx context.Context, encodedPayload string) (int, []byte, error) {
	payload := "f.req=" + encodedPayload
	if data, ok := c.getCached(CalendarGridURL, payload); ok {
		return 200, data, nil
	}
	status, body, err := c.PostForm(ctx, CalendarGridURL, payload)
	if err == nil && status == 200 {
		c.setCached(CalendarGridURL, payload, body, FlightCacheTTL)
	}
	return status, body, err
}

// ChromeHTTPClient returns an *http.Client with Chrome TLS fingerprint impersonation
// and HTTP/2 support via golang.org/x/net/http2.
//
// Datadome and similar bot-detection systems fingerprint the TLS ClientHello
// (JA3/JA4) and also verify that the client negotiates HTTP/2 — real Chrome
// always advertises "h2" first in ALPN and uses HTTP/2. Forcing HTTP/1.1 ALPN
// is itself a detectable signal.
//
// This client uses HelloChrome_Auto (currently Chrome 133) with its native ALPN
// ["h2", "http/1.1"] and then uses http2.Transport to actually speak HTTP/2 when
// the server negotiates it, with an http1.1 fallback via net/http Transport.
//
// Coverage exclusion: creates raw TLS connections with Chrome fingerprint.
// Not unit-testable: requires real TCP + TLS handshake. Covered by integration tests.
func ChromeHTTPClient() *http.Client {
	dialTLS := dialTLSChromeH2

	h2Transport := &http2.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			return dialTLS(ctx, network, addr)
		},
	}

	h1Transport := &http.Transport{
		DialTLSContext:      dialTLS,
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
		ForceAttemptHTTP2:   false,
	}

	return &http.Client{
		Transport: &chromeRoundTripper{h2: h2Transport, h1: h1Transport},
		Timeout:   30 * time.Second,
	}
}

// chromeRoundTripper dispatches requests to the HTTP/2 or HTTP/1.1 transport
// depending on which protocol was negotiated during the TLS handshake.
// http2.Transport.RoundTrip handles the ALPN check internally and returns
// ErrSkipAltSvc / connection errors when h2 is not negotiated; we fall back
// to the plain http1 transport in that case.
type chromeRoundTripper struct {
	h2 *http2.Transport
	h1 *http.Transport
}

func (t *chromeRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Try h2 first. If the server did not negotiate h2 the transport returns
	// an error and we fall through to h1.
	resp, err := t.h2.RoundTrip(req)
	if err == nil {
		return resp, nil
	}
	slog.Debug("chrome h2 failed, falling back to h1", "err", logredact.Err(err))
	return t.h1.RoundTrip(req)
}

// dialTLSChromeH2 dials and performs a Chrome 146 TLS handshake advertising
// both "h2" and "http/1.1" in ALPN — matching what real Chrome 146 sends.
// It uses Chrome146Spec() verbatim (ALPN already includes "h2" first).
//
// Datadome and similar bot-detection systems fingerprint the TLS ClientHello
// (JA3/JA4). Chrome 146 uses X25519MLKEM768 for Post-Quantum key exchange and
// sends a GREASE ECH extension; HelloChrome_Auto resolves to Chrome 133 which
// produces a different fingerprint and triggers 403s from Datadome-protected sites.
//
// Coverage exclusion: creates a raw TLS connection with Chrome fingerprint.
// Not unit-testable: requires real TCP + TLS handshake. Covered by integration tests.
func dialTLSChromeH2(ctx context.Context, network, addr string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("split host: %w", err)
	}

	dialer := &net.Dialer{Timeout: 10 * time.Second}
	rawConn, err := dialer.DialContext(ctx, network, addr)
	if err != nil {
		return nil, fmt.Errorf("dial tcp: %w", err)
	}

	spec := Chrome146Spec()
	uConn := utls.UClient(rawConn, &utls.Config{ServerName: host}, utls.HelloCustom)
	if err := uConn.ApplyPreset(&spec); err != nil {
		_ = uConn.Close()
		return nil, fmt.Errorf("apply chrome146 preset: %w", err)
	}

	if err := uConn.HandshakeContext(ctx); err != nil {
		_ = uConn.Close()
		return nil, fmt.Errorf("utls handshake: %w", err)
	}

	slog.Debug("chrome tls handshake", "proto", uConn.ConnectionState().NegotiatedProtocol)
	return uConn, nil
}

// ErrBlocked is returned when Google responds with 403 Forbidden.
var ErrBlocked = errors.New("request blocked (403)")

// transportForStealth selects the *http.Client to use for a request to url when
// the caller has requested stealth.
//
// It enforces the operator-authorized scope fence: stealth activates ONLY when
//  1. stealthRequested is true (the operator passed --stealth), AND
//  2. the request host is on the operator-authorized allowlist
//     (stealth.Authorized, backed by TRVL_STEALTH_ALLOWLIST).
//
// When stealth is requested for a non-allowlisted host, it logs exactly one
// clear line and returns the normal transport (stealth no-op, never an error or
// abort). When stealth is not requested at all, it returns the normal transport
// silently. The fail-safe default — empty/unset allowlist — means stealth never
// activates. Returns the *http.Client to use and whether stealth was engaged.
func (c *Client) transportForStealth(stealthRequested bool, url string) (*http.Client, bool) {
	if !stealthRequested {
		return c.http, false
	}
	host := hostFromURL(url)
	if !stealth.Authorized(host) {
		slog.Info("stealth not authorized for host " + host)
		return c.http, false
	}
	return c.stealthClient(), true
}

// stealthClient lazily builds and caches the operator-authorized stealth
// transport (Chrome 146 HTTP/2 fingerprint). It is only ever reached after the
// allowlist gate has authorized the host, so building it is itself gated.
func (c *Client) stealthClient() *http.Client {
	c.stealthOnce.Do(func() {
		// A client told to reuse its transport keeps it, which is how the
		// stealth path stays deterministic and offline under test. Production
		// clients build the real Chrome HTTP/2 fingerprint client.
		if c.reuseTransportForStealth {
			c.stealthHTTP = c.http
			return
		}
		c.stealthHTTP = ChromeHTTPClient()
	})
	return c.stealthHTTP
}

// hostFromURL extracts the lowercase host (no port) from a request URL. It
// returns an empty string when the URL cannot be parsed, which the allowlist
// treats as unauthorized (fail-safe).
func hostFromURL(rawURL string) string {
	u, err := neturl.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}
