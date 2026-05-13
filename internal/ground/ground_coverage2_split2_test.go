package ground

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"golang.org/x/time/rate"
)

func TestFetchFinnlinesTimetables_MockHTTP500(t *testing.T) {
	origClient := finnlinesClient
	origLimiter := finnlinesLimiter
	t.Cleanup(func() {
		finnlinesClient = origClient
		finnlinesLimiter = origLimiter
	})
	finnlinesLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("error"))
	}))
	defer srv.Close()

	finnlinesClient = &http.Client{
		Transport: &redirectTransport{target: srv.URL},
		Timeout:   5 * time.Second,
	}

	_, err := fetchFinnlinesTimetables(context.Background(), "FIHEL", "DETRV", "2026-07-01")
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
}

// ============================================================
// fetchFinnlinesProducts via httptest (was 0%)
// ============================================================

func TestFetchFinnlinesProducts_MockHappyPath(t *testing.T) {
	origClient := finnlinesClient
	origLimiter := finnlinesLimiter
	t.Cleanup(func() {
		finnlinesClient = origClient
		finnlinesLimiter = origLimiter
	})
	finnlinesLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(finnlinesProductResponse{
			Data: struct {
				ListProductsAvailability []finnlinesProduct `json:"listProductsAvailability"`
			}{
				ListProductsAvailability: []finnlinesProduct{
					{Code: "BII", Type: "ACCOMMODATION", Name: "B-Inside cabin", MaxPeople: 2, Available: true, ChargePerUnit: 12900},
					{Code: "SE", Type: "SEAT", Name: "Seat", MaxPeople: 1, Available: true, ChargePerUnit: 0},
				},
			},
		})
	}))
	defer srv.Close()

	finnlinesClient = &http.Client{
		Transport: &redirectTransport{target: srv.URL},
		Timeout:   5 * time.Second,
	}

	products, err := fetchFinnlinesProducts(context.Background(), "FIHEL", "DETRV", "2026-07-01", "17:00")
	if err != nil {
		t.Fatalf("fetchFinnlinesProducts: %v", err)
	}
	if len(products) != 2 {
		t.Fatalf("expected 2 products, got %d", len(products))
	}
}

func TestFetchFinnlinesProducts_MockHTTP500(t *testing.T) {
	origClient := finnlinesClient
	origLimiter := finnlinesLimiter
	t.Cleanup(func() {
		finnlinesClient = origClient
		finnlinesLimiter = origLimiter
	})
	finnlinesLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("error"))
	}))
	defer srv.Close()

	finnlinesClient = &http.Client{
		Transport: &redirectTransport{target: srv.URL},
		Timeout:   5 * time.Second,
	}

	_, err := fetchFinnlinesProducts(context.Background(), "FIHEL", "DETRV", "2026-07-01", "17:00")
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
}

func TestFetchFinnlinesProducts_MockGraphQLError(t *testing.T) {
	origClient := finnlinesClient
	origLimiter := finnlinesLimiter
	t.Cleanup(func() {
		finnlinesClient = origClient
		finnlinesLimiter = origLimiter
	})
	finnlinesLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"errors": []map[string]string{{"message": "product error"}},
		})
	}))
	defer srv.Close()

	finnlinesClient = &http.Client{
		Transport: &redirectTransport{target: srv.URL},
		Timeout:   5 * time.Second,
	}

	_, err := fetchFinnlinesProducts(context.Background(), "FIHEL", "DETRV", "2026-07-01", "17:00")
	if err == nil {
		t.Fatal("expected error for GraphQL error")
	}
}

// ============================================================
// SearchEuropeanSleeper via httptest (additional cases, was 0%)
// ============================================================

