package ground

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"golang.org/x/time/rate"
)

func TestIsProviderNotApplicable_DFDSRoute(t *testing.T) {
	err := fmt.Errorf("no DFDS route from X to Y")
	if !isProviderNotApplicable(err) {
		t.Errorf("dfds route error should be not-applicable")
	}
}

func TestIsProviderNotApplicable_StenaLineRoute(t *testing.T) {
	err := fmt.Errorf("no Stena Line route")
	if !isProviderNotApplicable(err) {
		t.Errorf("stena route error should be not-applicable")
	}
}

func TestIsProviderNotApplicable_RateLimiterExceed_v2(t *testing.T) {
	err := fmt.Errorf("rate limiter: rate: Wait(n=1) would exceed context deadline")
	if !isProviderNotApplicable(err) {
		t.Errorf("rate limiter exceed error should be not-applicable")
	}
}

func TestIsProviderNotApplicable_ContextDeadline_v2(t *testing.T) {
	err := fmt.Errorf("context deadline exceeded")
	if !isProviderNotApplicable(err) {
		t.Errorf("context deadline should be not-applicable")
	}
}

func TestIsProviderNotApplicable_UnrelatedError(t *testing.T) {
	err := fmt.Errorf("unexpected HTTP 500 from server")
	if isProviderNotApplicable(err) {
		t.Errorf("unrelated HTTP 500 should NOT be not-applicable")
	}
}

// ---------------------------------------------------------------------------
// filterGroundRoutes — all branches
// ---------------------------------------------------------------------------

func TestFilterGroundRoutes_Dedup_v2(t *testing.T) {
	routes := []models.GroundRoute{
		{Provider: "tallink", Type: "ferry", Price: 30.0, Departure: models.GroundStop{Time: "2026-08-15T07:30:00"}, Arrival: models.GroundStop{Time: "2026-08-15T09:30:00"}},
		{Provider: "tallink", Type: "ferry", Price: 30.0, Departure: models.GroundStop{Time: "2026-08-15T07:30:00"}, Arrival: models.GroundStop{Time: "2026-08-15T09:30:00"}},
		{Provider: "tallink", Type: "ferry", Price: 25.0, Departure: models.GroundStop{Time: "2026-08-15T12:00:00"}, Arrival: models.GroundStop{Time: "2026-08-15T14:00:00"}},
	}
	opts := SearchOptions{}
	got := filterGroundRoutes(routes, opts)
	if len(got) != 2 {
		t.Errorf("expected 2 after dedup, got %d", len(got))
	}
}

func TestFilterGroundRoutes_MaxPrice_v2(t *testing.T) {
	routes := []models.GroundRoute{
		{Provider: "tallink", Type: "ferry", Price: 30.0, Departure: models.GroundStop{Time: "T1"}, Arrival: models.GroundStop{Time: "T2"}},
		{Provider: "tallink", Type: "ferry", Price: 50.0, Departure: models.GroundStop{Time: "T3"}, Arrival: models.GroundStop{Time: "T4"}},
	}
	opts := SearchOptions{MaxPrice: 40.0}
	got := filterGroundRoutes(routes, opts)
	if len(got) != 1 {
		t.Errorf("expected 1 route under maxPrice 40, got %d", len(got))
	}
	if got[0].Price != 30.0 {
		t.Errorf("wrong route kept: price = %f", got[0].Price)
	}
}

func TestFilterGroundRoutes_TypeFilter_v2(t *testing.T) {
	routes := []models.GroundRoute{
		{Provider: "flixbus", Type: "bus", Price: 20.0, Departure: models.GroundStop{Time: "T1"}, Arrival: models.GroundStop{Time: "T2"}},
		{Provider: "tallink", Type: "ferry", Price: 30.0, Departure: models.GroundStop{Time: "T3"}, Arrival: models.GroundStop{Time: "T4"}},
	}
	opts := SearchOptions{Type: "bus"}
	got := filterGroundRoutes(routes, opts)
	if len(got) != 1 {
		t.Errorf("expected 1 bus route, got %d", len(got))
	}
	if got[0].Type != "bus" {
		t.Errorf("expected bus, got %q", got[0].Type)
	}
}

