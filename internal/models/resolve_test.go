package models

import "testing"

func TestResolveFlightSources_CollapsesDuplicate(t *testing.T) {
	leg := func() []FlightLeg {
		return []FlightLeg{{
			AirlineCode:      "AF",
			FlightNumber:     "1234",
			DepartureTime:    "2026-06-01T08:00:00Z",
			DepartureAirport: AirportInfo{Code: "HEL"},
			ArrivalAirport:   AirportInfo{Code: "CDG"},
		}}
	}
	// Same physical flight from two providers at different prices.
	in := []FlightResult{
		{Price: 250, Currency: "EUR", Provider: "google_flights", BookingURL: "g", Legs: leg()},
		{Price: 210, Currency: "EUR", Provider: "kiwi", BookingURL: "k", Legs: leg()},
	}
	out := ResolveFlightSources(in)
	if len(out) != 1 {
		t.Fatalf("expected 1 collapsed flight, got %d", len(out))
	}
	r := out[0]
	if len(r.Sources) != 2 {
		t.Errorf("expected 2 sources, got %d", len(r.Sources))
	}
	if r.Price != 210 || r.CheapestSource != "kiwi" || r.BookingURL != "k" {
		t.Errorf("headline not cheapest: price=%v cheapest=%q url=%q", r.Price, r.CheapestSource, r.BookingURL)
	}
	if r.Savings != 40 {
		t.Errorf("savings = %v, want 40", r.Savings)
	}
}

func TestResolveFlightSources_KeepsDistinct(t *testing.T) {
	in := []FlightResult{
		{Price: 250, Provider: "a", Legs: []FlightLeg{{AirlineCode: "AF", FlightNumber: "1", DepartureTime: "2026-06-01T08:00:00Z"}}},
		{Price: 260, Provider: "b", Legs: []FlightLeg{{AirlineCode: "LH", FlightNumber: "9", DepartureTime: "2026-06-01T09:00:00Z"}}},
	}
	if out := ResolveFlightSources(in); len(out) != 2 {
		t.Fatalf("distinct flights collapsed: got %d want 2", len(out))
	}
}

func TestResolveGroundSources_CollapsesSameTrain(t *testing.T) {
	mk := func(provider string, price float64) GroundRoute {
		return GroundRoute{
			Provider:  provider,
			Type:      "train",
			Price:     price,
			Currency:  "EUR",
			Departure: GroundStop{City: "Paris", Station: "Gare de Lyon", Time: "2026-06-01T10:00:00Z"},
			Arrival:   GroundStop{City: "Lyon", Station: "Part-Dieu", Time: "2026-06-01T12:00:00Z"},
			Legs:      []GroundLeg{{Provider: "sncf", Type: "train"}},
		}
	}
	out := ResolveGroundSources([]GroundRoute{mk("trainline", 89), mk("sncf", 75)})
	if len(out) != 1 {
		t.Fatalf("expected 1 collapsed train, got %d", len(out))
	}
	if out[0].Price != 75 || out[0].CheapestSource != "sncf" || len(out[0].Sources) != 2 {
		t.Errorf("ground collapse wrong: price=%v cheapest=%q sources=%d", out[0].Price, out[0].CheapestSource, len(out[0].Sources))
	}
}

func TestFlightIdentityKey_NoLegsEmpty(t *testing.T) {
	if k := FlightIdentityKey(FlightResult{}); k != "" {
		t.Errorf("no-legs key = %q, want empty (passthrough)", k)
	}
}

func TestResolveFlightSources_NoCrossCurrencyCheapest(t *testing.T) {
	leg := []FlightLeg{{AirlineCode: "AF", FlightNumber: "1234", DepartureTime: "2026-06-01T08:00:00Z"}}
	// Same flight: USD 119 (skiplagged) vs EUR 210 (kiwi). Raw float compare would
	// wrongly call USD 119 cheaper than EUR 210. Currency-aware compare must not.
	in := []FlightResult{
		{Price: 210, Currency: "EUR", Provider: "kiwi", Legs: leg},
		{Price: 119, Currency: "USD", Provider: "skiplagged", Legs: leg},
	}
	out := ResolveFlightSources(in)
	if len(out) != 1 {
		t.Fatalf("want 1 collapsed, got %d", len(out))
	}
	r := out[0]
	// Headline must stay in the comparison currency (EUR, first priced source);
	// the USD source is retained but not used to claim a cheaper price.
	if r.Currency != "EUR" || r.Price != 210 {
		t.Errorf("cross-currency leaked into cheapest: price=%v cur=%s", r.Price, r.Currency)
	}
	if r.Savings != 0 {
		t.Errorf("savings claimed across currencies: %v", r.Savings)
	}
	if len(r.Sources) != 2 {
		t.Errorf("both sources should be retained, got %d", len(r.Sources))
	}
}
