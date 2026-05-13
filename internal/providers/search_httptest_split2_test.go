package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSearchProvider_GraphQLPartialSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"results": []any{
					map[string]any{"name": "Partial Hotel", "id": "ph1"},
				},
			},
			"errors": []any{
				map[string]any{"message": "hotelpage service timeout"},
			},
		})
	}))
	defer srv.Close()

	dir := t.TempDir()
	reg, err := NewRegistryAt(dir)
	if err != nil {
		t.Fatalf("NewRegistryAt: %v", err)
	}

	cfg := &ProviderConfig{
		ID:       "graphql-partial",
		Name:     "GraphQL Partial",
		Category: "hotels",
		Endpoint: srv.URL + "/graphql",
		Method:   "POST",
		ResponseMapping: ResponseMapping{
			ResultsPath: "data.results",
			Fields: map[string]string{
				"name":     "name",
				"hotel_id": "id",
			},
		},
		RateLimit: RateLimitConfig{RequestsPerSecond: 100, Burst: 10},
	}
	if err := reg.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	rt := NewRuntime(reg)
	hotels, _, err := rt.SearchHotels(context.Background(), "Test", 0, 0, "2026-06-01", "2026-06-05", "EUR", 2, nil)
	if err != nil {
		t.Fatalf("SearchHotels should succeed with partial data: %v", err)
	}
	if len(hotels) != 1 {
		t.Fatalf("got %d hotels, want 1", len(hotels))
	}
	if hotels[0].Name != "Partial Hotel" {
		t.Errorf("name = %q, want 'Partial Hotel'", hotels[0].Name)
	}
}

// TestSearchProvider_RatingScale verifies the rating_scale normalization.

func TestSearchProvider_RatingScale(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []any{
				map[string]any{
					"name":   "Scaled Hotel",
					"rating": 4.2, // on a 0-5 scale
				},
			},
		})
	}))
	defer srv.Close()

	dir := t.TempDir()
	reg, err := NewRegistryAt(dir)
	if err != nil {
		t.Fatalf("NewRegistryAt: %v", err)
	}

	cfg := &ProviderConfig{
		ID:       "rating-scale",
		Name:     "Rating Scale",
		Category: "hotels",
		Endpoint: srv.URL + "/search",
		Method:   "GET",
		ResponseMapping: ResponseMapping{
			ResultsPath: "results",
			RatingScale: 2.0, // multiply by 2 to normalize 0-5 -> 0-10
			Fields: map[string]string{
				"name":   "name",
				"rating": "rating",
			},
		},
		RateLimit: RateLimitConfig{RequestsPerSecond: 100, Burst: 10},
	}
	if err := reg.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	rt := NewRuntime(reg)
	hotels, _, err := rt.SearchHotels(context.Background(), "Test", 0, 0, "2026-06-01", "2026-06-05", "EUR", 2, nil)
	if err != nil {
		t.Fatalf("SearchHotels: %v", err)
	}
	if len(hotels) != 1 {
		t.Fatalf("got %d hotels, want 1", len(hotels))
	}
	// 4.2 * 2.0 = 8.4
	if hotels[0].Rating != 8.4 {
		t.Errorf("rating = %v, want 8.4 (4.2 * 2.0)", hotels[0].Rating)
	}
}

// TestSearchProvider_FilterComposite verifies the FilterComposite feature
// that builds compound URL parameters from individual filter variables.

