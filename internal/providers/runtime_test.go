package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/time/rate"
)

func TestSubstituteVars(t *testing.T) {
	vars := map[string]string{
		"${checkin}":  "2025-06-01",
		"${checkout}": "2025-06-05",
		"${currency}": "USD",
		"${guests}":   "2",
		"${lat}":      "48.856613",
		"${lon}":      "2.352222",
	}

	input := "https://api.example.com/search?checkin=${checkin}&checkout=${checkout}&currency=${currency}&guests=${guests}"
	want := "https://api.example.com/search?checkin=2025-06-01&checkout=2025-06-05&currency=USD&guests=2"

	got := substituteVars(input, vars)
	if got != want {
		t.Errorf("substituteVars:\n got  %s\n want %s", got, want)
	}
}

func TestSubstituteVarsBodyTemplate(t *testing.T) {
	vars := map[string]string{
		"${ne_lat}": "49.006613",
		"${ne_lon}": "2.502222",
		"${sw_lat}": "48.706613",
		"${sw_lon}": "2.202222",
	}

	input := `{"bounds":{"ne":{"lat":${ne_lat},"lon":${ne_lon}},"sw":{"lat":${sw_lat},"lon":${sw_lon}}}}`
	want := `{"bounds":{"ne":{"lat":49.006613,"lon":2.502222},"sw":{"lat":48.706613,"lon":2.202222}}}`

	got := substituteVars(input, vars)
	if got != want {
		t.Errorf("substituteVars body template:\n got  %s\n want %s", got, want)
	}
}

func TestJSONPathSimple(t *testing.T) {
	data := map[string]any{
		"results": []any{
			map[string]any{"name": "Hotel A"},
			map[string]any{"name": "Hotel B"},
		},
	}

	got := jsonPath(data, "results")
	arr, ok := got.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", got)
	}
	if len(arr) != 2 {
		t.Fatalf("expected 2 results, got %d", len(arr))
	}
}

func TestJSONPathNested(t *testing.T) {
	data := map[string]any{
		"data": map[string]any{
			"search": map[string]any{
				"results": []any{
					map[string]any{"name": "Nested Hotel"},
				},
			},
		},
	}

	got := jsonPath(data, "data.search.results")
	arr, ok := got.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", got)
	}
	if len(arr) != 1 {
		t.Fatalf("expected 1 result, got %d", len(arr))
	}
	m := arr[0].(map[string]any)
	if m["name"] != "Nested Hotel" {
		t.Errorf("expected 'Nested Hotel', got %v", m["name"])
	}
}

func TestJSONPathMissing(t *testing.T) {
	data := map[string]any{"foo": "bar"}
	got := jsonPath(data, "missing.path")
	if got != nil {
		t.Errorf("expected nil for missing path, got %v", got)
	}
}

func TestJSONPathEmpty(t *testing.T) {
	data := map[string]any{"foo": "bar"}
	got := jsonPath(data, "")
	if got == nil {
		t.Error("expected data for empty path, got nil")
	}
}