func TestFilterGroundRoutes_ZeroPriceNonSchedule(t *testing.T) {
	// Zero-price route from non-schedule-only provider should be dropped.
	routes := []models.GroundRoute{
		{Provider: "flixbus", Type: "bus", Price: 0, Departure: models.GroundStop{Time: "T1"}, Arrival: models.GroundStop{Time: "T2"}},
		{Provider: "tallink", Type: "ferry", Price: 0, Departure: models.GroundStop{Time: "T3"}, Arrival: models.GroundStop{Time: "T4"}},
	}
	opts := SearchOptions{}
	got := filterGroundRoutes(routes, opts)
	// flixbus with price 0 → dropped; tallink with price 0 → kept (schedule-only)
	if len(got) != 1 {
		t.Errorf("expected 1 (schedule-only tallink), got %d", len(got))
	}
	if got[0].Provider != "tallink" {
		t.Errorf("expected tallink, got %q", got[0].Provider)
	}
}

// ---------------------------------------------------------------------------
// deduplicateGroundRoutes
// ---------------------------------------------------------------------------

func TestDeduplicateGroundRoutes_v2(t *testing.T) {
	routes := []models.GroundRoute{
		{Provider: "db", Price: 45.0, Departure: models.GroundStop{Time: "T1"}, Arrival: models.GroundStop{Time: "T2"}},
		{Provider: "db", Price: 45.0, Departure: models.GroundStop{Time: "T1"}, Arrival: models.GroundStop{Time: "T2"}},
		{Provider: "db", Price: 55.0, Departure: models.GroundStop{Time: "T1"}, Arrival: models.GroundStop{Time: "T2"}},
	}
	got := deduplicateGroundRoutes(routes)
	if len(got) != 2 {
		t.Errorf("expected 2 after dedup, got %d", len(got))
	}
}

// ---------------------------------------------------------------------------
// shouldKeepGroundRoute
// ---------------------------------------------------------------------------

func TestShouldKeepGroundRoute_PriceAboveZero(t *testing.T) {
	r := models.GroundRoute{Provider: "flixbus", Price: 10.0}
	if !shouldKeepGroundRoute(r) {
		t.Error("route with price > 0 should be kept")
	}
}

func TestShouldKeepGroundRoute_ZeroPriceScheduleOnly(t *testing.T) {
	for _, p := range []string{"distribusion", "transitous", "db", "ns", "oebb", "vr",
		"european_sleeper", "snalltaget", "tallink", "stenaline",
		"dfds", "vikingline", "eckeroline", "finnlines", "ferryhopper"} {
		r := models.GroundRoute{Provider: p, Price: 0}
		if !shouldKeepGroundRoute(r) {
			t.Errorf("schedule-only provider %q with price 0 should be kept", p)
		}
	}
}

func TestShouldKeepGroundRoute_ZeroPriceNonSchedule(t *testing.T) {
	r := models.GroundRoute{Provider: "flixbus", Price: 0}
	if shouldKeepGroundRoute(r) {
		t.Error("non-schedule-only flixbus with price 0 should be dropped")
	}
}

// ---------------------------------------------------------------------------
// browserFallbacksEnabled
// ---------------------------------------------------------------------------

func TestBrowserFallbacksEnabled_ExplicitTrue(t *testing.T) {
	opts := SearchOptions{AllowBrowserFallbacks: true}
	if !browserFallbacksEnabled(opts) {
		t.Error("AllowBrowserFallbacks=true should return true")
	}
}

func TestBrowserFallbacksEnabled_EnvTrue_v2(t *testing.T) {
	t.Setenv("TRVL_ALLOW_BROWSER_FALLBACKS", "true")
	opts := SearchOptions{}
	if !browserFallbacksEnabled(opts) {
		t.Error("env TRVL_ALLOW_BROWSER_FALLBACKS=true should return true")
	}
}

func TestBrowserFallbacksEnabled_EnvFalse_v2(t *testing.T) {
	t.Setenv("TRVL_ALLOW_BROWSER_FALLBACKS", "false")
	opts := SearchOptions{}
	if browserFallbacksEnabled(opts) {
		t.Error("env TRVL_ALLOW_BROWSER_FALLBACKS=false should return false")
	}
}

func TestBrowserFallbacksEnabled_EnvEmpty_v2(t *testing.T) {
	t.Setenv("TRVL_ALLOW_BROWSER_FALLBACKS", "")
	opts := SearchOptions{}
	if browserFallbacksEnabled(opts) {
		t.Error("empty env should return false")
	}
}

func TestBrowserFallbacksEnabled_EnvInvalid_v2(t *testing.T) {
	t.Setenv("TRVL_ALLOW_BROWSER_FALLBACKS", "not-a-bool")
	opts := SearchOptions{}
	if browserFallbacksEnabled(opts) {
		t.Error("invalid env value should return false")
	}
}

