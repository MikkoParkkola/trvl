package ground

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestFetchDBBestPrice_MockHappyPath(t *testing.T) {
	origClient := dbClient
	origLimiter := dbLimiter
	t.Cleanup(func() {
		dbClient = origClient
		dbLimiter = origLimiter
	})
	dbLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(dbBestPriceResponse{
			TagesbestPreisIntervalle: []dbBestPriceInterval{
				{AngebotsPreis: &dbPreis{Betrag: 29.90, Waehrung: "eur"}},
				{AngebotsPreis: &dbPreis{Betrag: 39.90, Waehrung: "eur"}},
			},
		})
	}))
	defer srv.Close()

	dbClient = &http.Client{
		Transport: &redirectTransport{target: srv.URL},
		Timeout:   5 * time.Second,
	}

	price, cur := fetchDBBestPrice(context.Background(), "8011160", "8010205", "2026-07-01")
	if price != 29.90 {
		t.Errorf("price = %v, want 29.90", price)
	}
	if cur != "EUR" {
		t.Errorf("currency = %q, want EUR", cur)
	}
}

func TestFetchDBBestPrice_MockNoPrices(t *testing.T) {
	origClient := dbClient
	origLimiter := dbLimiter
	t.Cleanup(func() {
		dbClient = origClient
		dbLimiter = origLimiter
	})
	dbLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(dbBestPriceResponse{
			TagesbestPreisIntervalle: []dbBestPriceInterval{
				{AngebotsPreis: nil},
			},
		})
	}))
	defer srv.Close()

	dbClient = &http.Client{
		Transport: &redirectTransport{target: srv.URL},
		Timeout:   5 * time.Second,
	}

	price, cur := fetchDBBestPrice(context.Background(), "8011160", "8010205", "2026-07-01")
	if price != 0 {
		t.Errorf("price = %v, want 0", price)
	}
	if cur != "" {
		t.Errorf("currency = %q, want empty", cur)
	}
}

func TestFetchDBBestPrice_MockHTTPError(t *testing.T) {
	origClient := dbClient
	origLimiter := dbLimiter
	t.Cleanup(func() {
		dbClient = origClient
		dbLimiter = origLimiter
	})
	dbLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	dbClient = &http.Client{
		Transport: &redirectTransport{target: srv.URL},
		Timeout:   5 * time.Second,
	}

	price, _ := fetchDBBestPrice(context.Background(), "8011160", "8010205", "2026-07-01")
	if price != 0 {
		t.Errorf("price = %v, want 0 for HTTP error", price)
	}
}

// ============================================================
// SearchDeutscheBahn via httptest (was 0%)
// ============================================================

func TestSearchDeutscheBahn_MockHappyPath(t *testing.T) {
	origClient := dbClient
	origLimiter := dbLimiter
	t.Cleanup(func() {
		dbClient = origClient
		dbLimiter = origLimiter
	})
	dbLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(dbJourneysResponse{
			Verbindungen: []dbVerbindung{
				{
					VerbindungsAbschnitte: []dbAbschnitt{
						{
							AbgangsDatum:   "2026-07-01T08:00:00",
							AnkunftsDatum:  "2026-07-01T12:30:00",
							ProduktGattung: "ICE",
							Kurztext:       "ICE 123",
						},
					},
					AngebotsPreis: &dbPreis{Betrag: 45.00, Waehrung: "EUR"},
				},
			},
		})
	}))
	defer srv.Close()

	dbClient = &http.Client{
		Transport: &redirectTransport{target: srv.URL},
		Timeout:   5 * time.Second,
	}

	routes, err := SearchDeutscheBahn(context.Background(), "Berlin", "Munich", "2026-07-01", "EUR")
	if err != nil {
		t.Fatalf("SearchDeutscheBahn: %v", err)
	}
	if len(routes) == 0 {
		t.Fatal("expected at least 1 route")
	}
	r := routes[0]
	if r.Provider != "db" {
		t.Errorf("provider = %q, want db", r.Provider)
	}
	if r.Type != "train" {
		t.Errorf("type = %q, want train", r.Type)
	}
	if r.Price != 45.00 {
		t.Errorf("price = %v, want 45.00", r.Price)
	}
}

func TestSearchDeutscheBahn_MockInvalidDate(t *testing.T) {
	_, err := SearchDeutscheBahn(context.Background(), "Berlin", "Munich", "not-a-date", "EUR")
	if err == nil {
		t.Fatal("expected error for invalid date")
	}
}

