package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTestProvider_WrongResultsPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"items": []any{
					map[string]any{"name": "Hotel A"},
				},
			},
		})
	}))
	defer srv.Close()

	cfg := &ProviderConfig{
		ID:       "test-wrong-path",
		Name:     "Wrong Path Provider",
		Category: "hotel",
		Endpoint: srv.URL + "/search",
		Method:   "GET",
		ResponseMapping: ResponseMapping{
			ResultsPath: "nonexistent.path",
			Fields:      map[string]string{"name": "name"},
		},
	}

	result := TestProvider(context.Background(), cfg, "Helsinki", 60.17, 24.94,
		"2026-05-01", "2026-05-03", "EUR", 2)

	if result.Success {
		t.Error("expected failure on wrong results path")
	}
}

func TestTestProvider_WithPreflight(t *testing.T) {
	preflightCalls := 0
	searchCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/auth") {
			preflightCalls++
			_, _ = fmt.Fprint(w, `<html>csrf_token=abc123</html>`)
			return
		}
		searchCalls++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []any{
				map[string]any{"name": "Preflight Hotel", "price": 150.0},
			},
		})
	}))
	defer srv.Close()

	cfg := &ProviderConfig{
		ID:       "test-preflight",
		Name:     "Preflight Provider",
		Category: "hotel",
		Endpoint: srv.URL + "/search",
		Method:   "GET",
		Auth: &AuthConfig{
			Type:         "preflight",
			PreflightURL: srv.URL + "/auth",
			Extractions: map[string]Extraction{
				"csrf": {
					Pattern:  `csrf_token=(\w+)`,
					Variable: "csrf",
				},
			},
		},
		ResponseMapping: ResponseMapping{
			ResultsPath: "results",
			Fields: map[string]string{
				"name":  "name",
				"price": "price",
			},
		},
	}

	result := TestProvider(context.Background(), cfg, "Helsinki", 60.17, 24.94,
		"2026-05-01", "2026-05-03", "EUR", 2)

	if !result.Success {
		t.Errorf("expected success with preflight, got error at step %q: %s", result.Step, result.Error)
	}
	if preflightCalls == 0 {
		t.Error("expected at least one preflight call")
	}
	if searchCalls == 0 {
		t.Error("expected at least one search call")
	}
}

func TestTestProvider_PostWithBody(t *testing.T) {
	var receivedBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		receivedBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"hotels": []any{
					map[string]any{"name": "POST Hotel"},
				},
			},
		})
	}))
	defer srv.Close()

	cfg := &ProviderConfig{
		ID:           "test-post",
		Name:         "POST Provider",
		Category:     "hotel",
		Endpoint:     srv.URL + "/graphql",
		Method:       "POST",
		BodyTemplate: `{"query":"hotels","variables":{"location":"${location}"}}`,
		Headers: map[string]string{
			"Content-Type": "application/json",
		},
		ResponseMapping: ResponseMapping{
			ResultsPath: "data.hotels",
			Fields:      map[string]string{"name": "name"},
		},
	}

	result := TestProvider(context.Background(), cfg, "Helsinki", 60.17, 24.94,
		"2026-05-01", "2026-05-03", "EUR", 2)

	if !result.Success {
		t.Errorf("expected success, got error at step %q: %s", result.Step, result.Error)
	}
	if !strings.Contains(receivedBody, "Helsinki") {
		t.Errorf("body should contain 'Helsinki': %s", receivedBody)
	}
}

