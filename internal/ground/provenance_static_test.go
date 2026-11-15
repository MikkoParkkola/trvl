package ground

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"golang.org/x/time/rate"
)

// eckeroDefaultHome serves the homepage the Eckerö live flow scrapes first.
func eckeroDefaultHome(w http.ResponseWriter, _ *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "PHPSESSID", Value: "test-session"})
	w.Header().Set("Content-Type", "text/html")
	_, _ = w.Write([]byte(`<html><input name="form_key" type="hidden" value="testkey"/></html>`))
}

// withEckeroServer points the Eckerö client at a test server: home serves "/"
// and dep serves getdepartures.
func withEckeroServer(t *testing.T, home, dep http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/":
			home(w, r)
		case r.URL.Path == "/checkout/bookingBar/getdepartures":
			dep(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	origClient, origLimiter := eckerolineClient, eckerolineLimiter
	eckerolineClient = &http.Client{Transport: &urlRewriter{base: srv.URL}, Timeout: 5 * time.Second}
	eckerolineLimiter = rate.NewLimiter(rate.Limit(1000), 1)
	t.Cleanup(func() {
		srv.Close()
		eckerolineClient, eckerolineLimiter = origClient, origLimiter
	})
}

// assertNotLiveAndEUR checks a static-table producer: its fares are euro
// amounts, so they stay in EUR whatever currency was requested, and nothing
// from a table is ever marked live.
func assertNotLiveAndEUR(t *testing.T, provider string, routes []models.GroundRoute) {
	t.Helper()
	if len(routes) == 0 {
		t.Fatalf("%s: want routes, got none", provider)
	}
	for i, r := range routes {
		if r.Currency != "EUR" {
			t.Errorf("%s route %d currency = %q, want EUR (the table amount is euros)", provider, i, r.Currency)
		}
		if r.PriceProvenance == models.PriceLive {
			t.Errorf("%s route %d marked live, but its price comes from a table", provider, i)
		}
	}
}

// TestSearchStenaLine_StaticFareKeepsEUR pins that Stena Line's euro table
// is not relabelled with the requested currency.
func TestSearchStenaLine_StaticFareKeepsEUR(t *testing.T) {
	orig := stenalineLimiter
	stenalineLimiter = rate.NewLimiter(rate.Limit(1000), 1)
	t.Cleanup(func() { stenalineLimiter = orig })
	routes, err := SearchStenaLine(context.Background(), "Gothenburg", "Kiel", "2026-07-15", "SEK")
	if err != nil {
		t.Fatalf("SearchStenaLine: %v", err)
	}
	assertNotLiveAndEUR(t, "stenaline", routes)
}

// TestSearchEckeroLine_FetchedFareKeepsEUR pins that fares fetched from
// Eckerö (euros) are not relabelled, and that Eckerö is not upgraded to live
// (it is not on PR A's live list and substitutes a base price for a 0 fare).
func TestSearchEckeroLine_FetchedFareKeepsEUR(t *testing.T) {
	withEckeroServer(t, eckeroDefaultHome, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]eckerolineDeparture{{Time: "09:00", Price: 22.5, Ship: "M/S Finlandia"}, {Time: "15:15", Price: 0}})
	})
	routes, err := SearchEckeroLine(context.Background(), "Helsinki", "Tallinn", "2026-07-15", "SEK")
	if err != nil {
		t.Fatalf("SearchEckeroLine: %v", err)
	}
	assertNotLiveAndEUR(t, "eckeroline fetched", routes)
}

// TestSearchEckeroLine_TimetableFareKeepsEUR pins the same for the published
// timetable path, reached when Eckerö validly answers with no departures.
func TestSearchEckeroLine_TimetableFareKeepsEUR(t *testing.T) {
	withEckeroServer(t, eckeroDefaultHome, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	})
	routes, err := SearchEckeroLine(context.Background(), "Helsinki", "Tallinn", "2026-07-15", "SEK")
	if err != nil {
		t.Fatalf("SearchEckeroLine: %v", err)
	}
	assertNotLiveAndEUR(t, "eckeroline timetable", routes)
}

