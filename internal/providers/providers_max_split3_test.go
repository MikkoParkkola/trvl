package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

func TestAnyToString_Bool(t *testing.T) {
	if anyToString(true) != "true" {
		t.Errorf("anyToString(true) = %q", anyToString(true))
	}
	if anyToString(false) != "false" {
		t.Errorf("anyToString(false) = %q", anyToString(false))
	}
}

func TestAnyToString_Nil(t *testing.T) {
	if anyToString(nil) != "" {
		t.Errorf("anyToString(nil) = %q, want empty", anyToString(nil))
	}
}

func TestAnyToString_FractionalFloat(t *testing.T) {
	got := anyToString(3.14)
	if got != "3.14" {
		t.Errorf("anyToString(3.14) = %q, want '3.14'", got)
	}
}

func TestAnyToString_OtherType(t *testing.T) {
	got := anyToString([]int{1, 2, 3})
	if got != "[1 2 3]" {
		t.Errorf("anyToString([]int{1,2,3}) = %q", got)
	}
}

// ===========================================================================
// openURLInBrowser — defaultChromePreference on darwin
// ===========================================================================

func TestOpenURLInBrowser_DefaultChromeOnDarwin(t *testing.T) {
	var gotPref string
	withOpener(t, func(goos, pref, target string) error {
		gotPref = pref
		return nil
	})

	if err := openURLInBrowser("https://example.com", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// On darwin, empty preference should default to "Google Chrome".
	// On other OS, it stays empty.
	if runtime.GOOS == "darwin" && gotPref != "Google Chrome" {
		t.Errorf("on darwin, expected 'Google Chrome' default, got %q", gotPref)
	}
}

// ===========================================================================
// waitForFreshCookies — zero poll/max values
// ===========================================================================

func TestWaitForFreshCookies_ZeroPollInterval(t *testing.T) {
	prev := []*http.Cookie{{Name: "sid", Value: "v1"}}
	callCount := 0
	withCookieSource(t, func(string) []*http.Cookie {
		callCount++
		if callCount >= 2 {
			return []*http.Cookie{{Name: "sid", Value: "v2"}}
		}
		return []*http.Cookie{{Name: "sid", Value: "v1"}}
	})

	got, changed := waitForFreshCookies(context.Background(), "https://example.com",
		prev, 0, 0) // zero -> defaults to 1s poll, 10s max

	if !changed {
		t.Fatal("expected cookie change to be detected")
	}
	if len(got) != 1 || got[0].Value != "v2" {
		t.Errorf("unexpected cookies: %+v", got)
	}
}

// ===========================================================================
// cookieSnapshotKey — nil cookies
// ===========================================================================

func TestCookieSnapshotKey_NilCookies(t *testing.T) {
	key := cookieSnapshotKey(nil)
	if key != "" {
		t.Errorf("expected empty key for nil cookies, got %q", key)
	}
}

// ===========================================================================
// tryBrowserCookieRetry — exercises via httptest
// ===========================================================================

func TestTryBrowserCookieRetry_NoBrowserCookies(t *testing.T) {
	// When applyBrowserCookies returns false (no cookies found), retry should fail.
	jar, _ := cookiejar.New(nil)
	pc := &providerClient{
		config: &ProviderConfig{
			ID:      "test-retry",
			Cookies: CookieConfig{Browser: ""},
		},
		client:     &http.Client{Jar: jar},
		authValues: make(map[string]string),
	}

	auth := &AuthConfig{
		PreflightURL: "https://no-browser-cookies.example.invalid/page",
	}

	got := tryBrowserCookieRetry(context.Background(), pc, auth)
	if got {
		t.Error("expected false when no browser cookies available")
	}
}

// ===========================================================================
// tryWAFSolve — exercises via httptest
// ===========================================================================

func TestTryWAFSolve_NonChallengeStatus(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	pc := &providerClient{
		config:     &ProviderConfig{ID: "test-waf"},
		client:     &http.Client{Jar: jar},
		authValues: make(map[string]string),
	}

	auth := &AuthConfig{
		PreflightURL: "https://example.com/page",
	}

	// Status 200 — not a WAF challenge.
	got := tryWAFSolve(context.Background(), pc, auth, 200, []byte("normal page"))
	if got {
		t.Error("expected false for non-challenge status code")
	}
}

func TestTryWAFSolve_403NoWAFMarkers(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	pc := &providerClient{
		config:     &ProviderConfig{ID: "test-waf"},
		client:     &http.Client{Jar: jar},
		authValues: make(map[string]string),
	}

	auth := &AuthConfig{
		PreflightURL: "https://example.com/page",
	}

	// Status 403 without WAF markers — SolveAWSWAF should fail.
	got := tryWAFSolve(context.Background(), pc, auth, 403, []byte("<html>Access Denied</html>"))
	if got {
		t.Error("expected false when WAF solver doesn't find a token")
	}
}

// ===========================================================================
// tryBrowserEscapeHatch — exercises edges
// ===========================================================================

func TestTryBrowserEscapeHatch_NotInteractive(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	pc := &providerClient{
		config: &ProviderConfig{
			ID: "test-escape",
			Auth: &AuthConfig{
				BrowserEscapeHatch: true,
			},
		},
		client:     &http.Client{Jar: jar},
		authValues: make(map[string]string),
	}

	auth := &AuthConfig{
		PreflightURL:       "https://example.com/page",
		BrowserEscapeHatch: true,
	}

	// Non-interactive context — should attempt since the function doesn't check
	// isInteractive (that's done by the caller in runPreflight).
	// But withOpener should prevent actual browser launch.
	withOpener(t, func(goos, pref, target string) error {
		return fmt.Errorf("browser launch blocked in test")
	})

	got := tryBrowserEscapeHatch(context.Background(), pc, auth)
	if got {
		t.Error("expected false when browser open fails")
	}
}

func TestTryBrowserEscapeHatch_ElicitDeclined(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	pc := &providerClient{
		config: &ProviderConfig{
			ID:   "test-escape-elicit",
			Name: "TestProvider",
		},
		client:     &http.Client{Jar: jar},
		authValues: make(map[string]string),
	}

	auth := &AuthConfig{
		PreflightURL:       "https://example.com/page",
		BrowserEscapeHatch: true,
	}

	// Add elicit that declines.
	ctx := WithElicit(context.Background(), func(msg string) (bool, error) {
		return false, nil // user declined
	})

	got := tryBrowserEscapeHatch(ctx, pc, auth)
	if got {
		t.Error("expected false when user declines elicitation")
	}
}

func TestTryBrowserEscapeHatch_ElicitError(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	pc := &providerClient{
		config: &ProviderConfig{
			ID:   "test-escape-elicit-err",
			Name: "TestProvider",
		},
		client:     &http.Client{Jar: jar},
		authValues: make(map[string]string),
	}

	auth := &AuthConfig{
		PreflightURL:       "https://example.com/page",
		BrowserEscapeHatch: true,
	}

	// Add elicit that returns an error.
	ctx := WithElicit(context.Background(), func(msg string) (bool, error) {
		return false, fmt.Errorf("elicitation failed")
	})

	got := tryBrowserEscapeHatch(ctx, pc, auth)
	if got {
		t.Error("expected false when elicitation errors")
	}
}

// ===========================================================================
// enrichRatings — no enrichable results
// ===========================================================================

func TestEnrichRatings_AllRated(t *testing.T) {
	hotels := []models.HotelResult{
		{Name: "Already Rated 1", Rating: 8.0, BookingURL: "https://example.com/1"},
		{Name: "Already Rated 2", Rating: 7.5, BookingURL: "https://example.com/2"},
	}

	cfg := &ProviderConfig{ID: "test"}
	enrichRatings(context.Background(), http.DefaultClient, hotels, cfg)

	// Ratings should remain unchanged.
	if hotels[0].Rating != 8.0 || hotels[1].Rating != 7.5 {
		t.Error("already-rated hotels should not be modified")
	}
}

func TestEnrichRatings_EmptySlice(t *testing.T) {
	cfg := &ProviderConfig{ID: "test"}
	// Should not panic.
	enrichRatings(context.Background(), http.DefaultClient, nil, cfg)
}

// ===========================================================================
// runPreflight — exercises cache valid path + extraction paths
// ===========================================================================

func TestRunPreflight_NilAuth(t *testing.T) {
	rt := NewRuntime(nil)
	jar, _ := cookiejar.New(nil)
	pc := &providerClient{
		config:     &ProviderConfig{ID: "test-nil-auth"},
		client:     &http.Client{Jar: jar},
		authValues: make(map[string]string),
	}

	// No auth config -> should return nil immediately.
	_, err := rt.runPreflight(context.Background(), pc, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunPreflight_EmptyPreflightURL(t *testing.T) {
	rt := NewRuntime(nil)
	jar, _ := cookiejar.New(nil)
	pc := &providerClient{
		config: &ProviderConfig{
			ID:   "test-empty-preflight",
			Auth: &AuthConfig{PreflightURL: ""},
		},
		client:     &http.Client{Jar: jar},
		authValues: make(map[string]string),
	}

	_, err := rt.runPreflight(context.Background(), pc, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunPreflight_SuccessfulExtraction(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<html><meta name="csrf" content="tok-999"></html>`)
	}))
	defer srv.Close()

	rt := NewRuntime(nil)
	jar, _ := cookiejar.New(nil)
	pc := &providerClient{
		config: &ProviderConfig{
			ID: "test-preflight-extract",
			Auth: &AuthConfig{
				PreflightURL: srv.URL + "/page",
				Extractions: map[string]Extraction{
					"csrf": {
						Pattern:  `content="(tok-[^"]+)"`,
						Variable: "csrf_token",
					},
				},
			},
		},
		client:     &http.Client{Jar: jar},
		authValues: make(map[string]string),
	}

	_, err := rt.runPreflight(context.Background(), pc, nil)
	if err != nil {
		t.Fatalf("runPreflight: %v", err)
	}
	if pc.authValues["csrf_token"] != "tok-999" {
		t.Errorf("csrf_token = %q, want 'tok-999'", pc.authValues["csrf_token"])
	}
}