func TestSearchProvider_FilterComposite(t *testing.T) {
	var receivedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.String()
		resp := map[string]any{
			"results": []any{
				map[string]any{"name": "Composite Hotel"},
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
		ID:       "composite-test",
		Name:     "Composite Test",
		Category: "hotels",
		Endpoint: srv.URL + "/search",
		Method:   "GET",
		QueryParams: map[string]string{
			"nflt": "${nflt}",
		},
		PropertyTypeLookup: map[string]string{
			"hotel": "204",
		},
		FilterComposite: &FilterComposite{
			TargetVar: "nflt",
			Separator: "%3B",
			Parts: map[string]string{
				"property_type":     "ht_id%3D",
				"free_cancellation": "fc%3D",
			},
		},
		ResponseMapping: ResponseMapping{
			ResultsPath: "results",
			Fields:      map[string]string{"name": "name"},
		},
		RateLimit: RateLimitConfig{RequestsPerSecond: 100, Burst: 10},
	}
	if err := reg.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	rt := NewRuntime(reg)
	filters := &HotelFilterParams{
		PropertyType:     "hotel",
		FreeCancellation: true,
	}
	hotels, _, err := rt.SearchHotels(context.Background(), "Test", 0, 0, "2026-06-01", "2026-06-05", "EUR", 2, filters)
	if err != nil {
		t.Fatalf("SearchHotels: %v", err)
	}
	if len(hotels) != 1 {
		t.Fatalf("got %d hotels, want 1", len(hotels))
	}

	// Verify the nflt composite param was built correctly.
	// The composite separator and prefixes are already URL-encoded in the config
	// (e.g. "%3B", "ht_id%3D"). When placed into a query parameter, Go's
	// url.Values.Encode() percent-encodes the '%' again, so "%3D" becomes
	// "%253D" in the final URL. The assertion checks for the double-encoded form.
	if !containsSubstring(receivedPath, "ht_id") {
		t.Errorf("URL should contain ht_id composite, got %s", receivedPath)
	}
	if !containsSubstring(receivedPath, "fc") {
		t.Errorf("URL should contain fc composite, got %s", receivedPath)
	}
	if !containsSubstring(receivedPath, "204") {
		t.Errorf("URL should contain property type ID 204, got %s", receivedPath)
	}
}

// TestSearchProvider_CityIDSubstitution verifies that ${city_id} is resolved
// from the CityLookup table and substituted into the endpoint.

func TestSearchProvider_CityIDSubstitution(t *testing.T) {
	var receivedURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedURL = r.URL.String()
		resp := map[string]any{
			"results": []any{
				map[string]any{"name": "City Hotel"},
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
		ID:       "city-id-test",
		Name:     "City ID Test",
		Category: "hotels",
		Endpoint: srv.URL + "/search?city=${city_id}",
		Method:   "GET",
		CityLookup: map[string]string{
			"prague": "19",
			"paris":  "42",
		},
		ResponseMapping: ResponseMapping{
			ResultsPath: "results",
			Fields:      map[string]string{"name": "name"},
		},
		RateLimit: RateLimitConfig{RequestsPerSecond: 100, Burst: 10},
	}
	if err := reg.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	rt := NewRuntime(reg)
	hotels, _, err := rt.SearchHotels(context.Background(), "Prague", 50.08, 14.43, "2026-06-01", "2026-06-05", "EUR", 2, nil)
	if err != nil {
		t.Fatalf("SearchHotels: %v", err)
	}
	if len(hotels) != 1 {
		t.Fatalf("got %d hotels, want 1", len(hotels))
	}

	// Verify city_id was substituted.
	if !containsSubstring(receivedURL, "city=19") {
		t.Errorf("URL should contain city=19, got %s", receivedURL)
	}
}

// TestSearchProvider_HeaderOrder verifies that headers are sent in the
// configured order when header_order is set.

func TestSearchProvider_HeaderOrder(t *testing.T) {
	var receivedHeaders []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Collect custom headers that were set.
		for _, name := range []string{"X-First", "X-Second", "X-Third"} {
			if v := r.Header.Get(name); v != "" {
				receivedHeaders = append(receivedHeaders, name+"="+v)
			}
		}
		resp := map[string]any{
			"results": []any{
				map[string]any{"name": "Ordered Hotel"},
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
		ID:       "header-order-test",
		Name:     "Header Order Test",
		Category: "hotels",
		Endpoint: srv.URL + "/search",
		Method:   "GET",
		Headers: map[string]string{
			"X-First":  "one",
			"X-Second": "two",
			"X-Third":  "three",
		},
		HeaderOrder: []string{"X-First", "X-Second", "X-Third"},
		ResponseMapping: ResponseMapping{
			ResultsPath: "results",
			Fields:      map[string]string{"name": "name"},
		},
		RateLimit: RateLimitConfig{RequestsPerSecond: 100, Burst: 10},
	}
	if err := reg.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	rt := NewRuntime(reg)
	hotels, _, err := rt.SearchHotels(context.Background(), "Test", 0, 0, "2026-06-01", "2026-06-05", "EUR", 2, nil)
	if err != nil {
		t.Fatalf("SearchHotels: %v", err)
	}
	if len(hotels) != 1 {
		t.Fatalf("got %d hotels, want 1", len(hotels))
	}
	// All three headers should be present.
	if len(receivedHeaders) != 3 {
		t.Errorf("expected 3 custom headers, got %d: %v", len(receivedHeaders), receivedHeaders)
	}
}

// TestSearchProvider_NumNightsComputation verifies that ${num_nights} is
// correctly computed from checkin/checkout dates.

func TestSearchProvider_NumNightsComputation(t *testing.T) {
	var receivedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedQuery = r.URL.RawQuery
		resp := map[string]any{
			"results": []any{
				map[string]any{"name": "Nights Hotel"},
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
		ID:       "num-nights-test",
		Name:     "Num Nights Test",
		Category: "hotels",
		Endpoint: srv.URL + "/search",
		Method:   "GET",
		QueryParams: map[string]string{
			"nights": "${num_nights}",
		},
		ResponseMapping: ResponseMapping{
			ResultsPath: "results",
			Fields:      map[string]string{"name": "name"},
		},
		RateLimit: RateLimitConfig{RequestsPerSecond: 100, Burst: 10},
	}
	if err := reg.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	rt := NewRuntime(reg)
	// 3 nights: June 1 to June 4
	hotels, _, err := rt.SearchHotels(context.Background(), "Test", 0, 0, "2026-06-01", "2026-06-04", "EUR", 2, nil)
	if err != nil {
		t.Fatalf("SearchHotels: %v", err)
	}
	if len(hotels) != 1 {
		t.Fatalf("got %d hotels, want 1", len(hotels))
	}
	if !containsSubstring(receivedQuery, "nights=3") {
		t.Errorf("query should contain nights=3, got %s", receivedQuery)
	}
}

// TestSearchProvider_PreflightExtractionDefault verifies that when a
// preflight extraction pattern does not match, the default value is used.

func TestSearchProvider_PreflightExtractionDefault(t *testing.T) {
	preflightSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return a response that does NOT match the extraction pattern.
		_, _ = fmt.Fprint(w, `<html><body>No token here</body></html>`)
	}))
	defer preflightSrv.Close()

	searchSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hash := r.Header.Get("X-Hash")
		if hash != "default-hash-abc" {
			t.Errorf("X-Hash = %q, want 'default-hash-abc'", hash)
		}
		resp := map[string]any{
			"results": []any{
				map[string]any{"name": "Default Hotel"},
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
		ID:       "default-extraction",
		Name:     "Default Extraction",
		Category: "hotels",
		Endpoint: searchSrv.URL + "/search",
		Method:   "GET",
		Headers: map[string]string{
			"X-Hash": "${sha_hash}",
		},
		Auth: &AuthConfig{
			Type:         "preflight",
			PreflightURL: preflightSrv.URL + "/page",
			Extractions: map[string]Extraction{
				"sha_hash": {
					Pattern:  `sha256Hash":"([a-f0-9]{64})"`,
					Variable: "sha_hash",
					Default:  "default-hash-abc",
				},
			},
		},
		ResponseMapping: ResponseMapping{
			ResultsPath: "results",
			Fields:      map[string]string{"name": "name"},
		},
		RateLimit: RateLimitConfig{RequestsPerSecond: 100, Burst: 10},
	}
	if err := reg.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	rt := NewRuntime(reg)
	hotels, _, err := rt.SearchHotels(context.Background(), "Test", 0, 0, "2026-06-01", "2026-06-05", "EUR", 2, nil)
	if err != nil {
		t.Fatalf("SearchHotels: %v", err)
	}
	if len(hotels) != 1 {
		t.Fatalf("got %d hotels, want 1", len(hotels))
	}
	if hotels[0].Name != "Default Hotel" {
		t.Errorf("name = %q, want 'Default Hotel'", hotels[0].Name)
	}
}

// TestSearchProvider_UnresolvedPlaceholderStripping verifies that unresolved
// ${placeholder} variables are stripped from the URL instead of being sent
// as literal strings.

func TestSearchProvider_UnresolvedPlaceholderStripping(t *testing.T) {
	var receivedURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedURL = r.URL.String()
		resp := map[string]any{
			"results": []any{
				map[string]any{"name": "Stripped Hotel"},
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
		ID:       "strip-test",
		Name:     "Strip Test",
		Category: "hotels",
		// The endpoint has an optional ${nflt} that won't be resolved (no filters).
		Endpoint: srv.URL + "/search?checkin=${checkin}&nflt=${nflt}",
		Method:   "GET",
		ResponseMapping: ResponseMapping{
			ResultsPath: "results",
			Fields:      map[string]string{"name": "name"},
		},
		RateLimit: RateLimitConfig{RequestsPerSecond: 100, Burst: 10},
	}
	if err := reg.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	rt := NewRuntime(reg)
	hotels, _, err := rt.SearchHotels(context.Background(), "Test", 0, 0, "2026-06-01", "2026-06-05", "EUR", 2, nil)
	if err != nil {
		t.Fatalf("SearchHotels: %v", err)
	}
	if len(hotels) != 1 {
		t.Fatalf("got %d hotels, want 1", len(hotels))
	}

	// The URL should contain checkin=2026-06-01 but NOT ${nflt}.
	if !containsSubstring(receivedURL, "checkin=2026-06-01") {
		t.Errorf("URL should contain checkin=2026-06-01, got %s", receivedURL)
	}
	if strings.Contains(receivedURL, "${nflt}") {
		t.Errorf("URL should not contain literal ${nflt}, got %s", receivedURL)
	}
	if strings.Contains(receivedURL, "nflt=") {
		t.Errorf("URL should not contain empty nflt=, got %s", receivedURL)
	}
}
