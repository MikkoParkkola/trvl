package ground

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/cache"
	"github.com/MikkoParkkola/trvl/internal/models"
	"golang.org/x/time/rate"
)

// ============================================================
// estimateTaxiRoadDistanceKm — all 4 branches (was 40%)
// ============================================================

func TestEstimateTaxiRoadDistanceKm_AllBranches(t *testing.T) {
	tests := []struct {
		name    string
		input   float64
		wantMin float64
		wantMax float64
	}{
		{"very short (<1km)", 0.5, 2, 2}, // returns fixed 2
		{"short (<8km)", 5, 5 * 1.45, 5*1.45 + 0.01},
		{"medium (<25km)", 15, 15 * 1.30, 15*1.30 + 0.01},
		{"long (>=25km)", 50, 50 * 1.18, 50*1.18 + 0.01},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := estimateTaxiRoadDistanceKm(tt.input)
			if got < tt.wantMin || got > tt.wantMax {
				t.Errorf("estimateTaxiRoadDistanceKm(%v) = %v, want in [%v, %v]", tt.input, got, tt.wantMin, tt.wantMax)
			}
		})
	}
}

// ============================================================
// estimateTaxiDurationMinutes — all 4 branches (was 60%)
// ============================================================

func TestEstimateTaxiDurationMinutes_AllBranches(t *testing.T) {
	tests := []struct {
		name string
		km   float64
	}{
		{"short (<10km)", 5},
		{"medium (10-25km)", 15},
		{"long (25-60km)", 40},
		{"very long (>=60km)", 80},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := estimateTaxiDurationMinutes(tt.km)
			if got <= 0 {
				t.Errorf("estimateTaxiDurationMinutes(%v) = %d, want > 0", tt.km, got)
			}
		})
	}

	// Verify monotonic: longer distance should mean longer duration.
	d5 := estimateTaxiDurationMinutes(5)
	d15 := estimateTaxiDurationMinutes(15)
	d40 := estimateTaxiDurationMinutes(40)
	d80 := estimateTaxiDurationMinutes(80)
	if d5 >= d15 || d15 >= d40 || d40 >= d80 {
		t.Errorf("duration should be monotonic: %d, %d, %d, %d", d5, d15, d40, d80)
	}
}

// ============================================================
// estimateTaxiFareEUR — high/low relationship (was 83%)
// ============================================================

func TestEstimateTaxiFareEUR_HighGreaterThanLow(t *testing.T) {
	for _, cc := range []string{"DE", "CH", "PL", "ZZ"} {
		t.Run(cc, func(t *testing.T) {
			low, high := estimateTaxiFareEUR(20, 30, cc)
			if low <= 0 {
				t.Errorf("low = %v, want > 0", low)
			}
			if high < low {
				t.Errorf("high (%v) < low (%v)", high, low)
			}
		})
	}
}

// ============================================================
// roundTaxiMoney
// ============================================================