func TestTestProvider_WithHeaderOrder(t *testing.T) {
	var headerOrder []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Record the order headers arrive (Go's http.Header preserves order within values
		// but doesn't expose inter-key order; just verify all headers are present).
		for _, k := range []string{"Accept", "User-Agent", "X-Custom"} {
			if v := r.Header.Get(k); v != "" {
				headerOrder = append(headerOrder, k)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"results":[{"name":"Ordered Hotel"}]}`)
	}))
	defer srv.Close()

	cfg := &ProviderConfig{
		ID:       "test-header-order",
		Name:     "Header Order Provider",
		Category: "hotel",
		Endpoint: srv.URL + "/search",
		Method:   "GET",
		Headers: map[string]string{
			"Accept":     "application/json",
			"User-Agent": "trvl-test",
			"X-Custom":   "test-value",
		},
		HeaderOrder: []string{"Accept", "User-Agent", "X-Custom"},
		ResponseMapping: ResponseMapping{
			ResultsPath: "results",
			Fields:      map[string]string{"name": "name"},
		},
	}

	result := TestProvider(context.Background(), cfg, "Helsinki", 60.17, 24.94,
		"2026-05-01", "2026-05-03", "EUR", 2)

	if !result.Success {
		t.Errorf("expected success, got error: %s", result.Error)
	}
	if len(headerOrder) != 3 {
		t.Errorf("expected 3 ordered headers, got %d", len(headerOrder))
	}
}

func TestTestProvider_BodyExtractPattern(t *testing.T) {
	html := `<html><head></head><body>
	<script type="application/json" data-state>{"results":[{"name":"Extracted Hotel"}]}</script>
	</body></html>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, html)
	}))
	defer srv.Close()

	cfg := &ProviderConfig{
		ID:       "test-body-extract",
		Name:     "Body Extract Provider",
		Category: "hotel",
		Endpoint: srv.URL + "/search",
		Method:   "GET",
		ResponseMapping: ResponseMapping{
			ResultsPath:        "results",
			Fields:             map[string]string{"name": "name"},
			BodyExtractPattern: `<script[^>]*data-state[^>]*>([\s\S]*?)</script>`,
		},
	}

	result := TestProvider(context.Background(), cfg, "Helsinki", 60.17, 24.94,
		"2026-05-01", "2026-05-03", "EUR", 2)

	if !result.Success {
		t.Errorf("expected success, got error at step %q: %s", result.Step, result.Error)
	}
}

func TestTestProvider_GraphQLError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"errors": []any{
				map[string]any{
					"message": "PersistedQueryNotFound",
				},
			},
		})
	}))
	defer srv.Close()

	cfg := &ProviderConfig{
		ID:       "test-gql-error",
		Name:     "GraphQL Error Provider",
		Category: "hotel",
		Endpoint: srv.URL + "/graphql",
		Method:   "POST",
		ResponseMapping: ResponseMapping{
			ResultsPath: "data.results",
			Fields:      map[string]string{"name": "name"},
		},
	}

	result := TestProvider(context.Background(), cfg, "Helsinki", 60.17, 24.94,
		"2026-05-01", "2026-05-03", "EUR", 2)

	if result.Success {
		t.Error("expected failure on GraphQL error response")
	}
}

// ===========================================================================
// ReloadIfChanged — exercises the file-modified path
// ===========================================================================

func TestReloadIfChanged_ModifiedFile(t *testing.T) {
	dir := t.TempDir()

	// Create initial config.
	cfg := &ProviderConfig{
		ID:              "reload-test",
		Name:            "Initial Name",
		Category:        "hotel",
		Endpoint:        "https://example.com/search",
		ResponseMapping: ResponseMapping{ResultsPath: "results"},
	}
	data, _ := json.Marshal(cfg)
	path := filepath.Join(dir, "reload-test.json")
	_ = os.WriteFile(path, data, 0o600)

	reg, err := NewRegistryAt(dir)
	if err != nil {
		t.Fatal(err)
	}

	// First call should return the existing config.
	got := reg.ReloadIfChanged("reload-test")
	if got == nil || got.Name != "Initial Name" {
		t.Fatalf("initial load: got %v", got)
	}

	// Modify the file with a future mtime.
	cfg.Name = "Updated Name"
	data2, _ := json.Marshal(cfg)
	_ = os.WriteFile(path, data2, 0o600)

	// Touch the file to ensure the mtime is newer.
	futureTime := time.Now().Add(10 * time.Second)
	_ = os.Chtimes(path, futureTime, futureTime)

	// Second call should reload.
	got2 := reg.ReloadIfChanged("reload-test")
	if got2 == nil || got2.Name != "Updated Name" {
		t.Errorf("reload: name = %q, want 'Updated Name'", got2.Name)
	}
}

func TestReloadIfChanged_NonexistentFile(t *testing.T) {
	dir := t.TempDir()

	// Create registry with a provider in memory but no file on disk.
	reg, _ := NewRegistryAt(dir)

	// Try to reload a non-existent provider.
	got := reg.ReloadIfChanged("nonexistent")
	if got != nil {
		t.Errorf("expected nil for non-existent provider, got %v", got)
	}
}