func TestSearchDeutscheBahn_MockUnknownCity(t *testing.T) {
	_, err := SearchDeutscheBahn(context.Background(), "NoSuchCityXYZ", "Munich", "2026-07-01", "EUR")
	if err == nil {
		t.Fatal("expected error for unknown city")
	}
}

func TestSearchDeutscheBahn_MockAPIError(t *testing.T) {
	origClient := dbClient
	origLimiter := dbLimiter
	t.Cleanup(func() {
		dbClient = origClient
		dbLimiter = origLimiter
	})
	dbLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(dbJourneysResponse{
			FehlerNachricht: &dbError{
				Code:         "SERVICE_UNAVAILABLE",
				Ueberschrift: "Service temporarily unavailable",
			},
		})
	}))
	defer srv.Close()

	dbClient = &http.Client{
		Transport: &redirectTransport{target: srv.URL},
		Timeout:   5 * time.Second,
	}

	_, err := SearchDeutscheBahn(context.Background(), "Berlin", "Munich", "2026-07-01", "EUR")
	if err == nil {
		t.Fatal("expected error for API error response")
	}
	if !strings.Contains(err.Error(), "SERVICE_UNAVAILABLE") {
		t.Errorf("error should mention SERVICE_UNAVAILABLE, got: %v", err)
	}
}

func TestSearchDeutscheBahn_MockDefaultCurrency(t *testing.T) {
	origClient := dbClient
	origLimiter := dbLimiter
	t.Cleanup(func() {
		dbClient = origClient
		dbLimiter = origLimiter
	})
	dbLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(dbJourneysResponse{
			Verbindungen: []dbVerbindung{},
		})
	}))
	defer srv.Close()

	dbClient = &http.Client{
		Transport: &redirectTransport{target: srv.URL},
		Timeout:   5 * time.Second,
	}

	// Empty currency should default to EUR without error.
	routes, err := SearchDeutscheBahn(context.Background(), "Berlin", "Munich", "2026-07-01", "")
	if err != nil {
		t.Fatalf("SearchDeutscheBahn: %v", err)
	}
	_ = routes
}

func TestSearchDeutscheBahn_MockHTTP500(t *testing.T) {
	origClient := dbClient
	origLimiter := dbLimiter
	t.Cleanup(func() {
		dbClient = origClient
		dbLimiter = origLimiter
	})
	dbLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("server error"))
	}))
	defer srv.Close()

	dbClient = &http.Client{
		Transport: &redirectTransport{target: srv.URL},
		Timeout:   5 * time.Second,
	}

	_, err := SearchDeutscheBahn(context.Background(), "Berlin", "Munich", "2026-07-01", "EUR")
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
}

// ============================================================
// fetchDFDSAvailability via httptest — additional cases (was 0%)
// ============================================================

func TestFetchDFDSAvailability_MockDateDisabled(t *testing.T) {
	origClient := dfdsClient
	t.Cleanup(func() { dfdsClient = origClient })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := dfdsAvailabilityResponse{
			DisabledDates: []string{"2026-07-01"},
		}
		resp.Dates.FromDate = "2026-06-01"
		resp.Dates.ToDate = "2026-09-30"
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	dfdsClient = &http.Client{
		Transport: &redirectTransport{target: srv.URL},
		Timeout:   5 * time.Second,
	}

	routeInfo := dfdsRouteInfo{RouteCode: "CPHO-OSFO", SalesOwner: 19}
	avail, isOffer, err := fetchDFDSAvailability(context.Background(), routeInfo, "2026-07-01")
	if err != nil {
		t.Fatalf("fetchDFDSAvailability: %v", err)
	}
	if avail {
		t.Error("expected unavailable for disabled date")
	}
	if isOffer {
		t.Error("expected not offer for disabled date")
	}
}

func TestFetchDFDSAvailability_MockDateBeforeRange(t *testing.T) {
	origClient := dfdsClient
	t.Cleanup(func() { dfdsClient = origClient })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := dfdsAvailabilityResponse{}
		resp.Dates.FromDate = "2026-06-01"
		resp.Dates.ToDate = "2026-09-30"
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	dfdsClient = &http.Client{
		Transport: &redirectTransport{target: srv.URL},
		Timeout:   5 * time.Second,
	}

	routeInfo := dfdsRouteInfo{RouteCode: "CPHO-OSFO", SalesOwner: 19}
	avail, _, err := fetchDFDSAvailability(context.Background(), routeInfo, "2026-05-01")
	if err != nil {
		t.Fatalf("fetchDFDSAvailability: %v", err)
	}
	if avail {
		t.Error("expected unavailable for date before range")
	}
}