func TestRunPreflight_CacheValid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<html>token=first</html>`)
	}))
	defer srv.Close()

	rt := NewRuntime(nil)
	jar, _ := cookiejar.New(nil)
	pc := &providerClient{
		config: &ProviderConfig{
			ID: "test-preflight-cache",
			Auth: &AuthConfig{
				PreflightURL: srv.URL + "/page",
			},
		},
		client:     &http.Client{Jar: jar},
		authValues: make(map[string]string),
	}

	// First call populates cache.
	if _, err := rt.runPreflight(context.Background(), pc, nil); err != nil {
		t.Fatalf("first preflight: %v", err)
	}

	// Second call should hit the cache (same URL, within expiry).
	if _, err := rt.runPreflight(context.Background(), pc, nil); err != nil {
		t.Fatalf("second preflight (cached): %v", err)
	}
}

// ===========================================================================
// doSearchRequest — nil GetBody
// ===========================================================================

func TestDoSearchRequest_NilGetBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"ok":true}`)
	}))
	defer srv.Close()

	orig, _ := http.NewRequestWithContext(context.Background(), "GET", srv.URL+"/search", nil)
	// GetBody is nil for GET requests without a body.
	resp, body, err := doSearchRequest(context.Background(), srv.Client(), orig)
	if err != nil {
		t.Fatalf("doSearchRequest: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(string(body), "ok") {
		t.Errorf("body = %q, want to contain 'ok'", string(body))
	}
}