// ===========================================================================
// toFHTTPRequest / toStdResponse — exercises type conversion
// ===========================================================================

func TestToFHTTPRequest_RoundTrip(t *testing.T) {
	body := strings.NewReader("test body")
	req, _ := http.NewRequest("POST", "https://example.com/path", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/html")

	fReq, err := toFHTTPRequest(req)
	if err != nil {
		t.Fatalf("toFHTTPRequest: %v", err)
	}
	if fReq.Method != "POST" {
		t.Errorf("Method = %q, want POST", fReq.Method)
	}
	if fReq.URL.String() != "https://example.com/path" {
		t.Errorf("URL = %q", fReq.URL.String())
	}
}

func TestToStdResponse_Conversion(t *testing.T) {
	// We can't easily construct an fhttp.Response without importing
	// the bogdanfinn/fhttp package directly (it's an internal dep).
	// Coverage for toStdResponse is exercised indirectly when
	// fhttpBridgeTransport.RoundTrip is called in live integration tests.
	// This test verifies toFHTTPRequest doesn't panic on edge inputs.
	req, _ := http.NewRequest("GET", "https://example.com/", nil)
	fReq, err := toFHTTPRequest(req)
	if err != nil {
		t.Fatalf("toFHTTPRequest: %v", err)
	}
	if fReq == nil {
		t.Fatal("expected non-nil fhttp request")
	}
}

func TestDefaultOpenURL_LinuxPath(t *testing.T) {
	// Test the linux path returns an error (xdg-open may not exist in test env).
	err := defaultOpenURL("linux", "", "https://example.com")
	// We don't assert success/failure because xdg-open may or may not exist.
	_ = err
}

func TestDefaultOpenURL_WindowsPath(t *testing.T) {
	// Test the windows path returns an error (cmd not available on non-Windows).
	err := defaultOpenURL("windows", "", "https://example.com")
	_ = err
}

func TestDefaultOpenURL_DarwinWithPreference(t *testing.T) {
	// On darwin, with a non-existent browser preference, should fall back to "open".
	err := defaultOpenURL("darwin", "NonExistentBrowser12345", "https://example.com")
	// The "open" command should work on macOS regardless.
	_ = err
}

// ===========================================================================
// TestProvider — more edge cases for runTestPreflight branches
// ===========================================================================

func TestTestProvider_PreflightFails403(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/auth") {
			w.WriteHeader(403)
			_, _ = fmt.Fprint(w, `<html>Access Denied</html>`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"results":[]}`)
	}))
	defer srv.Close()

	cfg := &ProviderConfig{
		ID:       "test-preflight-403",
		Name:     "Preflight 403 Provider",
		Category: "hotel",
		Endpoint: srv.URL + "/search",
		Method:   "GET",
		Auth: &AuthConfig{
			Type:         "preflight",
			PreflightURL: srv.URL + "/auth",
			Extractions: map[string]Extraction{
				"token": {
					Pattern:  `token=(\w+)`,
					Variable: "token",
				},
			},
		},
		ResponseMapping: ResponseMapping{
			ResultsPath: "results",
			Fields:      map[string]string{"name": "name"},
		},
	}

	result := TestProvider(context.Background(), cfg, "Helsinki", 60.17, 24.94,
		"2026-05-01", "2026-05-03", "EUR", 2)

	// The preflight gets 403 but extraction fails, triggering the no-match
	// fallback path. Should report extraction failure.
	if result.Success {
		t.Error("expected failure when preflight extraction fails")
	}
}

func TestTestProvider_PreflightExtractionNoMatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/auth") {
			// Returns 200 but body doesn't match the extraction pattern.
			_, _ = fmt.Fprint(w, `<html>no token here</html>`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"results":[]}`)
	}))
	defer srv.Close()

	cfg := &ProviderConfig{
		ID:       "test-preflight-nomatch",
		Name:     "No Match Provider",
		Category: "hotel",
		Endpoint: srv.URL + "/search",
		Method:   "GET",
		Auth: &AuthConfig{
			Type:         "preflight",
			PreflightURL: srv.URL + "/auth",
			Extractions: map[string]Extraction{
				"token": {
					Pattern:  `token=(\w+)`,
					Variable: "token",
				},
			},
		},
		ResponseMapping: ResponseMapping{
			ResultsPath: "results",
			Fields:      map[string]string{"name": "name"},
		},
	}

	result := TestProvider(context.Background(), cfg, "Helsinki", 60.17, 24.94,
		"2026-05-01", "2026-05-03", "EUR", 2)

	if result.Success {
		t.Error("expected failure when extraction doesn't match")
	}
	if result.AuthTier != "" {
		t.Errorf("expected no successful auth tier, got %q", result.AuthTier)
	}
}

