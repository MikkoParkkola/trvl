package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSearchHotelsProviderError(t *testing.T) {
	// Mock server returning 500.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprint(w, "internal error")
	}))
	defer srv.Close()

	dir := t.TempDir()
	reg, err := NewRegistryAt(dir)
	if err != nil {
		t.Fatalf("NewRegistryAt: %v", err)
	}

	cfg := &ProviderConfig{
		ID:       "error-test",
		Name:     "Error Test",
		Category: "hotels",
		Endpoint: srv.URL + "/search",
		Method:   "GET",
		ResponseMapping: ResponseMapping{
			ResultsPath: "results",
		},
		RateLimit: RateLimitConfig{
			RequestsPerSecond: 100,
			Burst:             10,
		},
	}
	if err := reg.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	rt := NewRuntime(reg)
	_, _, err = rt.SearchHotels(context.Background(), "Test", 0, 0, "2025-06-01", "2025-06-05", "USD", 2, nil)
	if err == nil {
		t.Fatal("expected error from provider returning 500")
	}

	// Verify error was marked.
	got := reg.Get("error-test")
	if got.ErrorCount != 1 {
		t.Errorf("ErrorCount = %d, want 1", got.ErrorCount)
	}
}

func TestSearchHotelsCircuitBreaksNeverSuccessfulProvider(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		t.Error("circuit-broken provider should not be called")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	dir := t.TempDir()
	reg, err := NewRegistryAt(dir)
	if err != nil {
		t.Fatalf("NewRegistryAt: %v", err)
	}

	cfg := &ProviderConfig{
		ID:          "never-success",
		Name:        "Never Success",
		Category:    "hotels",
		Endpoint:    srv.URL + "/search",
		Method:      "GET",
		ErrorCount:  circuitBreakerThreshold,
		LastErrorAt: time.Now(), // ensure cooldown is active so half-open probe does not fire
		ResponseMapping: ResponseMapping{
			ResultsPath: "results",
		},
		RateLimit: RateLimitConfig{
			RequestsPerSecond: 100,
			Burst:             10,
		},
	}
	if err := reg.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	rt := NewRuntime(reg)
	hotels, statuses, err := rt.SearchHotels(context.Background(), "Test", 0, 0, "2025-06-01", "2025-06-05", "USD", 2, nil)
	if err != nil {
		t.Fatalf("SearchHotels: %v", err)
	}
	if len(hotels) != 0 {
		t.Fatalf("hotels = %d, want 0", len(hotels))
	}
	// New contract: tripped providers MUST appear in statuses so callers can
	// see which providers were skipped and why. Surfacing the breaker state
	// is exactly what the user asked for ("if there is an error, the error
	// should be passed accordingly").
	if len(statuses) != 1 {
		t.Fatalf("statuses = %d, want 1 (circuit_broken)", len(statuses))
	}
	if statuses[0].ID != "never-success" {
		t.Errorf("statuses[0].ID = %q, want %q", statuses[0].ID, "never-success")
	}
	if statuses[0].Status != "circuit_broken" {
		t.Errorf("statuses[0].Status = %q, want %q", statuses[0].Status, "circuit_broken")
	}
	if statuses[0].FixHint == "" {
		t.Errorf("statuses[0].FixHint = empty, want non-empty fix hint")
	}
	if calls != 0 {
		t.Fatalf("provider calls = %d, want 0", calls)
	}
}

func TestSearchHotelsContextCanceled(t *testing.T) {
	dir := t.TempDir()
	reg, err := NewRegistryAt(dir)
	if err != nil {
		t.Fatalf("NewRegistryAt: %v", err)
	}

	cfg := &ProviderConfig{
		ID:       "ctx-test",
		Name:     "Ctx Test",
		Category: "hotels",
		Endpoint: "https://example.com/search",
		Method:   "GET",
		ResponseMapping: ResponseMapping{
			ResultsPath: "results",
		},
		RateLimit: RateLimitConfig{
			RequestsPerSecond: 0.001, // very slow limiter to force context cancellation
			Burst:             1,
		},
	}
	if err := reg.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	rt := NewRuntime(reg)

	// Exhaust the burst token.
	pc := rt.getOrCreateClient(cfg)
	pc.limiter.Allow()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, _, err = rt.SearchHotels(ctx, "Test", 0, 0, "2025-06-01", "2025-06-05", "USD", 2, nil)
	if err == nil {
		t.Fatal("expected error from canceled context")
	}
}

