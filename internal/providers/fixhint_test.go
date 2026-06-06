package providers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClassifyProviderError_AllCodes(t *testing.T) {
	cases := []struct {
		name     string
		errMsg   string
		wantCode FixHintCode
	}{
		// Rate-limit beats WAF
		{"rate_limit_text", "upstream: rate limit exceeded", FixHintRateLimited},
		{"http_429", "server returned http 429 too many requests", FixHintRateLimited},
		{"too_many_requests", "too many requests from this ip", FixHintRateLimited},

		// Akamai / WAF block
		{"akamai_explicit", "akamai edge server rejected request", FixHintAkamaiBlock},
		{"http_403", "http 403 forbidden", FixHintAkamaiBlock},
		{"http_202", "http 202 challenge page", FixHintAkamaiBlock},
		{"access_denied", "access denied by provider waf", FixHintAkamaiBlock},

		// Missing browser cookies (Booking.com kooky auto-detect found none)
		{"browser_cookies_missing", "browser cookies missing for booking: no cookies found", FixHintBrowserCookiesMissing},
		{"no_browser_cookies", "no browser cookies available for the configured browser", FixHintBrowserCookiesMissing},

		// Cookie / auth failure
		{"cookie_expired", "cookie jar expired, re-login required", FixHintCookieExpired},
		{"csrf_mismatch", "csrf token mismatch on post", FixHintCookieExpired},
		{"http_401", "server returned 401 unauthorized", FixHintCookieExpired},

		// Preflight step failure
		{"preflight_fail", "preflight request to /auth failed", FixHintPreflightFailed},

		// Response shape changed
		{"results_path", "results_path '/data/hotels' not found in response", FixHintResponseShapeChanged},
		{"unmarshal", "cannot unmarshal string into Go value of type []Hotel", FixHintResponseShapeChanged},
		{"unexpected_eof", "unexpected end of json input", FixHintResponseShapeChanged},

		// DNS failure
		{"no_such_host", "dial tcp: lookup booking.com: no such host", FixHintDNSFail},
		{"dns_explicit", "dns resolution failed for provider endpoint", FixHintDNSFail},

		// TLS / connection timeout
		{"tls_handshake", "tls handshake timeout after 10s", FixHintTLSTimeout},
		{"deadline_exceeded", "context deadline exceeded", FixHintTLSTimeout},
		{"timeout_generic", "connection timeout to provider", FixHintTLSTimeout},
		{"connection_refused", "connection refused on port 443", FixHintTLSTimeout},

		// Unclassified
		{"unknown", "something completely unrecognised happened", FixHintUnclassified},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, hint := classifyProviderError(errors.New(tc.errMsg))
			if code != tc.wantCode {
				t.Errorf("classifyProviderError(%q) code = %q, want %q", tc.errMsg, code, tc.wantCode)
			}
			if hint == "" {
				t.Errorf("classifyProviderError(%q) returned empty hint", tc.errMsg)
			}
		})
	}
}

// TestClassifyProviderError_NilError verifies graceful handling of nil.
func TestClassifyProviderError_NilError(t *testing.T) {
	code, hint := classifyProviderError(nil)
	if code != FixHintUnclassified {
		t.Errorf("nil error should classify as UNCLASSIFIED, got %q", code)
	}
	if hint == "" {
		t.Error("nil error should still return a non-empty hint")
	}
}

// TestClassifyProviderError_OrderingPriority pins tie-breaker ordering:
// rate-limit > WAF > cookie.
func TestClassifyProviderError_OrderingPriority(t *testing.T) {
	// A 429 that also contains "403" in the body — rate-limit wins.
	code, _ := classifyProviderError(errors.New("http 429, previously saw http 403"))
	if code != FixHintRateLimited {
		t.Errorf("rate-limit should beat WAF, got %q", code)
	}

	// A 403 that also mentions "cookie" — WAF wins (403 is checked before cookie).
	code, _ = classifyProviderError(errors.New("http 403, cookie header was present"))
	if code != FixHintAkamaiBlock {
		t.Errorf("WAF (403) should beat cookie when both match, got %q", code)
	}

	// "browser cookies missing" must beat the generic cookie-expired branch:
	// the message contains "cookie" but the root cause is no session at all.
	code, _ = classifyProviderError(errors.New("browser cookies missing for booking"))
	if code != FixHintBrowserCookiesMissing {
		t.Errorf("missing browser cookies should beat COOKIE_EXPIRED, got %q", code)
	}
}

// TestSearchProvider_BrowserCookiesMissing verifies that a browser-cookie
// provider with the escape hatch enabled (e.g. Booking.com) fails loudly when
// no browser cookies are available, and that the failure is classified as
// BOOKING_COOKIES_MISSING with an actionable fix hint rather than silently
// returning zero results. The test binary never has real browser cookies, so
// applyBrowserCookies returns false deterministically (offline).
func TestSearchProvider_BrowserCookiesMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"hotels":[]}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	reg, err := NewRegistryAt(dir)
	if err != nil {
		t.Fatalf("NewRegistryAt: %v", err)
	}
	cfg := &ProviderConfig{
		ID:       "booking",
		Name:     "Booking.com",
		Category: "hotels",
		Endpoint: srv.URL + "/search",
		Method:   "GET",
		Cookies:  CookieConfig{Source: "browser"},
		Auth: &AuthConfig{
			Type:               "preflight",
			PreflightURL:       srv.URL + "/preflight",
			BrowserEscapeHatch: true,
		},
		ResponseMapping: ResponseMapping{ResultsPath: "hotels"},
		RateLimit:       RateLimitConfig{RequestsPerSecond: 100, Burst: 100},
	}
	if err := reg.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	rt := NewRuntime(reg)
	_, statuses, err := rt.SearchHotels(context.Background(), "Paris", 48.85, 2.35,
		"2026-06-01", "2026-06-03", "EUR", 2, nil)
	if err == nil {
		t.Fatal("expected error when browser cookies are missing, got nil")
	}

	var found bool
	for _, s := range statuses {
		if s.ID != "booking" {
			continue
		}
		found = true
		if s.Status != "error" {
			t.Errorf("status = %q, want error", s.Status)
		}
		if s.FixHintCode != string(FixHintBrowserCookiesMissing) {
			t.Errorf("FixHintCode = %q, want %q", s.FixHintCode, FixHintBrowserCookiesMissing)
		}
		if s.FixHint == "" {
			t.Error("FixHint should be non-empty and actionable")
		}
	}
	if !found {
		t.Fatal("no provider status for booking")
	}
}

// TestProviderFixHint_BackCompat verifies that providerFixHint (the legacy
// wrapper) returns a non-empty string for every error class.
func TestProviderFixHint_BackCompat(t *testing.T) {
	errs := []string{
		"rate limit exceeded",
		"http 403 forbidden",
		"cookie expired",
		"preflight failed",
		"results_path not found",
		"tls handshake timeout",
		"context deadline exceeded",
		"no such host",
		"unrecognised error",
	}
	for _, msg := range errs {
		hint := providerFixHint(errors.New(msg))
		if hint == "" {
			t.Errorf("providerFixHint(%q) returned empty hint", msg)
		}
	}
}
