package flights

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSearchTransavia_NoKeyIsNoOp(t *testing.T) {
	t.Setenv("TRANSAVIA_API_KEY", "")
	out, err := SearchTransavia(context.Background(), "AMS", "BCN", "2026-07-07", "EUR", SearchOptions{Adults: 1})
	if err != nil {
		t.Fatalf("unexpected error with no key: %v", err)
	}
	if out != nil {
		t.Errorf("want nil results with no key (opt-in skip), got %d", len(out))
	}
}

func TestSearchTransavia_MapsOffers(t *testing.T) {
	fixture := loadFixture(t, "transavia_offers.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("apikey"); got != "test-key" {
			t.Errorf("apikey header = %q, want test-key", got)
		}
		if got := r.URL.Query().Get("origin"); got != "AMS" {
			t.Errorf("origin param = %q, want AMS", got)
		}
		if got := r.URL.Query().Get("originDepartureDate"); got != "20260707" {
			t.Errorf("date param = %q, want 20260707", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()

	t.Setenv("TRANSAVIA_API_KEY", "test-key")
	orig := transaviaHost
	transaviaHost = srv.URL
	defer func() { transaviaHost = orig }()

	out, err := SearchTransavia(context.Background(), "AMS", "BCN", "2026-07-07", "EUR", SearchOptions{Adults: 1})
	if err != nil {
		t.Fatalf("SearchTransavia error: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("want 2 results, got %d", len(out))
	}

	// First offer: pricingInfoSummary path, outboundFlights array.
	f0 := out[0]
	if f0.Price != 59.0 || f0.Currency != "EUR" || f0.Provider != "transavia" {
		t.Errorf("offer 0 bad: %+v", f0)
	}
	if len(f0.Legs) != 1 {
		t.Fatalf("offer 0 want 1 leg, got %d", len(f0.Legs))
	}
	leg := f0.Legs[0]
	if leg.AirlineCode != "HV" || leg.Airline != "Transavia" || leg.FlightNumber != "HV6051" {
		t.Errorf("offer 0 leg airline/flight: %+v", leg)
	}
	if leg.Aircraft != "Boeing 737-800" {
		t.Errorf("offer 0 aircraft = %q", leg.Aircraft)
	}
	if leg.DepartureTime != "2026-07-07T07:20" || leg.ArrivalTime != "2026-07-07T09:35" {
		t.Errorf("offer 0 times: dep=%q arr=%q", leg.DepartureTime, leg.ArrivalTime)
	}
	if leg.Duration != 135 { // 07:20 -> 09:35 = 2h15m
		t.Errorf("offer 0 duration = %d, want 135", leg.Duration)
	}

	// Second offer: price{} path, singular outboundFlight.
	f1 := out[1]
	if f1.Price != 42.5 || f1.Currency != "EUR" {
		t.Errorf("offer 1 bad price/currency: %+v", f1)
	}
	if len(f1.Legs) != 1 || f1.Legs[0].FlightNumber != "HV5022" {
		t.Errorf("offer 1 leg: %+v", f1.Legs)
	}
	if f1.BookingURL == "" {
		t.Error("offer 1 booking URL not set")
	}
}

func TestSearchTransavia_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	t.Setenv("TRANSAVIA_API_KEY", "bad-key")
	orig := transaviaHost
	transaviaHost = srv.URL
	defer func() { transaviaHost = orig }()

	_, err := SearchTransavia(context.Background(), "AMS", "BCN", "2026-07-07", "EUR", SearchOptions{Adults: 1})
	if err == nil {
		t.Fatal("want error on 401, got nil")
	}
}

func TestTransaviaConfigured(t *testing.T) {
	t.Setenv("TRANSAVIA_API_KEY", "")
	if transaviaConfigured() {
		t.Error("should be unconfigured with empty key")
	}
	t.Setenv("TRANSAVIA_API_KEY", "x")
	if !transaviaConfigured() {
		t.Error("should be configured with key set")
	}
}

func TestTransaviaEligibleOptions(t *testing.T) {
	if !transaviaEligibleOptions(SearchOptions{}) {
		t.Error("plain one-way should be eligible")
	}
	if transaviaEligibleOptions(SearchOptions{ReturnDate: "2026-07-10"}) {
		t.Error("round-trip should be ineligible")
	}
	if transaviaEligibleOptions(SearchOptions{Alliances: []string{"SKYTEAM"}}) {
		t.Error("alliance filter should be ineligible")
	}
	if transaviaEligibleOptions(SearchOptions{Airlines: []string{"BA"}}) {
		t.Error("non-HV/TO airline filter should be ineligible")
	}
	if !transaviaEligibleOptions(SearchOptions{Airlines: []string{"TO"}}) {
		t.Error("TO airline filter should be eligible")
	}
}