func TestSearchEuropeanSleeper_MockHappyPath2(t *testing.T) {
	origClient := europeanSleeperClient
	origLimiter := europeanSleeperLimiter
	t.Cleanup(func() {
		europeanSleeperClient = origClient
		europeanSleeperLimiter = origLimiter
	})
	europeanSleeperLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(europeanSleeperTripsResponse{
			Trips: []europeanSleeperTrip{
				{
					DepartureTime: "2026-07-01T19:22:00",
					ArrivalTime:   "2026-07-02T07:30:00",
					Duration:      728,
					Prices: []europeanSleeperPrice{
						{Amount: 49.0, Currency: "EUR", Class: "seat"},
						{Amount: 99.0, Currency: "EUR", Class: "couchette"},
					},
					Segments: []europeanSleeperSegment{
						{
							DepartureTime: "2026-07-01T19:22:00",
							ArrivalTime:   "2026-07-02T07:30:00",
							Origin:        europeanSleeperTripStop{Name: "Brussels-Midi"},
							Destination:   europeanSleeperTripStop{Name: "Prague hl.n."},
							TrainNumber:   "NJ 40421",
							Operator:      "European Sleeper",
						},
					},
				},
			},
		})
	}))
	defer srv.Close()

	europeanSleeperClient = &http.Client{
		Transport: &redirectTransport{target: srv.URL},
		Timeout:   5 * time.Second,
	}

	routes, err := SearchEuropeanSleeper(context.Background(), "Brussels", "Prague", "2026-07-01", "EUR")
	if err != nil {
		t.Fatalf("SearchEuropeanSleeper: %v", err)
	}
	if len(routes) == 0 {
		t.Fatal("expected at least 1 route")
	}
	r := routes[0]
	if r.Provider != "european_sleeper" {
		t.Errorf("provider = %q, want european_sleeper", r.Provider)
	}
	if r.Price != 49.0 {
		t.Errorf("price = %v, want 49.0", r.Price)
	}
}

func TestSearchEuropeanSleeper_MockNon200(t *testing.T) {
	origClient := europeanSleeperClient
	origLimiter := europeanSleeperLimiter
	t.Cleanup(func() {
		europeanSleeperClient = origClient
		europeanSleeperLimiter = origLimiter
	})
	europeanSleeperLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	europeanSleeperClient = &http.Client{
		Transport: &redirectTransport{target: srv.URL},
		Timeout:   5 * time.Second,
	}

	routes, err := SearchEuropeanSleeper(context.Background(), "Brussels", "Prague", "2026-07-01", "EUR")
	if err != nil {
		t.Fatalf("expected nil error for non-200 (graceful), got: %v", err)
	}
	if len(routes) != 0 {
		t.Errorf("expected empty routes for non-200, got %d", len(routes))
	}
}

func TestSearchEuropeanSleeper_UnknownStation2(t *testing.T) {
	_, err := SearchEuropeanSleeper(context.Background(), "NoSuchCityABC", "Prague", "2026-07-01", "EUR")
	if err == nil {
		t.Fatal("expected error for unknown station")
	}
}

func TestSearchEuropeanSleeper_MockDefaultCurrency(t *testing.T) {
	origClient := europeanSleeperClient
	origLimiter := europeanSleeperLimiter
	t.Cleanup(func() {
		europeanSleeperClient = origClient
		europeanSleeperLimiter = origLimiter
	})
	europeanSleeperLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "currency=EUR") {
			t.Errorf("expected currency=EUR in query, got: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(europeanSleeperTripsResponse{Trips: nil})
	}))
	defer srv.Close()

	europeanSleeperClient = &http.Client{
		Transport: &redirectTransport{target: srv.URL},
		Timeout:   5 * time.Second,
	}

	_, err := SearchEuropeanSleeper(context.Background(), "Brussels", "Prague", "2026-07-01", "")
	if err != nil {
		t.Fatalf("SearchEuropeanSleeper: %v", err)
	}
}

func TestSearchEuropeanSleeper_MockEmptyTrips(t *testing.T) {
	origClient := europeanSleeperClient
	origLimiter := europeanSleeperLimiter
	t.Cleanup(func() {
		europeanSleeperClient = origClient
		europeanSleeperLimiter = origLimiter
	})
	europeanSleeperLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(europeanSleeperTripsResponse{Trips: nil})
	}))
	defer srv.Close()

	europeanSleeperClient = &http.Client{
		Transport: &redirectTransport{target: srv.URL},
		Timeout:   5 * time.Second,
	}

	routes, err := SearchEuropeanSleeper(context.Background(), "Brussels", "Prague", "2026-07-01", "EUR")
	if err != nil {
		t.Fatalf("SearchEuropeanSleeper: %v", err)
	}
	if len(routes) != 0 {
		t.Errorf("expected 0 routes for empty trips, got %d", len(routes))
	}
}