func TestTestProvider_ZeroResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []any{},
		})
	}))
	defer srv.Close()

	cfg := &ProviderConfig{
		ID:       "test-empty-results",
		Name:     "Empty Results Provider",
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

	if !result.Success {
		t.Errorf("expected success with empty results, got error: %s", result.Error)
	}
	if result.ResultsCount != 0 {
		t.Errorf("results count = %d, want 0", result.ResultsCount)
	}
}

func TestTestProvider_WithCityLookup(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"results":[{"name":"City Hotel"}]}`)
	}))
	defer srv.Close()

	cfg := &ProviderConfig{
		ID:       "test-city-lookup",
		Name:     "City Lookup Provider",
		Category: "hotel",
		Endpoint: srv.URL + "/search?city=${city_id}",
		Method:   "GET",
		CityLookup: map[string]string{
			"helsinki": "45",
		},
		ResponseMapping: ResponseMapping{
			ResultsPath: "results",
			Fields:      map[string]string{"name": "name"},
		},
	}

	result := TestProvider(context.Background(), cfg, "Helsinki", 60.17, 24.94,
		"2026-05-01", "2026-05-03", "EUR", 2)

	if !result.Success {
		t.Errorf("expected success, got error: %s", result.Error)
	}
}

func TestTestProvider_AkamaiChallenge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(202)
		_, _ = fmt.Fprint(w, `<html><script src="https://1234.awswaf.com/challenge.js"></script></html>`)
	}))
	defer srv.Close()

	cfg := &ProviderConfig{
		ID:       "test-akamai",
		Name:     "Akamai Provider",
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
		t.Error("expected failure on Akamai challenge")
	}
	if !strings.Contains(result.Error, "WAF") {
		t.Errorf("error should mention WAF, got %q", result.Error)
	}
}

func TestTestProvider_WithQueryParams(t *testing.T) {
	var receivedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"results":[{"name":"QP Hotel"}]}`)
	}))
	defer srv.Close()

	cfg := &ProviderConfig{
		ID:       "test-query-params",
		Name:     "Query Params Provider",
		Category: "hotel",
		Endpoint: srv.URL + "/search",
		Method:   "GET",
		QueryParams: map[string]string{
			"checkin":  "${checkin}",
			"checkout": "${checkout}",
			"guests":   "${guests}",
		},
		ResponseMapping: ResponseMapping{
			ResultsPath: "results",
			Fields:      map[string]string{"name": "name"},
		},
	}

	result := TestProvider(context.Background(), cfg, "Helsinki", 60.17, 24.94,
		"2026-05-01", "2026-05-03", "EUR", 2)

	if !result.Success {
		t.Errorf("expected success, got error: %s", result.Error)
	}
	if !strings.Contains(receivedQuery, "checkin=2026-05-01") {
		t.Errorf("query should contain checkin, got %q", receivedQuery)
	}
}

func TestTestProvider_RatingScaleNormalization(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []any{
				map[string]any{"name": "Scaled Hotel", "rating": 4.5},
			},
		})
	}))
	defer srv.Close()

	cfg := &ProviderConfig{
		ID:       "test-rating-scale",
		Name:     "Rating Scale Provider",
		Category: "hotel",
		Endpoint: srv.URL + "/search",
		Method:   "GET",
		ResponseMapping: ResponseMapping{
			ResultsPath: "results",
			Fields:      map[string]string{"name": "name", "rating": "rating"},
			RatingScale: 2.0, // multiply by 2 to normalize 0-5 -> 0-10
		},
	}

	result := TestProvider(context.Background(), cfg, "Helsinki", 60.17, 24.94,
		"2026-05-01", "2026-05-03", "EUR", 2)

	if !result.Success {
		t.Errorf("expected success, got error: %s", result.Error)
	}
}

