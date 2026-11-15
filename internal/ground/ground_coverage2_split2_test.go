package ground

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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
// SearchEuropeanSleeper: upstream failures are errors, not empty results
// ============================================================

// A 404 HTML page (what the old /api/v3/offers path returns) is a failure the
// status layer must see; it used to be swallowed as "ok, zero routes".
func TestSearchEuropeanSleeper_Mock404IsError(t *testing.T) {
	origClient := europeanSleeperClient
	origLimiter := europeanSleeperLimiter
	t.Cleanup(func() {
		europeanSleeperClient = origClient
		europeanSleeperLimiter = origLimiter
	})
	europeanSleeperLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("<html>Not Found</html>"))
	}))
	defer srv.Close()

	europeanSleeperClient = &http.Client{
		Transport: &redirectTransport{target: srv.URL},
		Timeout:   5 * time.Second,
	}

	routes, err := SearchEuropeanSleeper(context.Background(), "Amsterdam", "Prague", "2026-11-20", "EUR")
	if !errors.Is(err, models.ErrParseFailed) {
		t.Fatalf("err = %v, want wrapping models.ErrParseFailed", err)
	}
	if len(routes) != 0 {
		t.Errorf("expected no routes on 404, got %d", len(routes))
	}
}

func TestSearchEuropeanSleeper_UnknownStation2(t *testing.T) {
	_, err := SearchEuropeanSleeper(context.Background(), "NoSuchCityABC", "Prague", "2026-07-01", "EUR")
	if !errors.Is(err, models.ErrNotApplicable) {
		t.Fatalf("err = %v, want wrapping models.ErrNotApplicable", err)
	}
}

// ============================================================
// SearchDigitransit — additional coverage (was 16.7%)
// ============================================================

func TestSearchDigitransit_MockHappyPath2(t *testing.T) {
	// Dynamic future date: the search query and the mock route times must be in
	// the future or they get filtered as past. Was hard-coded to 2026-07-01,
	// which broke once that date passed.
	day := time.Now().AddDate(0, 1, 0)
	origClient := digitransitClient
	origLimiter := digitransitLimiter
	t.Cleanup(func() {
		digitransitClient = origClient
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
							"startTime":    float64(time.Date(day.Year(), day.Month(), day.Day(), 8, 0, 0, 0, time.UTC).UnixMilli()),
							"endTime":      float64(time.Date(day.Year(), day.Month(), day.Day(), 9, 30, 0, 0, time.UTC).UnixMilli()),
							"walkDistance": 500.0,
							"waitingTime":  0,
							"transfers":    0,
							"legs": []any{
								map[string]any{
									"mode":       "RAIL",
									"startTime":  float64(time.Date(day.Year(), day.Month(), day.Day(), 8, 0, 0, 0, time.UTC).UnixMilli()),
									"endTime":    float64(time.Date(day.Year(), day.Month(), day.Day(), 9, 30, 0, 0, time.UTC).UnixMilli()),
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

	digitransitClient = &http.Client{
		Transport: &redirectTransport{target: srv.URL},
		Timeout:   5 * time.Second,
	}

	routes, err := SearchDigitransit(context.Background(), "Helsinki", "Tampere", day.Format("2006-01-02"), "EUR")
	if err != nil {
		t.Fatalf("SearchDigitransit: %v", err)
	}
	if len(routes) == 0 {
		t.Fatal("expected at least 1 route")
	}
	if routes[0].Provider != "vr" {
		t.Errorf("provider = %q, want vr", routes[0].Provider)
	}
	// The train name comes only from the mock, so a live response cannot pass.
	if len(routes[0].Legs) != 1 || routes[0].Legs[0].Provider != "IC 123" {
		t.Errorf("legs = %+v, want one IC 123 leg from the mock", routes[0].Legs)
	}
}

// ============================================================
// Additional edge case coverage
// ============================================================

// D3: a deadline is a timeout, never "provider does not serve this route".
func TestIsProviderNotApplicable_ContextDeadline(t *testing.T) {
	if isProviderNotApplicable(context.DeadlineExceeded) {
		t.Error("context.DeadlineExceeded must not be not-applicable (it is a timeout)")
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
	if len(result) != 3 {
		t.Errorf("expected 3 routes (zero price kept as unavailable), got %d", len(result))
	}
}

func TestSearchResultBufferCapacity_NonZero(t *testing.T) {
	cap := searchResultBufferCapacity()
	if cap <= 0 {
		t.Errorf("searchResultBufferCapacity() = %d, want > 0", cap)
	}
}