func TestFetchDFDSAvailability_MockDateAfterRange(t *testing.T) {
	origClient := dfdsClient
	t.Cleanup(func() { dfdsClient = origClient })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := dfdsAvailabilityResponse{}
		resp.Dates.FromDate = "2026-06-01"
		resp.Dates.ToDate = "2026-09-30"
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	dfdsClient = &http.Client{
		Transport: &redirectTransport{target: srv.URL},
		Timeout:   5 * time.Second,
	}

	routeInfo := dfdsRouteInfo{RouteCode: "CPHO-OSFO", SalesOwner: 19}
	avail, _, err := fetchDFDSAvailability(context.Background(), routeInfo, "2026-10-15")
	if err != nil {
		t.Fatalf("fetchDFDSAvailability: %v", err)
	}
	if avail {
		t.Error("expected unavailable for date after range")
	}
}

func TestFetchDFDSAvailability_MockInactive(t *testing.T) {
	origClient := dfdsClient
	t.Cleanup(func() { dfdsClient = origClient })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := dfdsAvailabilityResponse{}
		resp.Dates.FromDate = ""
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	dfdsClient = &http.Client{
		Transport: &redirectTransport{target: srv.URL},
		Timeout:   5 * time.Second,
	}

	routeInfo := dfdsRouteInfo{RouteCode: "CPHO-OSFO", SalesOwner: 19}
	avail, _, err := fetchDFDSAvailability(context.Background(), routeInfo, "2026-07-01")
	if err != nil {
		t.Fatalf("fetchDFDSAvailability: %v", err)
	}
	if avail {
		t.Error("expected unavailable for inactive route")
	}
}

func TestFetchDFDSAvailability_MockDecodeError(t *testing.T) {
	origClient := dfdsClient
	t.Cleanup(func() { dfdsClient = origClient })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	dfdsClient = &http.Client{
		Transport: &redirectTransport{target: srv.URL},
		Timeout:   5 * time.Second,
	}

	routeInfo := dfdsRouteInfo{RouteCode: "CPHO-OSFO", SalesOwner: 19}
	avail, _, err := fetchDFDSAvailability(context.Background(), routeInfo, "2026-07-01")
	if err != nil {
		t.Fatalf("expected nil error (non-fatal), got: %v", err)
	}
	// Decode error is non-fatal; returns available=true.
	if !avail {
		t.Error("expected available=true on decode error (non-fatal)")
	}
}

// ============================================================
// fetchFinnlinesTimetables via httptest (was 0%)
// ============================================================

func TestFetchFinnlinesTimetables_MockHappyPath(t *testing.T) {
	origClient := finnlinesClient
	origLimiter := finnlinesLimiter
	t.Cleanup(func() {
		finnlinesClient = origClient
		finnlinesLimiter = origLimiter
	})
	finnlinesLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		charge := 8500
		_ = json.NewEncoder(w).Encode(finnlinesGraphQLResponse{
			Data: struct {
				ListTimeTableAvailability []finnlinesTimetableEntry `json:"listTimeTableAvailability"`
			}{
				ListTimeTableAvailability: []finnlinesTimetableEntry{
					{
						SailingCode:   "HT-2026-07-01",
						DepartureDate: "2026-07-01",
						DepartureTime: "17:00",
						ArrivalDate:   "2026-07-02",
						ArrivalTime:   "21:00",
						DeparturePort: "FIHEL",
						ArrivalPort:   "DETRV",
						IsAvailable:   true,
						ShipName:      "Finnmaid",
						CrossingTime:  "28:00",
						ChargeTotal:   &charge,
					},
				},
			},
		})
	}))
	defer srv.Close()

	finnlinesClient = &http.Client{
		Transport: &redirectTransport{target: srv.URL},
		Timeout:   5 * time.Second,
	}

	entries, err := fetchFinnlinesTimetables(context.Background(), "FIHEL", "DETRV", "2026-07-01")
	if err != nil {
		t.Fatalf("fetchFinnlinesTimetables: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].ShipName != "Finnmaid" {
		t.Errorf("ship = %q, want Finnmaid", entries[0].ShipName)
	}
}

func TestFetchFinnlinesTimetables_MockGraphQLError(t *testing.T) {
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
			"errors": []map[string]string{
				{"message": "Validation error"},
			},
		})
	}))
	defer srv.Close()

	finnlinesClient = &http.Client{
		Transport: &redirectTransport{target: srv.URL},
		Timeout:   5 * time.Second,
	}

	_, err := fetchFinnlinesTimetables(context.Background(), "FIHEL", "DETRV", "2026-07-01")
	if err == nil {
		t.Fatal("expected error for GraphQL error response")
	}
}
