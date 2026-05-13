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

func TestTestProvider_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"data": map[string]any{
				"results": []any{
					map[string]any{
						"name":    "Paris Grand Hotel",
						"id":      "pg1",
						"rating":  4.5,
						"price":   250.0,
						"curr":    "EUR",
						"addr":    "1 Rue de Rivoli",
						"geo_lat": 48.8566,
						"geo_lon": 2.3522,
					},
					map[string]any{
						"name":    "Budget Paris",
						"id":      "bp1",
						"rating":  3.2,
						"price":   89.0,
						"curr":    "EUR",
						"geo_lat": 48.857,
						"geo_lon": 2.353,
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	cfg := &ProviderConfig{
		ID:       "test-provider-success",
		Name:     "Test Provider Success",
		Category: "hotels",
		Endpoint: srv.URL + "/search",
		Method:   "GET",
		ResponseMapping: ResponseMapping{
			ResultsPath: "data.results",
			Fields: map[string]string{
				"name":     "name",
				"hotel_id": "id",
				"rating":   "rating",
				"price":    "price",
				"currency": "curr",
				"address":  "addr",
				"lat":      "geo_lat",
				"lon":      "geo_lon",
			},
		},
	}

	result := TestProvider(context.Background(), cfg, "Paris", 48.8566, 2.3522, "2026-05-01", "2026-05-02", "EUR", 2)

	if !result.Success {
		t.Fatalf("expected Success=true, got false; step=%s error=%s", result.Step, result.Error)
	}
	if result.Step != "complete" {
		t.Errorf("Step = %q, want 'complete'", result.Step)
	}
	if result.ResultsCount != 2 {
		t.Errorf("ResultsCount = %d, want 2", result.ResultsCount)
	}
	if result.SampleResult == nil {
		t.Fatal("SampleResult is nil, want non-nil")
	}
	if result.SampleResult["name"] != "Paris Grand Hotel" {
		t.Errorf("SampleResult name = %v, want 'Paris Grand Hotel'", result.SampleResult["name"])
	}
}

func TestTestProvider_PreflightFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprint(w, "server error")
	}))
	defer srv.Close()

	cfg := &ProviderConfig{
		ID:       "preflight-fail",
		Name:     "Preflight Fail",
		Category: "hotels",
		Endpoint: srv.URL + "/search",
		Method:   "GET",
		Auth: &AuthConfig{
			Type:         "preflight",
			PreflightURL: srv.URL + "/auth",
			Extractions: map[string]Extraction{
				"token": {
					Pattern:  `"token":"([^"]+)"`,
					Variable: "token",
				},
			},
		},
		ResponseMapping: ResponseMapping{
			ResultsPath: "results",
			Fields:      map[string]string{"name": "name"},
		},
	}

	result := TestProvider(context.Background(), cfg, "Paris", 48.8566, 2.3522, "2026-05-01", "2026-05-02", "EUR", 2)

	if result.Success {
		t.Fatal("expected Success=false for preflight failure")
	}
	// The TestProvider function sets step to "auth_extraction" after reading the body,
	// then checks extractions. Since the 500 body is "server error" and the pattern
	// won't match, the failure should be in the extraction step.
	if result.Step != "auth_extraction" {
		t.Errorf("Step = %q, want 'auth_extraction'", result.Step)
	}
	if result.HTTPStatus != 500 {
		t.Errorf("HTTPStatus = %d, want 500", result.HTTPStatus)
	}
}

func TestTestProvider_BadResponseParse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, "<html><body>Not JSON</body></html>")
	}))
	defer srv.Close()

	cfg := &ProviderConfig{
		ID:       "bad-parse",
		Name:     "Bad Parse",
		Category: "hotels",
		Endpoint: srv.URL + "/search",
		Method:   "GET",
		ResponseMapping: ResponseMapping{
			ResultsPath: "results",
			Fields:      map[string]string{"name": "name"},
		},
	}

	result := TestProvider(context.Background(), cfg, "Paris", 48.8566, 2.3522, "2026-05-01", "2026-05-02", "EUR", 2)

	if result.Success {
		t.Fatal("expected Success=false for bad JSON response")
	}
	if result.Step != "response_parse" {
		t.Errorf("Step = %q, want 'response_parse'", result.Step)
	}
	if result.Error == "" {
		t.Error("Error is empty, want non-empty error message")
	}
}