// ===========================================================================
// applyBrowserCookies — nil client
// ===========================================================================

func TestApplyBrowserCookies_NilClient(t *testing.T) {
	if applyBrowserCookies(nil, "https://example.com", "") {
		t.Error("expected false for nil client")
	}
}

// ===========================================================================
// TestProvider — exercises the full diagnostic flow via httptest
// ===========================================================================

func TestTestProvider_FullFlow(t *testing.T) {
	// Serve a provider that returns valid JSON with results.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []any{
				map[string]any{
					"name":  "Test Hotel",
					"price": 99.0,
					"id":    "hotel-1",
				},
			},
		})
	}))
	defer srv.Close()

	cfg := &ProviderConfig{
		ID:       "test-full",
		Name:     "Test Provider",
		Category: "hotel",
		Endpoint: srv.URL + "/search?location=${location}&checkin=${checkin}&checkout=${checkout}",
		Method:   "GET",
		ResponseMapping: ResponseMapping{
			ResultsPath: "results",
			Fields: map[string]string{
				"name":     "name",
				"price":    "price",
				"hotel_id": "id",
			},
		},
		RateLimit: RateLimitConfig{RequestsPerSecond: 10, Burst: 1},
	}

	result := TestProvider(context.Background(), cfg, "Helsinki", 60.17, 24.94,
		"2026-05-01", "2026-05-03", "EUR", 2)

	if !result.Success {
		t.Errorf("expected success, got error at step %q: %s", result.Step, result.Error)
	}
	if result.ResultsCount != 1 {
		t.Errorf("results count = %d, want 1", result.ResultsCount)
	}
}

func TestTestProvider_HTTP500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = fmt.Fprint(w, "Internal Server Error")
	}))
	defer srv.Close()

	cfg := &ProviderConfig{
		ID:       "test-500",
		Name:     "Failing Provider",
		Category: "hotel",
		Endpoint: srv.URL + "/search",
		Method:   "GET",
		ResponseMapping: ResponseMapping{
			ResultsPath: "results",
			Fields:      map[string]string{"name": "name"},
		},
	}

	result := TestProvider(context.Background(), cfg, "Helsinki", 60.17, 24.94,
		"2026-05-01", "2026-05-03", "EUR", 2)

	if result.Success {
		t.Error("expected failure on HTTP 500")
	}
	if result.HTTPStatus != 500 {
		t.Errorf("status = %d, want 500", result.HTTPStatus)
	}
}

func TestTestProvider_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `not json`)
	}))
	defer srv.Close()

	cfg := &ProviderConfig{
		ID:       "test-bad-json",
		Name:     "Bad JSON Provider",
		Category: "hotel",
		Endpoint: srv.URL + "/search",
		Method:   "GET",
		ResponseMapping: ResponseMapping{
			ResultsPath: "results",
			Fields:      map[string]string{"name": "name"},
		},
	}

	result := TestProvider(context.Background(), cfg, "Helsinki", 60.17, 24.94,
		"2026-05-01", "2026-05-03", "EUR", 2)

	if result.Success {
		t.Error("expected failure on malformed JSON")
	}
	if !strings.Contains(result.Error, "response_parse") {
		t.Errorf("error should mention response_parse, got %q", result.Error)
	}
}