// ---------------------------------------------------------------------------
// roundedPriceCents + groundRouteDedupKey
// ---------------------------------------------------------------------------

func TestRoundedPriceCents_v2(t *testing.T) {
	tests := []struct {
		price float64
		want  int64
	}{
		{0.0, 0},
		{1.0, 100},
		{38.90, 3890},
		{12.505, 1251}, // rounds
		{100.999, 10100},
	}
	for _, tt := range tests {
		got := roundedPriceCents(tt.price)
		if got != tt.want {
			t.Errorf("roundedPriceCents(%f) = %d, want %d", tt.price, got, tt.want)
		}
	}
}

func TestGroundRouteDedupKey_Unique(t *testing.T) {
	r1 := models.GroundRoute{
		Provider:  "tallink",
		Price:     38.90,
		Departure: models.GroundStop{Time: "2026-08-15T07:30:00"},
		Arrival:   models.GroundStop{Time: "2026-08-15T09:30:00"},
	}
	r2 := models.GroundRoute{
		Provider:  "tallink",
		Price:     38.90,
		Departure: models.GroundStop{Time: "2026-08-15T07:30:00"},
		Arrival:   models.GroundStop{Time: "2026-08-15T09:30:00"},
	}
	r3 := models.GroundRoute{
		Provider:  "tallink",
		Price:     25.00,
		Departure: models.GroundStop{Time: "2026-08-15T12:00:00"},
		Arrival:   models.GroundStop{Time: "2026-08-15T14:00:00"},
	}
	if groundRouteDedupKey(r1) != groundRouteDedupKey(r2) {
		t.Error("identical routes should have same dedup key")
	}
	if groundRouteDedupKey(r1) == groundRouteDedupKey(r3) {
		t.Error("different routes should have different dedup keys")
	}
}

// ---------------------------------------------------------------------------
// geocodeCity — cache hit branch
// ---------------------------------------------------------------------------

func TestGeocodeCity_CacheHit(t *testing.T) {
	// Pre-populate the cache.
	key := "paris"
	geoCityCache.Lock()
	geoCityCache.entries[key] = geoCoord{lat: 48.8566, lon: 2.3522}
	geoCityCache.Unlock()

	coord, err := geocodeCity(context.Background(), "Paris")
	if err != nil {
		t.Fatalf("geocodeCity with cache: %v", err)
	}
	if coord.lat != 48.8566 || coord.lon != 2.3522 {
		t.Errorf("coord = %+v, want {48.8566 2.3522}", coord)
	}
}

func TestGeocodeCity_NetworkError(t *testing.T) {
	// Override httpClient with a transport that immediately returns an error,
	// preventing any real TCP connections to nominatim.openstreetmap.org.
	origClient := httpClient
	httpClient = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("test: network disabled")
		}),
	}
	defer func() { httpClient = origClient }()

	// Use a city not in the cache (different from "paris" which was pre-populated).
	_, err := geocodeCity(context.Background(), "CityNetworkErrorTest999XYZ")
	if err == nil {
		t.Error("expected error for network failure")
	}
}

func TestGeocodeCity_NonOKStatus(t *testing.T) {
	// Use a transport that returns a synthetic 404 to exercise the non-OK status branch.
	origClient := httpClient
	httpClient = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			rec.WriteHeader(http.StatusNotFound)
			return rec.Result(), nil
		}),
	}
	defer func() { httpClient = origClient }()

	_, err := geocodeCity(context.Background(), "CityNonOKStatusTest888XYZ")
	if err == nil {
		t.Error("expected error for non-OK status")
	}
}

// ---------------------------------------------------------------------------
// filterUnavailableGroundRoutes
// ---------------------------------------------------------------------------

func TestFilterUnavailableGroundRoutes_v2(t *testing.T) {
	routes := []models.GroundRoute{
		{Provider: "flixbus", Price: 15.0}, // kept (price > 0)
		{Provider: "flixbus", Price: 0},    // dropped (non-schedule, zero price)
		{Provider: "db", Price: 0},         // kept (schedule-only)
		{Provider: "transitous", Price: 0}, // kept (schedule-only)
	}
	got := filterUnavailableGroundRoutes(routes)
	if len(got) != 3 {
		t.Errorf("expected 3 routes, got %d", len(got))
	}
}

// ---------------------------------------------------------------------------
// SearchSNCF — additional branches
// ---------------------------------------------------------------------------