func TestTestProvider_EmptyResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"data": map[string]any{
				"results": []any{},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	cfg := &ProviderConfig{
		ID:       "empty-results",
		Name:     "Empty Results",
		Category: "hotels",
		Endpoint: srv.URL + "/search",
		Method:   "GET",
		ResponseMapping: ResponseMapping{
			ResultsPath: "data.results",
			Fields:      map[string]string{"name": "name"},
		},
	}

	result := TestProvider(context.Background(), cfg, "Paris", 48.8566, 2.3522, "2026-05-01", "2026-05-02", "EUR", 2)

	if !result.Success {
		t.Fatalf("expected Success=true for empty results, got error: %s", result.Error)
	}
	if result.ResultsCount != 0 {
		t.Errorf("ResultsCount = %d, want 0", result.ResultsCount)
	}
	if result.SampleResult != nil {
		t.Errorf("SampleResult = %v, want nil", result.SampleResult)
	}
}

func TestSubstituteEnvVars_InPreflight(t *testing.T) {
	t.Setenv("TRVL_TEST_API_KEY", "secret123")

	var receivedBody string
	preflightSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, 1024)
		n, _ := r.Body.Read(body)
		receivedBody = string(body[:n])

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"token":"tok-abc"}`)
	}))
	defer preflightSrv.Close()

	searchSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"results": []any{
				map[string]any{"name": "Env Hotel", "id": "eh1"},
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
		ID:       "env-preflight-test",
		Name:     "Env Preflight Test",
		Category: "hotels",
		Endpoint: searchSrv.URL + "/search",
		Method:   "GET",
		Headers: map[string]string{
			"Authorization": "Bearer ${auth_tok}",
		},
		Auth: &AuthConfig{
			Type:            "preflight",
			PreflightURL:    preflightSrv.URL + "/auth",
			PreflightMethod: "POST",
			PreflightBody:   "api_key=${env.TRVL_TEST_API_KEY}",
			PreflightHeaders: map[string]string{
				"Content-Type": "application/x-www-form-urlencoded",
			},
			Extractions: map[string]Extraction{
				"auth_tok": {
					Pattern:  `"token":"([^"]+)"`,
					Variable: "auth_tok",
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

	// Verify env var was substituted in preflight body.
	if receivedBody != "api_key=secret123" {
		t.Errorf("preflight body = %q, want %q", receivedBody, "api_key=secret123")
	}

	if len(hotels) != 1 {
		t.Fatalf("got %d hotels, want 1", len(hotels))
	}
	if hotels[0].Name != "Env Hotel" {
		t.Errorf("name = %q, want 'Env Hotel'", hotels[0].Name)
	}
}

func TestStatus(t *testing.T) {
	t.Run("new provider", func(t *testing.T) {
		cfg := &ProviderConfig{
			ID:   "status-new",
			Name: "New Provider",
		}
		if got := cfg.Status(); got != "new" {
			t.Errorf("Status() = %q, want 'new'", got)
		}
	})

	t.Run("provider with errors", func(t *testing.T) {
		cfg := &ProviderConfig{
			ID:         "status-error",
			Name:       "Error Provider",
			ErrorCount: 3,
			LastError:  "connection refused",
		}
		if got := cfg.Status(); got != "error" {
			t.Errorf("Status() = %q, want 'error'", got)
		}
	})

	t.Run("provider with success", func(t *testing.T) {
		cfg := &ProviderConfig{
			ID:          "status-ok",
			Name:        "OK Provider",
			LastSuccess: time.Now(),
		}
		if got := cfg.Status(); got != "ok" {
			t.Errorf("Status() = %q, want 'ok'", got)
		}
	})

	t.Run("errors take precedence over success", func(t *testing.T) {
		cfg := &ProviderConfig{
			ID:          "status-both",
			Name:        "Both Provider",
			LastSuccess: time.Now(),
			ErrorCount:  1,
		}
		if got := cfg.Status(); got != "error" {
			t.Errorf("Status() = %q, want 'error' (errors should take precedence)", got)
		}
	})
}