func TestRoundTaxiMoney_Precision(t *testing.T) {
	tests := []struct {
		input float64
		want  float64
	}{
		{19.999, 20.00},
		{0.005, 0.01},
		{100.0, 100.0},
		{0, 0},
		{-5.555, -5.56},
	}
	for _, tt := range tests {
		got := roundTaxiMoney(tt.input)
		if got != tt.want {
			t.Errorf("roundTaxiMoney(%v) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// ============================================================
// buildTaxiDirectionsURL
// ============================================================

func TestBuildTaxiDirectionsURL_ContainsCoordinates(t *testing.T) {
	url := buildTaxiDirectionsURL(48.8566, 2.3522, 49.0097, 2.5479)
	if url == "" {
		t.Fatal("expected non-empty URL")
	}
	for _, sub := range []string{"google.com/maps", "48.856600", "2.352200", "49.009700", "2.547900", "travelmode=driving"} {
		if !containsSubstring(url, sub) {
			t.Errorf("URL %q should contain %q", url, sub)
		}
	}
}

// ============================================================
// EstimateTaxiTransfer — error paths
// ============================================================

func TestEstimateTaxiTransfer_EmptyFromName(t *testing.T) {
	_, err := EstimateTaxiTransfer(context.Background(), TaxiEstimateInput{
		FromName: "", ToName: "B",
		FromLat: 48.0, FromLon: 2.0, ToLat: 49.0, ToLon: 3.0,
	})
	if err == nil {
		t.Fatal("expected error for empty from_name")
	}
}

func TestEstimateTaxiTransfer_EmptyToName(t *testing.T) {
	_, err := EstimateTaxiTransfer(context.Background(), TaxiEstimateInput{
		FromName: "A", ToName: "",
		FromLat: 48.0, FromLon: 2.0, ToLat: 49.0, ToLon: 3.0,
	})
	if err == nil {
		t.Fatal("expected error for empty to_name")
	}
}

func TestEstimateTaxiTransfer_WhitespaceNames(t *testing.T) {
	_, err := EstimateTaxiTransfer(context.Background(), TaxiEstimateInput{
		FromName: "  ", ToName: "  ",
		FromLat: 48.0, FromLon: 2.0, ToLat: 49.0, ToLon: 3.0,
	})
	if err == nil {
		t.Fatal("expected error for whitespace-only names")
	}
}

// ============================================================
// SearchTransitous via httptest (was 0%)
// ============================================================

func TestSearchTransitous_MockHappyPath(t *testing.T) {
	origClient := transitousClient
	origLimiter := transitousLimiter
	t.Cleanup(func() {
		transitousClient = origClient
		transitousLimiter = origLimiter
	})
	transitousLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(transitousResponse{
			From: transitousPlace{Name: "Helsinki"},
			To:   transitousPlace{Name: "Tampere"},
			Itineraries: []transitousItinerary{
				{
					Duration:  5400, // 90 minutes
					StartTime: "2026-07-01T08:00:00+03:00",
					EndTime:   "2026-07-01T09:30:00+03:00",
					Transfers: 0,
					Legs: []transitousLeg{
						{
							Mode:      "RAIL",
							From:      transitousPlace{Name: "Helsinki Railway Station"},
							To:        transitousPlace{Name: "Tampere Railway Station"},
							Duration:  5400,
							StartTime: "2026-07-01T08:00:00+03:00",
							EndTime:   "2026-07-01T09:30:00+03:00",
							Route:     &transitousRoute{Agency: "VR", ShortName: "IC 123"},
						},
					},
				},
				{
					// Walk-only itinerary — should be filtered.
					Duration:  3600,
					StartTime: "2026-07-01T10:00:00+03:00",
					EndTime:   "2026-07-01T11:00:00+03:00",
					Transfers: 0,
					Legs: []transitousLeg{
						{Mode: "WALK", Duration: 3600},
					},
				},
			},
		})
	}))
	defer srv.Close()

	transitousClient = srv.Client()
	// Override the endpoint by patching the client to redirect.
	origEndpoint := transitousEndpoint
	_ = origEndpoint // can't override const, use different approach

	// We test the internal logic by calling SearchTransitous with a server
	// that's listening. The function constructs URL from const endpoint, so
	// we need to redirect via client transport.
	transitousClient = &http.Client{
		Transport: &redirectTransport{target: srv.URL},
		Timeout:   5 * time.Second,
	}

	routes, err := SearchTransitous(context.Background(), 60.1699, 24.9384, 61.4978, 23.7610, "2026-07-01")
	if err != nil {
		t.Fatalf("SearchTransitous: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("expected 1 route (walk-only filtered), got %d", len(routes))
	}

	r := routes[0]
	if r.Provider != "transitous" {
		t.Errorf("provider = %q, want transitous", r.Provider)
	}
	if r.Type != "train" {
		t.Errorf("type = %q, want train", r.Type)
	}
	if r.Duration != 90 {
		t.Errorf("duration = %d, want 90", r.Duration)
	}
	if r.Transfers != 0 {
		t.Errorf("transfers = %d, want 0", r.Transfers)
	}
	if r.Departure.City != "Helsinki" {
		t.Errorf("departure city = %q, want Helsinki", r.Departure.City)
	}
	if r.Arrival.City != "Tampere" {
		t.Errorf("arrival city = %q, want Tampere", r.Arrival.City)
	}
	if len(r.Legs) != 1 {
		t.Fatalf("expected 1 leg, got %d", len(r.Legs))
	}
	if r.Legs[0].Provider != "VR" {
		t.Errorf("leg provider = %q, want VR", r.Legs[0].Provider)
	}
}

// redirectTransport redirects all requests to a target base URL.
type redirectTransport struct {
	target string
}

func (t *redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	newURL := t.target + req.URL.Path + "?" + req.URL.RawQuery
	newReq, err := http.NewRequestWithContext(req.Context(), req.Method, newURL, req.Body)
	if err != nil {
		return nil, err
	}
	newReq.Header = req.Header
	return http.DefaultClient.Do(newReq)
}

func TestSearchTransitous_MockHTTPError(t *testing.T) {
	origClient := transitousClient
	origLimiter := transitousLimiter
	t.Cleanup(func() {
		transitousClient = origClient
		transitousLimiter = origLimiter
	})
	transitousLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("overloaded"))
	}))
	defer srv.Close()

	transitousClient = &http.Client{
		Transport: &redirectTransport{target: srv.URL},
		Timeout:   5 * time.Second,
	}

	_, err := SearchTransitous(context.Background(), 60.1699, 24.9384, 61.4978, 23.7610, "2026-07-01")
	if err == nil {
		t.Fatal("expected error for 503 response")
	}
}