// TestSearchDigitransit_StaticFareKeepsEUR pins that the VR euro fare table
// is not relabelled with the requested currency.
func TestSearchDigitransit_StaticFareKeepsEUR(t *testing.T) {
	day := time.Now().AddDate(0, 1, 0)
	ms := func(h, m int) float64 {
		return float64(time.Date(day.Year(), day.Month(), day.Day(), h, m, 0, 0, time.UTC).UnixMilli())
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"plan": map[string]any{"itineraries": []any{
			map[string]any{
				"duration": 5400, "startTime": ms(8, 0), "endTime": ms(9, 30), "transfers": 0,
				"legs": []any{map[string]any{
					"mode": "RAIL", "startTime": ms(8, 0), "endTime": ms(9, 30), "duration": 5400,
					"from":       map[string]any{"name": "Helsinki", "stop": map[string]any{"code": "HEL"}},
					"to":         map[string]any{"name": "Tampere", "stop": map[string]any{"code": "TRE"}},
					"route":      map[string]any{"shortName": "IC 123", "agency": map[string]any{"name": "VR"}},
					"transitLeg": true,
				}},
			},
		}}}})
	}))
	defer srv.Close()
	origClient, origLimiter := digitransitClient, digitransitLimiter
	digitransitClient = &http.Client{Transport: &redirectTransport{target: srv.URL}, Timeout: 5 * time.Second}
	digitransitLimiter = rate.NewLimiter(rate.Limit(1000), 1)
	t.Cleanup(func() { digitransitClient, digitransitLimiter = origClient, origLimiter })

	routes, err := SearchDigitransit(context.Background(), "Helsinki", "Tampere", day.Format("2006-01-02"), "SEK")
	if err != nil {
		t.Fatalf("SearchDigitransit: %v", err)
	}
	assertNotLiveAndEUR(t, "vr", routes)
}

// TestParseNSTrips_LiveAndTableFares pins both NS price paths: a fare in the
// API answer (cents) is live, the fixed-fare fallback is not, and both stay
// in EUR whatever currency was requested.
func TestParseNSTrips_LiveAndTableFares(t *testing.T) {
	from := nsStation{UIC: "8400058", Name: "Amsterdam Centraal", City: "Amsterdam", Country: "NL"}
	to := nsStation{UIC: "8400530", Name: "Rotterdam Centraal", City: "Rotterdam", Country: "NL"}
	leg := func(dep, arr string) []nsTripLeg {
		return []nsTripLeg{{
			Origin:                   nsStop{Name: "Amsterdam Centraal", UICCode: "8400058"},
			Destination:              nsStop{Name: "Rotterdam Centraal", UICCode: "8400530"},
			TrainCategory:            "Intercity",
			PlannedDepartureDateTime: dep,
			PlannedArrivalDateTime:   arr,
		}}
	}
	trips := []nsTrip{
		{Legs: leg("2026-06-18T08:02:00+0200", "2026-06-18T09:07:00+0200"), OptimalPrice: &nsPrice{TotalPriceInCents: 1590}},
		{Legs: leg("2026-06-18T10:02:00+0200", "2026-06-18T11:07:00+0200")},
	}
	routes := parseNSTrips(trips, from, to, "SEK")
	if len(routes) != 2 {
		t.Fatalf("want 2 routes, got %d", len(routes))
	}
	if routes[0].PriceProvenance != models.PriceLive || routes[0].Currency != "EUR" || routes[0].Price != 15.90 {
		t.Errorf("API-priced trip = %v %s %q, want 15.90 EUR live", routes[0].Price, routes[0].Currency, routes[0].PriceProvenance)
	}
	assertNotLiveAndEUR(t, "ns table fare", routes[1:])
}

// TestEstimateTaxiTransfer_IsEstimated pins that the taxi estimator, which
// never goes through the ground merge, labels its own fare as estimated.
func TestEstimateTaxiTransfer_IsEstimated(t *testing.T) {
	r, err := EstimateTaxiTransfer(context.Background(), TaxiEstimateInput{
		FromName: "Airport", ToName: "Centre",
		FromLat: 60.3172, FromLon: 24.9633, ToLat: 60.1699, ToLon: 24.9384,
		CountryCode: "FI", Currency: "EUR",
	})
	if err != nil {
		t.Fatalf("EstimateTaxiTransfer: %v", err)
	}
	if r.PriceProvenance != models.PriceEstimated {
		t.Errorf("taxi provenance = %q, want estimated", r.PriceProvenance)
	}
}