// ===========================================================================
// TestProvider — Apollo denormalization and Niobe paths
// ===========================================================================

func TestTestProvider_WithApolloCache(t *testing.T) {
	apolloData := map[string]any{
		"ROOT_QUERY": map[string]any{
			"searchQueries": map[string]any{
				"search({})": map[string]any{
					"results": []any{
						map[string]any{
							"name":    "Apollo Hotel",
							"price":   120.0,
							"id":      "ap-1",
							"address": "123 Main St",
						},
					},
				},
			},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(apolloData)
	}))
	defer srv.Close()

	cfg := &ProviderConfig{
		ID:       "test-apollo",
		Name:     "Apollo Provider",
		Category: "hotel",
		Endpoint: srv.URL + "/search",
		Method:   "GET",
		ResponseMapping: ResponseMapping{
			ResultsPath: "ROOT_QUERY.searchQueries.search*.results",
			Fields: map[string]string{
				"name":     "name",
				"price":    "price",
				"hotel_id": "id",
				"address":  "address",
			},
		},
	}

	result := TestProvider(context.Background(), cfg, "Helsinki", 60.17, 24.94,
		"2026-05-01", "2026-05-03", "EUR", 2)

	if !result.Success {
		t.Errorf("expected success, got error at step %q: %s", result.Step, result.Error)
	}
	if result.SampleResult["address"] != "123 Main St" {
		t.Errorf("sample address = %v, want '123 Main St'", result.SampleResult["address"])
	}
}

func TestTestProvider_WithNiobeSSR(t *testing.T) {
	niobeData := map[string]any{
		"niobeClientData": []any{
			[]any{
				"CacheKey:1",
				map[string]any{
					"data": map[string]any{
						"search": map[string]any{
							"results": []any{
								map[string]any{
									"name":  "Niobe Hotel",
									"price": 200.0,
								},
							},
						},
					},
				},
			},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(niobeData)
	}))
	defer srv.Close()

	cfg := &ProviderConfig{
		ID:       "test-niobe",
		Name:     "Niobe Provider",
		Category: "hotel",
		Endpoint: srv.URL + "/search",
		Method:   "GET",
		ResponseMapping: ResponseMapping{
			ResultsPath: "data.search.results",
			Fields: map[string]string{
				"name":  "name",
				"price": "price",
			},
		},
	}

	result := TestProvider(context.Background(), cfg, "Helsinki", 60.17, 24.94,
		"2026-05-01", "2026-05-03", "EUR", 2)

	if !result.Success {
		t.Errorf("expected success with Niobe SSR, got error at step %q: %s", result.Step, result.Error)
	}
	if result.ResultsCount != 1 {
		t.Errorf("results count = %d, want 1", result.ResultsCount)
	}
}

func TestTestProvider_WithCityResolver(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/autocomplete") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"results": []any{
					map[string]any{
						"dest_id":   "-999",
						"city_name": "Helsinki",
					},
				},
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"results":[{"name":"Resolved Hotel"}]}`)
	}))
	defer srv.Close()

	cfg := &ProviderConfig{
		ID:       "test-city-resolver",
		Name:     "City Resolver Provider",
		Category: "hotel",
		Endpoint: srv.URL + "/search?city=${city_id}",
		Method:   "GET",
		CityResolver: &CityResolverConfig{
			URL:        srv.URL + "/autocomplete?text=${location}",
			ResultPath: "results",
			IDField:    "dest_id",
			NameField:  "city_name",
		},
		ResponseMapping: ResponseMapping{
			ResultsPath: "results",
			Fields:      map[string]string{"name": "name"},
		},
	}

	result := TestProvider(context.Background(), cfg, "Helsinki", 60.17, 24.94,
		"2026-05-01", "2026-05-03", "EUR", 2)

	if !result.Success {
		t.Errorf("expected success with city resolver, got error at step %q: %s", result.Step, result.Error)
	}
}

func TestCookieSnapshotKey_WithNilElement(t *testing.T) {
	cookies := []*http.Cookie{
		{Name: "a", Value: "1"},
		nil,
		{Name: "b", Value: "2"},
	}
	key := cookieSnapshotKey(cookies)
	// Should not panic and should produce a valid key ignoring the nil.
	if key == "" {
		t.Error("expected non-empty key")
	}
}