// ============================================================
// SearchDigitransit — additional coverage (was 16.7%)
// ============================================================

func TestSearchDigitransit_MockHappyPath2(t *testing.T) {
	origClient := httpClient
	origLimiter := digitransitLimiter
	t.Cleanup(func() {
		httpClient = origClient
		digitransitLimiter = origLimiter
	})
	digitransitLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"plan": map[string]any{
					"itineraries": []any{
						map[string]any{
							"duration":     5400,
							"startTime":    float64(time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC).UnixMilli()),
							"endTime":      float64(time.Date(2026, 7, 1, 9, 30, 0, 0, time.UTC).UnixMilli()),
							"walkDistance": 500.0,
							"waitingTime":  0,
							"transfers":    0,
							"legs": []any{
								map[string]any{
									"mode":       "RAIL",
									"startTime":  float64(time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC).UnixMilli()),
									"endTime":    float64(time.Date(2026, 7, 1, 9, 30, 0, 0, time.UTC).UnixMilli()),
									"duration":   5400,
									"from":       map[string]any{"name": "Helsinki", "stop": map[string]any{"code": "HEL"}},
									"to":         map[string]any{"name": "Tampere", "stop": map[string]any{"code": "TRE"}},
									"route":      map[string]any{"shortName": "IC 123", "agency": map[string]any{"name": "VR"}},
									"transitLeg": true,
								},
							},
						},
					},
				},
			},
		})
	}))
	defer srv.Close()

	httpClient = &http.Client{
		Transport: &redirectTransport{target: srv.URL},
		Timeout:   5 * time.Second,
	}

	routes, err := SearchDigitransit(context.Background(), "Helsinki", "Tampere", "2026-07-01", "EUR")
	if err != nil {
		t.Fatalf("SearchDigitransit: %v", err)
	}
	if len(routes) == 0 {
		t.Fatal("expected at least 1 route")
	}
	if routes[0].Provider != "vr" {
		t.Errorf("provider = %q, want vr", routes[0].Provider)
	}
}

// ============================================================
// Additional edge case coverage
// ============================================================

func TestIsProviderNotApplicable_ContextDeadline(t *testing.T) {
	if !isProviderNotApplicable(context.DeadlineExceeded) {
		t.Error("expected true for context.DeadlineExceeded")
	}
}

func TestIsProviderNotApplicable_Nil2(t *testing.T) {
	if isProviderNotApplicable(nil) {
		t.Error("expected false for nil error")
	}
}

func TestDeduplicateGroundRoutes_SameProviderDifferentTimes(t *testing.T) {
	routes := []models.GroundRoute{
		{Provider: "db", Departure: models.GroundStop{Time: "08:00"}, Arrival: models.GroundStop{Time: "12:00"}, Price: 45},
		{Provider: "db", Departure: models.GroundStop{Time: "10:00"}, Arrival: models.GroundStop{Time: "14:00"}, Price: 45},
	}
	result := deduplicateGroundRoutes(routes)
	if len(result) != 2 {
		t.Errorf("expected 2 routes (different times), got %d", len(result))
	}
}

func TestFilterUnavailableGroundRoutes_MixedProviders(t *testing.T) {
	routes := []models.GroundRoute{
		{Provider: "flixbus", Price: 0},
		{Provider: "transitous", Price: 0},
		{Provider: "db", Price: 25},
	}
	result := filterUnavailableGroundRoutes(routes)
	if len(result) != 2 {
		t.Errorf("expected 2 routes, got %d", len(result))
	}
}

func TestSearchResultBufferCapacity_NonZero(t *testing.T) {
	cap := searchResultBufferCapacity()
	if cap <= 0 {
		t.Errorf("searchResultBufferCapacity() = %d, want > 0", cap)
	}
}