func TestSearchHotelsPostMethod(t *testing.T) {
	// Mock server that verifies POST body.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("method = %q, want POST", r.Method)
		}

		body := make([]byte, 1024)
		n, _ := r.Body.Read(body)
		bodyStr := string(body[:n])

		if !containsSubstring(bodyStr, `"checkin":"2025-06-01"`) {
			t.Errorf("body does not contain checkin: %s", bodyStr)
		}

		resp := map[string]any{
			"results": []any{
				map[string]any{"name": "POST Hotel"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	dir := t.TempDir()
	reg, err := NewRegistryAt(dir)
	if err != nil {
		t.Fatalf("NewRegistryAt: %v", err)
	}

	cfg := &ProviderConfig{
		ID:           "post-test",
		Name:         "POST Test",
		Category:     "hotels",
		Endpoint:     srv.URL + "/search",
		Method:       "POST",
		BodyTemplate: `{"checkin":"${checkin}","checkout":"${checkout}"}`,
		Headers: map[string]string{
			"Content-Type": "application/json",
		},
		ResponseMapping: ResponseMapping{
			ResultsPath: "results",
			Fields: map[string]string{
				"name": "name",
			},
		},
		RateLimit: RateLimitConfig{
			RequestsPerSecond: 100,
			Burst:             10,
		},
	}
	if err := reg.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	rt := NewRuntime(reg)
	hotels, _, err := rt.SearchHotels(context.Background(), "Test", 0, 0, "2025-06-01", "2025-06-05", "USD", 2, nil)
	if err != nil {
		t.Fatalf("SearchHotels: %v", err)
	}
	if len(hotels) != 1 {
		t.Fatalf("got %d hotels, want 1", len(hotels))
	}
	if hotels[0].Name != "POST Hotel" {
		t.Errorf("name = %q, want 'POST Hotel'", hotels[0].Name)
	}
}

func TestSubstituteEnvVars(t *testing.T) {
	t.Run("basic substitution", func(t *testing.T) {
		t.Setenv("TRVL_TEST_VAR", "hello-world")
		got := substituteEnvVars("key=${env.TRVL_TEST_VAR}")
		if got != "key=hello-world" {
			t.Errorf("got %q, want %q", got, "key=hello-world")
		}
	})

	t.Run("missing env var replaced with empty string", func(t *testing.T) {
		got := substituteEnvVars("key=${env.TRVL_NONEXISTENT_VAR_12345}")
		if got != "key=" {
			t.Errorf("got %q, want %q", got, "key=")
		}
	})

	t.Run("no env vars returns unchanged", func(t *testing.T) {
		input := "plain string without env references"
		got := substituteEnvVars(input)
		if got != input {
			t.Errorf("got %q, want %q", got, input)
		}
	})

	t.Run("multiple env vars in one string", func(t *testing.T) {
		t.Setenv("TRVL_TEST_A", "alpha")
		t.Setenv("TRVL_TEST_B", "beta")
		got := substituteEnvVars("${env.TRVL_TEST_A}-and-${env.TRVL_TEST_B}")
		if got != "alpha-and-beta" {
			t.Errorf("got %q, want %q", got, "alpha-and-beta")
		}
	})

	t.Run("malformed pattern without closing brace", func(t *testing.T) {
		// ${env. without closing } should stop iteration and return what it has.
		input := "prefix${env.FOO_BAR"
		got := substituteEnvVars(input)
		// The function breaks out of the loop when it cannot find closing }.
		if got != input {
			t.Errorf("got %q, want %q", got, input)
		}
	})

	t.Run("empty var name", func(t *testing.T) {
		// ${env.} has an empty variable name -- os.Getenv("") returns "".
		got := substituteEnvVars("val=${env.}")
		if got != "val=" {
			t.Errorf("got %q, want %q", got, "val=")
		}
	})
}

// TestSearchHotels_AkamaiChallenge202 verifies that an HTTP 202 Akamai
// challenge page on the main search endpoint is detected and retried after
// browser cookie recovery. The mock server returns a challenge on the first
// request and real results on the second (simulating cookies being applied).
func TestSearchHotels_AkamaiChallenge202(t *testing.T) {
	var reqCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCount++
		if reqCount == 1 {
			// First request: return an Akamai challenge page.
			w.WriteHeader(http.StatusAccepted)
			_, _ = fmt.Fprint(w, `<html><script src="https://1234.awswaf.com/challenge.js"></script></html>`)
			return
		}
		// Subsequent requests: return real results (browser cookies worked).
		resp := map[string]any{
			"results": []any{
				map[string]any{"name": "Recovered Hotel", "id": "rh1"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	dir := t.TempDir()
	reg, err := NewRegistryAt(dir)
	if err != nil {
		t.Fatalf("NewRegistryAt: %v", err)
	}

	cfg := &ProviderConfig{
		ID:       "akamai-test",
		Name:     "Akamai Test",
		Category: "hotels",
		Endpoint: srv.URL + "/search",
		Method:   "GET",
		Cookies: CookieConfig{
			Source: "browser",
		},
		ResponseMapping: ResponseMapping{
			ResultsPath: "results",
			Fields: map[string]string{
				"name":     "name",
				"hotel_id": "id",
			},
		},
		RateLimit: RateLimitConfig{
			RequestsPerSecond: 100,
			Burst:             10,
		},
	}
	if err := reg.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	rt := NewRuntime(reg)
	// The test cannot install real browser cookies, but the mock server
	// returns real results on the second request regardless. The key assertion
	// is that searchProvider detects the 202 challenge and retries rather than
	// trying to parse the HTML as JSON and failing with a confusing error.
	hotels, statuses, err := rt.SearchHotels(context.Background(), "Test", 0, 0, "2025-06-01", "2025-06-05", "USD", 2, nil)

	// The outcome depends on whether browserCookiesForURL returns anything
	// in the test environment (it won't for the mock server's localhost).
	// What matters: the error message, if any, should mention "WAF/JS challenge"
	// NOT "parse json" or "body_extract_pattern".
	if err != nil {
		if containsSubstring(err.Error(), "parse json") || containsSubstring(err.Error(), "body_extract_pattern") {
			t.Fatalf("challenge page was not detected — error leaked through as JSON parse: %v", err)
		}
		// The error should reference WAF/challenge, which is correct behaviour
		// when no browser cookies are available to recover.
		if !containsSubstring(err.Error(), "WAF") && !containsSubstring(err.Error(), "challenge") {
			t.Fatalf("unexpected error: %v", err)
		}
		// Verify statuses contain a meaningful fix hint.
		for _, s := range statuses {
			if s.ID == "akamai-test" && s.Status == "error" {
				if !containsSubstring(s.FixHint, "WAF") && !containsSubstring(s.FixHint, "test_provider") {
					t.Errorf("fix hint should mention WAF or test_provider, got %q", s.FixHint)
				}
			}
		}
		return // acceptable: challenge detected, no browser cookies to recover
	}

	// If we got here, the retry succeeded (mock returned results on second call).
	if len(hotels) != 1 {
		t.Fatalf("got %d hotels, want 1", len(hotels))
	}
	if hotels[0].Name != "Recovered Hotel" {
		t.Errorf("name = %q, want 'Recovered Hotel'", hotels[0].Name)
	}
}

// TestSearchHotels_202NonChallenge verifies that a legitimate HTTP 202
// Accepted response (JSON body, not an Akamai challenge) is treated normally
// and not rejected as a WAF challenge.
func TestSearchHotels_202NonChallenge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		resp := map[string]any{
			"results": []any{
				map[string]any{"name": "Async Hotel", "id": "ah1"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	dir := t.TempDir()
	reg, err := NewRegistryAt(dir)
	if err != nil {
		t.Fatalf("NewRegistryAt: %v", err)
	}

	cfg := &ProviderConfig{
		ID:       "async-test",
		Name:     "Async Test",
		Category: "hotels",
		Endpoint: srv.URL + "/search",
		Method:   "GET",
		ResponseMapping: ResponseMapping{
			ResultsPath: "results",
			Fields: map[string]string{
				"name":     "name",
				"hotel_id": "id",
			},
		},
		RateLimit: RateLimitConfig{
			RequestsPerSecond: 100,
			Burst:             10,
		},
	}
	if err := reg.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	rt := NewRuntime(reg)
	hotels, _, err := rt.SearchHotels(context.Background(), "Test", 0, 0, "2025-06-01", "2025-06-05", "USD", 2, nil)
	if err != nil {
		t.Fatalf("SearchHotels: %v (legitimate 202 JSON should not be rejected)", err)
	}
	if len(hotels) != 1 {
		t.Fatalf("got %d hotels, want 1", len(hotels))
	}
	if hotels[0].Name != "Async Hotel" {
		t.Errorf("name = %q, want 'Async Hotel'", hotels[0].Name)
	}
}

func TestRunPreflight_POST(t *testing.T) {
	// Mock OAuth2-style token endpoint.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("preflight method = %q, want POST", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Errorf("Content-Type = %q, want application/x-www-form-urlencoded", r.Header.Get("Content-Type"))
		}
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("Accept = %q, want application/json", r.Header.Get("Accept"))
		}

		// Read and verify body.
		body := make([]byte, 1024)
		n, _ := r.Body.Read(body)
		bodyStr := string(body[:n])
		if bodyStr != "api_key=test-key" {
			t.Errorf("preflight body = %q, want %q", bodyStr, "api_key=test-key")
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"access_token":"test-token-123","expires_in":"3600"}`)
	}))
	defer srv.Close()

	// Mock search server that verifies auth token was extracted and used.
	searchSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader != "Bearer test-token-123" {
			t.Errorf("Authorization = %q, want 'Bearer test-token-123'", authHeader)
		}
		resp := map[string]any{
			"results": []any{
				map[string]any{"name": "Token Hotel", "id": "th1"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer searchSrv.Close()

	dir := t.TempDir()
	reg, err := NewRegistryAt(dir)
	if err != nil {
		t.Fatalf("NewRegistryAt: %v", err)
	}

	cfg := &ProviderConfig{
		ID:       "post-preflight-test",
		Name:     "POST Preflight Test",
		Category: "hotels",
		Endpoint: searchSrv.URL + "/search",
		Method:   "GET",
		Headers: map[string]string{
			"Authorization": "Bearer ${auth_token}",
		},
		Auth: &AuthConfig{
			Type:            "preflight",
			PreflightURL:    srv.URL + "/auth",
			PreflightMethod: "POST",
			PreflightBody:   "api_key=test-key",
			PreflightHeaders: map[string]string{
				"Content-Type": "application/x-www-form-urlencoded",
				"Accept":       "application/json",
			},
			Extractions: map[string]Extraction{
				"auth_token": {
					Pattern:  `"access_token":"([^"]+)"`,
					Variable: "auth_token",
				},
			},
		},
		ResponseMapping: ResponseMapping{
			ResultsPath: "results",
			Fields: map[string]string{
				"name":     "name",
				"hotel_id": "id",
			},
		},
		RateLimit: RateLimitConfig{
			RequestsPerSecond: 100,
			Burst:             10,
		},
	}

	if err := reg.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	rt := NewRuntime(reg)
	hotels, _, err := rt.SearchHotels(context.Background(), "Test", 0, 0, "2025-06-01", "2025-06-05", "USD", 2, nil)
	if err != nil {
		t.Fatalf("SearchHotels: %v", err)
	}
	if len(hotels) != 1 {
		t.Fatalf("got %d hotels, want 1", len(hotels))
	}
	if hotels[0].Name != "Token Hotel" {
		t.Errorf("name = %q, want 'Token Hotel'", hotels[0].Name)
	}
}