func TestSearchTransitous_MockEmptyItineraries(t *testing.T) {
	origClient := transitousClient
	origLimiter := transitousLimiter
	t.Cleanup(func() {
		transitousClient = origClient
		transitousLimiter = origLimiter
	})
	transitousLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(transitousResponse{
			From:        transitousPlace{Name: "A"},
			To:          transitousPlace{Name: "B"},
			Itineraries: nil,
		})
	}))
	defer srv.Close()

	transitousClient = &http.Client{
		Transport: &redirectTransport{target: srv.URL},
		Timeout:   5 * time.Second,
	}

	routes, err := SearchTransitous(context.Background(), 60.1699, 24.9384, 61.4978, 23.7610, "2026-07-01")
	if err != nil {
		t.Fatalf("SearchTransitous: %v", err)
	}
	if len(routes) != 0 {
		t.Errorf("expected 0 routes for empty itineraries, got %d", len(routes))
	}
}

// ============================================================
// geocodeCity via httptest (was 52%)
// ============================================================

func TestGeocodeCity_MockHappyPath(t *testing.T) {
	origClient := httpClient
	t.Cleanup(func() {
		httpClient = origClient
		// Clean up cache entry.
		geoCityCache.Lock()
		delete(geoCityCache.entries, "testcity123")
		geoCityCache.Unlock()
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]string{
			{"lat": "52.5200", "lon": "13.4050"},
		})
	}))
	defer srv.Close()

	httpClient = &http.Client{
		Transport: &redirectTransport{target: srv.URL},
		Timeout:   5 * time.Second,
	}

	coord, err := geocodeCity(context.Background(), "testcity123")
	if err != nil {
		t.Fatalf("geocodeCity: %v", err)
	}
	if coord.lat < 52.51 || coord.lat > 52.53 {
		t.Errorf("lat = %v, want ~52.52", coord.lat)
	}
	if coord.lon < 13.40 || coord.lon > 13.41 {
		t.Errorf("lon = %v, want ~13.405", coord.lon)
	}

	// Second call should hit cache.
	coord2, err := geocodeCity(context.Background(), "testcity123")
	if err != nil {
		t.Fatalf("geocodeCity (cached): %v", err)
	}
	if coord2.lat != coord.lat || coord2.lon != coord.lon {
		t.Error("cached result should match")
	}
}