func TestJSONPathScalar(t *testing.T) {
	data := map[string]any{
		"hotels": map[string]any{
			"name":   "Grand Plaza",
			"rating": 4.5,
		},
	}

	name := jsonPath(data, "hotels.name")
	if name != "Grand Plaza" {
		t.Errorf("expected 'Grand Plaza', got %v", name)
	}

	rating := jsonPath(data, "hotels.rating")
	if rating != 4.5 {
		t.Errorf("expected 4.5, got %v", rating)
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		config  ProviderConfig
		wantErr bool
	}{
		{
			name: "valid config",
			config: ProviderConfig{
				ID:       "test",
				Name:     "Test Provider",
				Category: "hotels",
				Endpoint: "https://api.example.com/search",
				ResponseMapping: ResponseMapping{
					ResultsPath: "results",
				},
			},
			wantErr: false,
		},
		{
			name: "missing id",
			config: ProviderConfig{
				Name:     "Test",
				Category: "hotels",
				Endpoint: "https://api.example.com",
				ResponseMapping: ResponseMapping{
					ResultsPath: "results",
				},
			},
			wantErr: true,
		},
		{
			name: "missing name",
			config: ProviderConfig{
				ID:       "test",
				Category: "hotels",
				Endpoint: "https://api.example.com",
				ResponseMapping: ResponseMapping{
					ResultsPath: "results",
				},
			},
			wantErr: true,
		},
		{
			name: "missing category",
			config: ProviderConfig{
				ID:       "test",
				Name:     "Test",
				Endpoint: "https://api.example.com",
				ResponseMapping: ResponseMapping{
					ResultsPath: "results",
				},
			},
			wantErr: true,
		},
		{
			name: "missing endpoint",
			config: ProviderConfig{
				ID:       "test",
				Name:     "Test",
				Category: "hotels",
				ResponseMapping: ResponseMapping{
					ResultsPath: "results",
				},
			},
			wantErr: true,
		},
		{
			name: "missing results_path",
			config: ProviderConfig{
				ID:       "test",
				Name:     "Test",
				Category: "hotels",
				Endpoint: "https://api.example.com",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestEndpointDomain(t *testing.T) {
	cfg := &ProviderConfig{Endpoint: "https://api.example.com:8443/v1/search"}
	if got := cfg.EndpointDomain(); got != "api.example.com:8443" {
		t.Errorf("EndpointDomain() = %q, want %q", got, "api.example.com:8443")
	}

	cfg2 := &ProviderConfig{Endpoint: "https://hotels.example.com/search"}
	if got := cfg2.EndpointDomain(); got != "hotels.example.com" {
		t.Errorf("EndpointDomain() = %q, want %q", got, "hotels.example.com")
	}

	cfg3 := &ProviderConfig{Endpoint: "://invalid"}
	if got := cfg3.EndpointDomain(); got != "" {
		t.Errorf("EndpointDomain() for invalid URL = %q, want empty", got)
	}
}

func TestRateLimiterCreation(t *testing.T) {
	dir := t.TempDir()
	reg, err := NewRegistryAt(dir)
	if err != nil {
		t.Fatalf("NewRegistryAt: %v", err)
	}

	rt := NewRuntime(reg)

	// Custom rate.
	cfg := &ProviderConfig{
		ID:       "rate-test",
		Name:     "Rate Test",
		Category: "hotels",
		Endpoint: "https://example.com",
		RateLimit: RateLimitConfig{
			RequestsPerSecond: 5.0,
			Burst:             3,
		},
	}

	pc := rt.getOrCreateClient(cfg)
	if pc.limiter.Limit() != rate.Limit(5.0) {
		t.Errorf("limiter rate = %v, want 5.0", pc.limiter.Limit())
	}
	if pc.limiter.Burst() != 3 {
		t.Errorf("limiter burst = %d, want 3", pc.limiter.Burst())
	}

	// Default rate.
	cfgDefault := &ProviderConfig{
		ID:       "rate-default",
		Name:     "Rate Default",
		Category: "hotels",
		Endpoint: "https://example.com",
	}

	pcDefault := rt.getOrCreateClient(cfgDefault)
	if pcDefault.limiter.Limit() != rate.Limit(defaultRPS) {
		t.Errorf("default limiter rate = %v, want %v", pcDefault.limiter.Limit(), defaultRPS)
	}
	if pcDefault.limiter.Burst() != defaultBurst {
		t.Errorf("default limiter burst = %d, want %d", pcDefault.limiter.Burst(), defaultBurst)
	}
}

func TestBoundingBox(t *testing.T) {
	// The bounding box is computed inside searchProvider. We verify it through
	// variable substitution by checking that the mock server receives correct values.
	lat := 48.856613
	lon := 2.352222

	neLat := lat + boundingBoxOffset
	neLon := lon + boundingBoxOffset
	swLat := lat - boundingBoxOffset
	swLon := lon - boundingBoxOffset

	const eps = 1e-9
	assertClose := func(t *testing.T, name string, got, want float64) {
		t.Helper()
		diff := got - want
		if diff < -eps || diff > eps {
			t.Errorf("%s = %f, want %f (diff %e)", name, got, want, diff)
		}
	}
	assertClose(t, "ne_lat", neLat, 49.006613)
	assertClose(t, "ne_lon", neLon, 2.502222)
	assertClose(t, "sw_lat", swLat, 48.706613)
	assertClose(t, "sw_lon", swLon, 2.202222)
}

func TestSearchHotelsFullFlow(t *testing.T) {
	// Mock server returning hotel results.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify query params.
		q := r.URL.Query()
		if q.Get("checkin") != "2025-06-01" {
			t.Errorf("checkin = %q, want 2025-06-01", q.Get("checkin"))
		}
		if q.Get("checkout") != "2025-06-05" {
			t.Errorf("checkout = %q, want 2025-06-05", q.Get("checkout"))
		}

		resp := map[string]any{
			"data": map[string]any{
				"hotels": []any{
					map[string]any{
						"id":         "h1",
						"hotel_name": "Grand Plaza",
						"stars":      4.0,
						"rate":       4.8,
						"reviews":    120.0,
						"cost":       199.99,
						"curr":       "USD",
						"addr":       "123 Main St",
						"latitude":   48.856613,
						"longitude":  2.352222,
					},
					map[string]any{
						"id":         "h2",
						"hotel_name": "Budget Inn",
						"stars":      2.0,
						"rate":       3.5,
						"reviews":    45.0,
						"cost":       79.99,
						"curr":       "USD",
						"addr":       "456 Side St",
						"latitude":   48.857,
						"longitude":  2.353,
					},
				},
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
		ID:       "test-hotel",
		Name:     "Test Hotels",
		Category: "hotels",
		Endpoint: srv.URL + "/search",
		Method:   "GET",
		QueryParams: map[string]string{
			"checkin":  "${checkin}",
			"checkout": "${checkout}",
			"currency": "${currency}",
		},
		ResponseMapping: ResponseMapping{
			ResultsPath: "data.hotels",
			Fields: map[string]string{
				"hotel_id":     "id",
				"name":         "hotel_name",
				"stars":        "stars",
				"rating":       "rate",
				"review_count": "reviews",
				"price":        "cost",
				"currency":     "curr",
				"address":      "addr",
				"lat":          "latitude",
				"lon":          "longitude",
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
	hotels, _, err := rt.SearchHotels(context.Background(), "Paris", 48.856613, 2.352222, "2025-06-01", "2025-06-05", "USD", 2, nil)
	if err != nil {
		t.Fatalf("SearchHotels: %v", err)
	}

	if len(hotels) != 2 {
		t.Fatalf("got %d hotels, want 2", len(hotels))
	}

	h := hotels[0]
	if h.Name != "Grand Plaza" && hotels[1].Name != "Grand Plaza" {
		t.Errorf("expected Grand Plaza in results, got %q and %q", hotels[0].Name, hotels[1].Name)
	}

	// Find Grand Plaza specifically.
	var gp *struct{ h int }
	for i, hotel := range hotels {
		if hotel.Name == "Grand Plaza" {
			idx := i
			gp = &struct{ h int }{h: idx}
			break
		}
	}
	if gp == nil {
		t.Fatal("Grand Plaza not found in results")
	}

	grand := hotels[gp.h]
	if grand.HotelID != "h1" {
		t.Errorf("hotel_id = %q, want h1", grand.HotelID)
	}
	if grand.Stars != 4 {
		t.Errorf("stars = %d, want 4", grand.Stars)
	}
	if grand.Rating != 4.8 {
		t.Errorf("rating = %f, want 4.8", grand.Rating)
	}
	if grand.ReviewCount != 120 {
		t.Errorf("review_count = %d, want 120", grand.ReviewCount)
	}
	if grand.Price != 199.99 {
		t.Errorf("price = %f, want 199.99", grand.Price)
	}
	if grand.Currency != "USD" {
		t.Errorf("currency = %q, want USD", grand.Currency)
	}
	if grand.Address != "123 Main St" {
		t.Errorf("address = %q, want '123 Main St'", grand.Address)
	}
}

func TestSearchHotelsNoProviders(t *testing.T) {
	dir := t.TempDir()
	reg, err := NewRegistryAt(dir)
	if err != nil {
		t.Fatalf("NewRegistryAt: %v", err)
	}

	rt := NewRuntime(reg)
	hotels, _, err := rt.SearchHotels(context.Background(), "Paris", 48.856613, 2.352222, "2025-06-01", "2025-06-05", "USD", 2, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hotels != nil {
		t.Errorf("expected nil, got %d hotels", len(hotels))
	}
}

func TestPreflightAuthExtraction(t *testing.T) {
	// Mock preflight server returning a page with an API key.
	preflightSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Session-Token", "sess-abc123")
		_, _ = fmt.Fprintf(w, `<html><script>var apiKey = "key-xyz789";</script></html>`)
	}))
	defer preflightSrv.Close()

	// Mock search server that verifies auth values were extracted.
	searchSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey := r.Header.Get("X-Api-Key")
		session := r.Header.Get("X-Session")
		if apiKey != "key-xyz789" {
			t.Errorf("X-Api-Key = %q, want 'key-xyz789'", apiKey)
		}
		if session != "sess-abc123" {
			t.Errorf("X-Session = %q, want 'sess-abc123'", session)
		}

		resp := map[string]any{
			"results": []any{
				map[string]any{
					"name": "Auth Hotel",
					"id":   "ah1",
				},
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
		ID:       "auth-test",
		Name:     "Auth Test",
		Category: "hotels",
		Endpoint: searchSrv.URL + "/search",
		Method:   "GET",
		Headers: map[string]string{
			"X-Api-Key": "${api_key}",
			"X-Session": "${session_token}",
		},
		Auth: &AuthConfig{
			Type:         "preflight",
			PreflightURL: preflightSrv.URL,
			Extractions: map[string]Extraction{
				"api_key": {
					Pattern:  `apiKey = "([^"]+)"`,
					Variable: "api_key",
				},
				"session_token": {
					Pattern:  `(.+)`,
					Variable: "session_token",
					Header:   "X-Session-Token",
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
	if hotels[0].Name != "Auth Hotel" {
		t.Errorf("name = %q, want 'Auth Hotel'", hotels[0].Name)
	}
}

func TestRegistryRoundTrip(t *testing.T) {
	dir := t.TempDir()
	reg, err := NewRegistryAt(dir)
	if err != nil {
		t.Fatalf("NewRegistryAt: %v", err)
	}

	cfg := &ProviderConfig{
		ID:       "round-trip",
		Name:     "Round Trip",
		Category: "hotels",
		Endpoint: "https://example.com/search",
		ResponseMapping: ResponseMapping{
			ResultsPath: "results",
			Fields:      map[string]string{"name": "name"},
		},
		Version: 1,
	}

	if err := reg.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify file exists.
	fpath := filepath.Join(dir, "round-trip.json")
	if _, err := os.Stat(fpath); err != nil {
		t.Fatalf("config file not found: %v", err)
	}

	// Create new registry from same directory — should load config.
	reg2, err := NewRegistryAt(dir)
	if err != nil {
		t.Fatalf("NewRegistryAt reload: %v", err)
	}

	got := reg2.Get("round-trip")
	if got == nil {
		t.Fatal("Get returned nil after reload")
	}
	if got.Name != "Round Trip" {
		t.Errorf("Name = %q, want 'Round Trip'", got.Name)
	}

	// Test ListByCategory.
	hotels := reg2.ListByCategory("hotels")
	if len(hotels) != 1 {
		t.Errorf("ListByCategory hotel = %d, want 1", len(hotels))
	}

	// Test Delete.
	if err := reg2.Delete("round-trip"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if reg2.Get("round-trip") != nil {
		t.Error("Get after Delete returned non-nil")
	}
	if _, err := os.Stat(fpath); !os.IsNotExist(err) {
		t.Error("config file still exists after Delete")
	}
}

func TestRegistryMarkSuccessAndError(t *testing.T) {
	dir := t.TempDir()
	reg, err := NewRegistryAt(dir)
	if err != nil {
		t.Fatalf("NewRegistryAt: %v", err)
	}

	cfg := &ProviderConfig{
		ID:       "mark-test",
		Name:     "Mark Test",
		Category: "hotels",
		Endpoint: "https://example.com",
		ResponseMapping: ResponseMapping{
			ResultsPath: "results",
		},
	}
	if err := reg.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Mark error.
	reg.MarkError("mark-test", "connection timeout")
	got := reg.Get("mark-test")
	if got.ErrorCount != 1 {
		t.Errorf("ErrorCount = %d, want 1", got.ErrorCount)
	}
	if got.LastError != "connection timeout" {
		t.Errorf("LastError = %q, want 'connection timeout'", got.LastError)
	}

	// Mark another error.
	reg.MarkError("mark-test", "server error")
	got = reg.Get("mark-test")
	if got.ErrorCount != 2 {
		t.Errorf("ErrorCount = %d, want 2", got.ErrorCount)
	}

	// Mark success — resets error count.
	reg.MarkSuccess("mark-test")
	got = reg.Get("mark-test")
	if got.ErrorCount != 0 {
		t.Errorf("ErrorCount after success = %d, want 0", got.ErrorCount)
	}
	if got.LastSuccess.IsZero() {
		t.Error("LastSuccess is zero after MarkSuccess")
	}
}

func TestMapHotelResult(t *testing.T) {
	raw := map[string]any{
		"hotel_name": "Test Hotel",
		"id":         "t1",
		"star_count": 3.0,
		"user_rate":  4.2,
		"num_review": 55.0,
		"nightly":    129.50,
		"money":      "EUR",
		"location":   "Berlin, Germany",
		"geo_lat":    52.520008,
		"geo_lon":    13.404954,
		"link":       "https://example.com/book/t1",
		"eco":        true,
	}

	fields := map[string]string{
		"name":          "hotel_name",
		"hotel_id":      "id",
		"stars":         "star_count",
		"rating":        "user_rate",
		"review_count":  "num_review",
		"price":         "nightly",
		"currency":      "money",
		"address":       "location",
		"lat":           "geo_lat",
		"lon":           "geo_lon",
		"booking_url":   "link",
		"eco_certified": "eco",
	}

	h := mapHotelResult(raw, fields)

	if h.Name != "Test Hotel" {
		t.Errorf("Name = %q, want 'Test Hotel'", h.Name)
	}
	if h.HotelID != "t1" {
		t.Errorf("HotelID = %q, want 't1'", h.HotelID)
	}
	if h.Stars != 3 {
		t.Errorf("Stars = %d, want 3", h.Stars)
	}
	if h.Rating != 4.2 {
		t.Errorf("Rating = %f, want 4.2", h.Rating)
	}
	if h.ReviewCount != 55 {
		t.Errorf("ReviewCount = %d, want 55", h.ReviewCount)
	}
	if h.Price != 129.50 {
		t.Errorf("Price = %f, want 129.50", h.Price)
	}
	if h.Currency != "EUR" {
		t.Errorf("Currency = %q, want 'EUR'", h.Currency)
	}
	if h.Address != "Berlin, Germany" {
		t.Errorf("Address = %q, want 'Berlin, Germany'", h.Address)
	}
	if h.Lat != 52.520008 {
		t.Errorf("Lat = %f, want 52.520008", h.Lat)
	}
	if h.Lon != 13.404954 {
		t.Errorf("Lon = %f, want 13.404954", h.Lon)
	}
	if h.BookingURL != "https://example.com/book/t1" {
		t.Errorf("BookingURL = %q, want 'https://example.com/book/t1'", h.BookingURL)
	}
	if !h.EcoCertified {
		t.Error("EcoCertified = false, want true")
	}
}

func TestToFloat64(t *testing.T) {
	tests := []struct {
		input any
		want  float64
	}{
		{3.14, 3.14},
		{42, 42.0},
		{"99.5", 99.5},
		{true, 0},
		{nil, 0},
		// Composite strings: firstNumericToken extracts the leading number.
		{"4.84 (25)", 4.84},
		{"€ 204", 204},
		{"€ 61", 61},
		// Thousands separator in prices.
		{"€1,204", 1204},
		{"$2,500 per night", 2500},
	}
	for _, tt := range tests {
		got := toFloat64(tt.input)
		if got != tt.want {
			t.Errorf("toFloat64(%v) = %f, want %f", tt.input, got, tt.want)
		}
	}
}