func TestIsStale(t *testing.T) {
	t.Run("no errors is not stale", func(t *testing.T) {
		cfg := &ProviderConfig{
			ID:         "stale-ok",
			ErrorCount: 0,
		}
		if cfg.IsStale() {
			t.Error("IsStale() = true, want false for provider with no errors")
		}
	})

	t.Run("errors with no success is stale", func(t *testing.T) {
		cfg := &ProviderConfig{
			ID:         "stale-never-success",
			ErrorCount: 2,
		}
		if !cfg.IsStale() {
			t.Error("IsStale() = false, want true for provider with errors and no success")
		}
	})

	t.Run("errors with recent success is not stale", func(t *testing.T) {
		cfg := &ProviderConfig{
			ID:          "stale-recent",
			ErrorCount:  1,
			LastSuccess: time.Now().Add(-1 * time.Hour), // 1 hour ago
		}
		if cfg.IsStale() {
			t.Error("IsStale() = true, want false for provider with recent success")
		}
	})

	t.Run("errors with old success is stale", func(t *testing.T) {
		cfg := &ProviderConfig{
			ID:          "stale-old",
			ErrorCount:  1,
			LastSuccess: time.Now().Add(-48 * time.Hour), // 48 hours ago
		}
		if !cfg.IsStale() {
			t.Error("IsStale() = false, want true for provider with old success and errors")
		}
	})
}

func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsIdx(s, substr))
}

func containsIdx(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestDiscoverArrayPaths_FindsNestedArray(t *testing.T) {
	raw := map[string]any{
		"data": map[string]any{
			"presentation": map[string]any{
				"staysSearch": map[string]any{
					"results": []any{
						map[string]any{"name": "Hotel A", "id": "1"},
						map[string]any{"name": "Hotel B", "id": "2"},
					},
				},
			},
			"metadata": map[string]any{"total": 42},
		},
	}

	sug := discoverArrayPaths(raw, "")
	path, ok := sug["results_path"]
	if !ok {
		t.Fatal("expected results_path suggestion, got none")
	}
	if !containsStr(path, "data.presentation.staysSearch.results") {
		t.Errorf("unexpected results_path: %s", path)
	}
	if !containsStr(path, "2 items") {
		t.Errorf("expected item count in suggestion: %s", path)
	}
}

func TestDiscoverArrayPaths_ExcludesCurrentPath(t *testing.T) {
	raw := map[string]any{
		"results": []any{
			map[string]any{"name": "Hotel A"},
		},
	}

	sug := discoverArrayPaths(raw, "results")
	if _, ok := sug["results_path"]; ok {
		t.Error("should exclude the path the caller already tried")
	}
}

func TestDiscoverArrayPaths_IgnoresPrimitiveArrays(t *testing.T) {
	raw := map[string]any{
		"tags": []any{"a", "b", "c"},
		"items": []any{
			map[string]any{"name": "X"},
		},
	}

	sug := discoverArrayPaths(raw, "")
	path := sug["results_path"]
	if containsStr(path, "tags") {
		t.Error("should not suggest primitive arrays")
	}
	if !containsStr(path, "items") {
		t.Errorf("should suggest object arrays: %s", path)
	}
}

func TestDiscoverFieldMappings_FindsCommonFields(t *testing.T) {
	obj := map[string]any{
		"displayName": map[string]any{
			"text": "Grand Hotel",
		},
		"id":     "12345",
		"rating": 4.5,
		"location": map[string]any{
			"latitude":  48.856,
			"longitude": 2.352,
		},
		"priceInfo": map[string]any{
			"amount": 129.0,
		},
	}

	sug := discoverFieldMappings(obj, "")

	checks := map[string]string{
		"field:hotel_id": "id",
		"field:rating":   "rating",
		"field:lat":      "location.latitude",
		"field:lon":      "location.longitude",
	}

	for key, wantContains := range checks {
		got, ok := sug[key]
		if !ok {
			t.Errorf("missing suggestion for %s", key)
			continue
		}
		if !containsStr(got, wantContains) {
			t.Errorf("%s: got %q, want to contain %q", key, got, wantContains)
		}
	}
}

func TestDiscoverFieldMappings_FlatObject(t *testing.T) {
	obj := map[string]any{
		"name":  "Budget Inn",
		"price": 59.99,
		"lat":   51.5074,
		"lon":   -0.1278,
	}

	sug := discoverFieldMappings(obj, "")

	if sug["field:name"] != "name" {
		t.Errorf("name: %q", sug["field:name"])
	}
	if sug["field:price"] != "price" {
		t.Errorf("price: %q", sug["field:price"])
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || findSubstr(s, sub))
}

func findSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