func TestGeocodeCity_MockNoResults(t *testing.T) {
	origClient := httpClient
	t.Cleanup(func() {
		httpClient = origClient
		geoCityCache.Lock()
		delete(geoCityCache.entries, "nosuchtestcity999")
		geoCityCache.Unlock()
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}))
	defer srv.Close()

	httpClient = &http.Client{
		Transport: &redirectTransport{target: srv.URL},
		Timeout:   5 * time.Second,
	}

	_, err := geocodeCity(context.Background(), "nosuchtestcity999")
	if err == nil {
		t.Fatal("expected error for no geocoding results")
	}
}

func TestGeocodeCity_MockHTTP500(t *testing.T) {
	origClient := httpClient
	t.Cleanup(func() {
		httpClient = origClient
		geoCityCache.Lock()
		delete(geoCityCache.entries, "errortestcity777")
		geoCityCache.Unlock()
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	httpClient = &http.Client{
		Transport: &redirectTransport{target: srv.URL},
		Timeout:   5 * time.Second,
	}

	_, err := geocodeCity(context.Background(), "errortestcity777")
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
}

// ============================================================
// normaliseFinCity — branch coverage (was 60%)
// ============================================================

func TestNormaliseFinCity_AllBranches(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"jyvaskyla", "jyväskylä"},
		{"seinajoki", "seinäjoki"},
		{"hameenlinna", "hämeenlinna"},
		{"helsinki", "helsinki"}, // passthrough
		{"tampere", "tampere"},   // passthrough
	}
	for _, tt := range tests {
		got := normaliseFinCity(tt.input)
		if got != tt.want {
			t.Errorf("normaliseFinCity(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ============================================================
// convertTaxiFare — non-EUR currency path (was 37%)
// ============================================================

func TestConvertTaxiFare_NonEURCurrency(t *testing.T) {
	ctx := context.Background()
	// When requesting a non-EUR currency that can't be converted (no live rates),
	// it should fall back to EUR.
	low, high, cur := convertTaxiFare(ctx, 10.0, 20.0, "XYZ")
	// Either falls back to EUR or converts — both are valid.
	if cur == "" {
		t.Error("currency should not be empty")
	}
	if low <= 0 || high <= 0 {
		t.Errorf("low=%v high=%v, both should be > 0", low, high)
	}
}

// ============================================================
// filterGroundRoutes — schedule-only providers with zero price
// ============================================================

func TestFilterGroundRoutes_ScheduleOnlyProviders(t *testing.T) {
	for _, provider := range []string{"distribusion", "transitous", "db", "ns", "oebb", "vr",
		"european_sleeper", "snalltaget", "tallink", "stenaline", "dfds",
		"vikingline", "eckeroline", "finnlines", "ferryhopper"} {
		routes := []models.GroundRoute{
			{Provider: provider, Price: 0, Departure: models.GroundStop{Time: "T08:00"}, Arrival: models.GroundStop{Time: "T12:00"}},
		}
		filtered := filterGroundRoutes(routes, SearchOptions{})
		if len(filtered) != 1 {
			t.Errorf("schedule-only provider %q with price=0 should be kept, got %d routes", provider, len(filtered))
		}
	}
}

// ============================================================
// SearchByName — cache hit path
// ============================================================

func TestSearchByName_CacheHit(t *testing.T) {
	// Seed the cache with a known result.
	testResult := &models.GroundSearchResult{
		Success: true,
		Count:   1,
		Routes: []models.GroundRoute{
			{Provider: "cached", Price: 42, Currency: "EUR"},
		},
	}
	data, _ := json.Marshal(testResult)

	// Build the same cache key SearchByName would use (cache.Key hashes).
	from, to, date := "CacheTestFrom", "CacheTestTo", "2099-12-31"
	opts := SearchOptions{Currency: "EUR"}
	cacheKey := cache.Key("ground", from+"|"+to+"|"+date+"|EUR|all|0.00||false")

	groundCache.Set(cacheKey, data, 5*time.Minute)
	// Cache entries expire naturally; no manual cleanup needed.

	result, err := SearchByName(context.Background(), from, to, date, opts)
	if err != nil {
		t.Fatalf("SearchByName: %v", err)
	}
	if !result.Success {
		t.Fatal("expected cache hit to return success")
	}
	if result.Count != 1 {
		t.Errorf("count = %d, want 1", result.Count)
	}
	if result.Routes[0].Provider != "cached" {
		t.Errorf("provider = %q, want cached", result.Routes[0].Provider)
	}
}

// ============================================================
// SearchByName — NoCache bypasses cache
// ============================================================

func TestSearchByName_NoCache(t *testing.T) {
	// Use a provider that won't make network calls (nonexistent provider filter).
	from, to, date := "NoCacheTestFrom", "NoCacheTestTo", "2099-12-31"
	opts := SearchOptions{Currency: "EUR", NoCache: true, Providers: []string{"nonexistent_xyz"}}

	// Seed the cache with a known result.
	testResult := &models.GroundSearchResult{
		Success: true,
		Count:   1,
		Routes: []models.GroundRoute{
			{Provider: "cached", Price: 42, Currency: "EUR"},
		},
	}
	data, _ := json.Marshal(testResult)

	cacheKey := cache.Key("ground", from+"|"+to+"|"+date+"|EUR|nonexistent_xyz|0.00||false")
	groundCache.Set(cacheKey, data, 5*time.Minute)

	// With NoCache=true, should NOT use the cache.
	result, err := SearchByName(context.Background(), from, to, date, opts)
	if err != nil {
		t.Fatalf("SearchByName: %v", err)
	}
	if len(result.Routes) > 0 && result.Routes[0].Provider == "cached" {
		t.Error("NoCache=true should bypass cache")
	}
}

// ============================================================
// SearchByName — provider filter
// ============================================================

func TestSearchByName_ProviderFilter(t *testing.T) {
	// Filter to a nonexistent provider so no network calls are made.
	opts := SearchOptions{
		Currency:  "EUR",
		Providers: []string{"nonexistent_provider_xyz"},
		NoCache:   true,
	}
	result, err := SearchByName(context.Background(), "FakeProvFilterA", "FakeProvFilterB", "2099-12-31", opts)
	if err != nil {
		t.Fatalf("SearchByName: %v", err)
	}
	// No providers match, so we get empty results.
	if result.Success {
		t.Error("expected no success when all providers filtered out")
	}
}

// ============================================================
// SetHTTPClient / SetEckeroLineClient — coverage for client.go
// ============================================================

func TestSetHTTPClient(t *testing.T) {
	orig := httpClient
	t.Cleanup(func() { httpClient = orig })

	c := &http.Client{Timeout: 99 * time.Second}
	SetHTTPClient(c)
	if httpClient != c {
		t.Error("SetHTTPClient did not replace the package-level client")
	}
}

func TestSetEckeroLineClient(t *testing.T) {
	orig := eckerolineClient
	t.Cleanup(func() { eckerolineClient = orig })

	c := &http.Client{Timeout: 99 * time.Second}
	SetEckeroLineClient(c)
	if eckerolineClient != c {
		t.Error("SetEckeroLineClient did not replace the package-level client")
	}
}

// ============================================================
// eurostarRouteDuration — all branches (was 37%)
// ============================================================

func TestEurostarRouteDuration_AllRoutes(t *testing.T) {
	tests := []struct {
		from string
		to   string
		want int
	}{
		{"London", "Paris", 135},
		{"Paris", "London", 135},
		{"London", "Brussels", 120},
		{"Brussels", "London", 120},
		{"London", "Amsterdam", 195},
		{"Amsterdam", "London", 195},
		{"London", "Rotterdam", 180},
		{"Rotterdam", "London", 180},
		{"London", "Cologne", 240},
		{"Cologne", "London", 240},
		{"Unknown", "Unknown", 135}, // default
	}
	for _, tt := range tests {
		got := eurostarRouteDuration(tt.from, tt.to)
		if got != tt.want {
			t.Errorf("eurostarRouteDuration(%q, %q) = %d, want %d", tt.from, tt.to, got, tt.want)
		}
	}
}

// ============================================================
// oebbShopSetHeaders (was 0%)
// ============================================================

func TestOebbShopSetHeaders_WithToken(t *testing.T) {
	req, _ := http.NewRequest("GET", "https://example.com", nil)
	oebbShopSetHeaders(req, "abc123")
	if req.Header.Get("Accept") != "application/json" {
		t.Errorf("Accept = %q, want application/json", req.Header.Get("Accept"))
	}
	if req.Header.Get("Channel") != "inet" {
		t.Errorf("Channel = %q, want inet", req.Header.Get("Channel"))
	}
	if req.Header.Get("accesstoken") != "abc123" {
		t.Errorf("accesstoken = %q, want abc123", req.Header.Get("accesstoken"))
	}
}

func TestOebbShopSetHeaders_WithoutToken(t *testing.T) {
	req, _ := http.NewRequest("GET", "https://example.com", nil)
	oebbShopSetHeaders(req, "")
	if req.Header.Get("accesstoken") != "" {
		t.Errorf("accesstoken should be empty, got %q", req.Header.Get("accesstoken"))
	}
}

// ============================================================
// searchTransitousByName via httptest (was 42%)
// ============================================================

func TestSearchTransitousByName_MockGeocodeSuccess(t *testing.T) {
	origClient := httpClient
	origTransClient := transitousClient
	origLimiter := transitousLimiter
	t.Cleanup(func() {
		httpClient = origClient
		transitousClient = origTransClient
		transitousLimiter = origLimiter
		// Clean up test cities from geocode cache.
		geoCityCache.Lock()
		delete(geoCityCache.entries, "mock_city_from_xyz")
		delete(geoCityCache.entries, "mock_city_to_xyz")
		geoCityCache.Unlock()
	})
	transitousLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	// Mock geocoding endpoint.
	geoSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]string{
			{"lat": "60.1699", "lon": "24.9384"},
		})
	}))
	defer geoSrv.Close()

	httpClient = &http.Client{
		Transport: &redirectTransport{target: geoSrv.URL},
		Timeout:   5 * time.Second,
	}

	// Mock transitous endpoint.
	transSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(transitousResponse{
			From:        transitousPlace{Name: "From City"},
			To:          transitousPlace{Name: "To City"},
			Itineraries: []transitousItinerary{},
		})
	}))
	defer transSrv.Close()

	transitousClient = &http.Client{
		Transport: &redirectTransport{target: transSrv.URL},
		Timeout:   5 * time.Second,
	}

	routes, err := searchTransitousByName(context.Background(), "mock_city_from_xyz", "mock_city_to_xyz", "2026-07-01")
	if err != nil {
		t.Fatalf("searchTransitousByName: %v", err)
	}
	if len(routes) != 0 {
		t.Errorf("expected 0 routes for empty itineraries, got %d", len(routes))
	}
}

// ============================================================
// rateLimitedDo — error path (was 66%)
// ============================================================

func TestRateLimitedDo_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	limiter := rate.NewLimiter(rate.Limit(1), 1)
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://example.com", nil)
	_, err := rateLimitedDo(ctx, limiter, req)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}
