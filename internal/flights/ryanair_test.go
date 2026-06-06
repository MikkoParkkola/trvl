package flights

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

const ryanairFixture = `{"fares":[{"outbound":{
 "departureAirport":{"iataCode":"STN","name":"London Stansted"},
 "arrivalAirport":{"iataCode":"BCN","name":"Barcelona"},
 "departureDate":"2026-07-07T15:10:00",
 "arrivalDate":"2026-07-07T18:25:00",
 "price":{"value":17.99,"currencyCode":"EUR"},
 "flightNumber":"FR9014"
}}]}`

func TestSearchRyanair_MapsFare(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("departureAirportIataCode"); got != "STN" {
			t.Errorf("departure param = %q, want STN", got)
		}
		if got := r.URL.Query().Get("outboundDepartureDateFrom"); got != "2026-07-07" {
			t.Errorf("date param = %q, want 2026-07-07", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(ryanairFixture))
	}))
	defer srv.Close()

	orig := ryanairBaseURL
	ryanairBaseURL = srv.URL
	defer func() { ryanairBaseURL = orig }()

	out, err := SearchRyanair(context.Background(), "STN", "BCN", "2026-07-07", "EUR", SearchOptions{Adults: 1})
	if err != nil {
		t.Fatalf("SearchRyanair error: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("want 1 result, got %d", len(out))
	}
	f := out[0]
	if f.Price != 17.99 || f.Currency != "EUR" || f.Provider != "ryanair" || f.Stops != 0 {
		t.Errorf("bad result: %+v", f)
	}
	if len(f.Legs) != 1 || f.Legs[0].AirlineCode != "FR" || f.Legs[0].FlightNumber != "FR9014" {
		t.Errorf("bad leg: %+v", f.Legs)
	}
	if f.Legs[0].DepartureTime != "2026-07-07T15:10" {
		t.Errorf("departure time = %q, want 2026-07-07T15:10", f.Legs[0].DepartureTime)
	}
	if f.Legs[0].Duration != 195 { // 15:10 -> 18:25 = 3h15m
		t.Errorf("duration = %d, want 195", f.Legs[0].Duration)
	}
	if f.BookingURL == "" {
		t.Error("booking URL not set")
	}
}

func TestRyanairEligibleOptions(t *testing.T) {
	if !ryanairEligibleOptions(SearchOptions{}) {
		t.Error("plain one-way economy should be eligible")
	}
	if ryanairEligibleOptions(SearchOptions{ReturnDate: "2026-07-10"}) {
		t.Error("round-trip should be ineligible")
	}
	if ryanairEligibleOptions(SearchOptions{Alliances: []string{"ONEWORLD"}}) {
		t.Error("alliance filter should be ineligible (Ryanair non-aligned)")
	}
	if ryanairEligibleOptions(SearchOptions{Airlines: []string{"BA"}}) {
		t.Error("non-FR airline filter should be ineligible")
	}
	if !ryanairEligibleOptions(SearchOptions{Airlines: []string{"FR"}}) {
		t.Error("FR airline filter should be eligible")
	}
}