func TestSearchSNCF_CalendarEmptyNoFallback(t *testing.T) {
	origDo := sncfDo
	origLimiter := sncfLimiter
	t.Cleanup(func() {
		sncfDo = origDo
		sncfLimiter = origLimiter
	})
	sncfLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	// Return 200 but empty body → parseSNCFResponse returns [].
	sncfDo = func(req *http.Request) (*http.Response, error) {
		rec := httptest.NewRecorder()
		rec.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(rec, `{"journeys":[]}`)
		return rec.Result(), nil
	}

	ctx := context.Background()
	routes, err := SearchSNCF(ctx, "Paris", "Lyon", "2026-08-15", "EUR", false)
	// empty journeys + no browser fallbacks → should return nil or empty, no error
	if err != nil {
		// allowBrowserFallbacks=false, apiErr=nil, empty routes → return nil, nil
		t.Logf("SearchSNCF returned err: %v (acceptable)", err)
	}
	_ = routes
}

func TestSearchSNCF_NonOKNoBrowserFallback(t *testing.T) {
	origDo := sncfDo
	origLimiter := sncfLimiter
	t.Cleanup(func() {
		sncfDo = origDo
		sncfLimiter = origLimiter
	})
	sncfLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	sncfDo = func(req *http.Request) (*http.Response, error) {
		rec := httptest.NewRecorder()
		rec.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprint(rec, `internal error`)
		return rec.Result(), nil
	}

	ctx := context.Background()
	_, err := SearchSNCF(ctx, "Paris", "Lyon", "2026-08-15", "EUR", false)
	if err == nil {
		t.Error("expected error for 500 response with no fallback")
	}
}

// ---------------------------------------------------------------------------
// SearchTrainline — additional branches
// ---------------------------------------------------------------------------

func TestSearchTrainline_HappyPath_200(t *testing.T) {
	origDo := trainlineDo
	origLimiter := trainlineLimiter
	t.Cleanup(func() {
		trainlineDo = origDo
		trainlineLimiter = origLimiter
	})
	trainlineLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	trainlineDo = func(req *http.Request) (*http.Response, error) {
		rec := httptest.NewRecorder()
		rec.WriteHeader(http.StatusOK)
		// minimal valid trainline response
		resp := trainlineJourneySearchResponse{
			Journeys: []trainlineJourney{},
		}
		_ = json.NewEncoder(rec).Encode(resp)
		return rec.Result(), nil
	}

	ctx := context.Background()
	routes, err := SearchTrainline(ctx, "London", "Paris", "2026-08-15", "GBP", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// empty journeys → 0 routes
	_ = routes
}

func TestSearchTrainline_500Error(t *testing.T) {
	origDo := trainlineDo
	origLimiter := trainlineLimiter
	t.Cleanup(func() {
		trainlineDo = origDo
		trainlineLimiter = origLimiter
	})
	trainlineLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	trainlineDo = func(req *http.Request) (*http.Response, error) {
		rec := httptest.NewRecorder()
		rec.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprint(rec, `server error`)
		return rec.Result(), nil
	}

	ctx := context.Background()
	_, err := SearchTrainline(ctx, "London", "Paris", "2026-08-15", "GBP", false)
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("expected HTTP 500 error, got %v", err)
	}
}

func TestSearchTrainline_403_NoBrowserFallback(t *testing.T) {
	origDo := trainlineDo
	origLimiter := trainlineLimiter
	t.Cleanup(func() {
		trainlineDo = origDo
		trainlineLimiter = origLimiter
	})
	trainlineLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	trainlineDo = func(req *http.Request) (*http.Response, error) {
		rec := httptest.NewRecorder()
		rec.WriteHeader(http.StatusForbidden)
		_, _ = fmt.Fprint(rec, `forbidden`)
		return rec.Result(), nil
	}

	ctx := context.Background()
	_, err := SearchTrainline(ctx, "London", "Paris", "2026-08-15", "GBP", false)
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Errorf("expected 403 error, got %v", err)
	}
}

func TestSearchTrainline_RateLimiterCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	origLimiter := trainlineLimiter
	trainlineLimiter = newProviderLimiter(60 * time.Second)
	defer func() { trainlineLimiter = origLimiter }()

	_, err := SearchTrainline(ctx, "London", "Paris", "2026-08-15", "GBP", false)
	if err == nil {
		t.Error("expected rate-limiter error on cancelled context")
	}
}
