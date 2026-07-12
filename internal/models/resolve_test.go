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

// ptrBool and ptrInt are local test helpers for constructing *bool and *int
// fields on FlightResult / GroundRoute (checked bags, seats left, etc.).
func ptrBool(b bool) *bool { return &b }
func ptrInt(i int) *int    { return &i }

func TestResolveFlightSources_Frankenfare_CheaperSourceFareFieldsTravel(t *testing.T) {
	// Common leg identity for all flight cases (same physical itinerary).
	leg := func() []FlightLeg {
		return []FlightLeg{{
			AirlineCode:      "AY",
			FlightNumber:     "101",
			DepartureTime:    "2026-07-15T09:00:00Z",
			DepartureAirport: AirportInfo{Code: "HEL"},
			ArrivalAirport:   AirportInfo{Code: "BCN"},
		}}
	}

	t.Run("bags_cheapest_wins_included_count", func(t *testing.T) {
		// Case 1: Google reports 1 checked bag included; cheaper Kiwi reports 0.
		// Result must carry Kiwi's (cheaper) baggage info, not Google's.
		in := []FlightResult{
			{
				Price: 180, Currency: "EUR", Provider: "google_flights", BookingURL: "g",
				Legs: leg(), CheckedBagsIncluded: ptrInt(1),
			},
			{
				Price: 120, Currency: "EUR", Provider: "kiwi", BookingURL: "k",
				Legs: leg(), CheckedBagsIncluded: ptrInt(0),
			},
		}
		out := ResolveFlightSources(in)
		if len(out) != 1 {
			t.Fatalf("expected 1, got %d", len(out))
		}
		r := out[0]
		if r.Price != 120 || r.Provider != "kiwi" {
			t.Errorf("headline: price=%v provider=%q want 120/kiwi", r.Price, r.Provider)
		}
		if r.CheckedBagsIncluded == nil || *r.CheckedBagsIncluded != 0 {
			got := -999
			if r.CheckedBagsIncluded != nil {
				got = *r.CheckedBagsIncluded
			}
			t.Errorf("CheckedBagsIncluded = %d, want deref 0 (not 1 from expensive source)", got)
		}
		if len(r.Sources) != 2 {
			t.Errorf("len(Sources)=%d want 2", len(r.Sources))
		}
	})

	t.Run("self_connect_cheapest_wins", func(t *testing.T) {
		// Case 2: cheaper source is a self-connect; must propagate true.
		in := []FlightResult{
			{Price: 220, Currency: "EUR", Provider: "a", Legs: leg(), SelfConnect: false},
			{Price: 140, Currency: "EUR", Provider: "b", Legs: leg(), SelfConnect: true},
		}
		r := ResolveFlightSources(in)[0]
		if r.Price != 140 || !r.SelfConnect {
			t.Errorf("Price=%v SelfConnect=%v want 140/true", r.Price, r.SelfConnect)
		}
	})

	t.Run("faretype_and_warnings_travel_with_cheapest", func(t *testing.T) {
		// Case 3: split tickets warning and FareSplitTickets must come from cheaper B.
		in := []FlightResult{
			{Price: 200, Currency: "EUR", Provider: "a", Legs: leg(), FareType: FareRoundTrip},
			{Price: 160, Currency: "EUR", Provider: "b", Legs: leg(), FareType: FareSplitTickets, Warnings: []string{"separate tickets"}},
		}
		r := ResolveFlightSources(in)[0]
		if r.Price != 160 || r.FareType != FareSplitTickets {
			t.Errorf("Price=%v FareType=%q want 160/%q", r.Price, r.FareType, FareSplitTickets)
		}
		if len(r.Warnings) != 1 || r.Warnings[0] != "separate tickets" {
			t.Errorf("Warnings=%v want [\"separate tickets\"]", r.Warnings)
		}
	})

	t.Run("comparable_price_follows_cheapest_source", func(t *testing.T) {
		// Case 4: ComparablePrice (the adjusted economics) must be taken from the cheapest src.
		in := []FlightResult{
			{Price: 150, Currency: "EUR", Provider: "a", Legs: leg(), ComparablePrice: 150},
			{Price: 100, Currency: "EUR", Provider: "b", Legs: leg(), ComparablePrice: 170},
		}
		r := ResolveFlightSources(in)[0]
		if r.Price != 100 || r.ComparablePrice != 170 {
			t.Errorf("Price=%v ComparablePrice=%v want 100/170", r.Price, r.ComparablePrice)
		}
	})

	t.Run("confidence_nil_on_cheapest_overwrites_prior_high", func(t *testing.T) {
		// Case 5: when cheapest has nil Confidence, result must have nil (do not keep A's high).
		high := &Confidence{Rated: true, Score: 0.92, Label: ConfidenceHigh, Basis: "test-high"}
		in := []FlightResult{
			{Price: 190, Currency: "EUR", Provider: "a", Legs: leg(), Confidence: high},
			{Price: 130, Currency: "EUR", Provider: "b", Legs: leg(), Confidence: nil},
		}
		r := ResolveFlightSources(in)[0]
		if r.Price != 130 || r.Confidence != nil {
			t.Errorf("Price=%v Confidence=%#v want 130/nil", r.Price, r.Confidence)
		}
	})

	t.Run("order_independent_cheapest_is_third", func(t *testing.T) {
		// Case 6: cheapest folded last (or in middle); order must not matter.
		base := leg()
		a := FlightResult{Price: 210, Currency: "EUR", Provider: "a", BookingURL: "a", Legs: base, CheckedBagsIncluded: ptrInt(1)}
		c := FlightResult{Price: 170, Currency: "EUR", Provider: "c", BookingURL: "c", Legs: base, CheckedBagsIncluded: ptrInt(2)}
		b := FlightResult{Price: 150, Currency: "EUR", Provider: "b", BookingURL: "b", Legs: base, CheckedBagsIncluded: ptrInt(0)}
		for _, order := range [][]FlightResult{{a, c, b}, {c, a, b}, {b, a, c}} {
			out := ResolveFlightSources(order)
			r := out[0]
			if r.Price != 150 || r.Provider != "b" {
				t.Errorf("in order %v: Price=%v Provider=%q want 150/b", providers(order), r.Price, r.Provider)
				continue
			}
			if r.CheckedBagsIncluded == nil || *r.CheckedBagsIncluded != 0 {
				t.Errorf("in order %v: CheckedBagsIncluded wrong", providers(order))
			}
			if len(r.Sources) != 3 {
				t.Errorf("in order %v: len(Sources)=%d want 3", providers(order), len(r.Sources))
			}
		}
	})
}

func providers(fs []FlightResult) []string {
	ps := make([]string, len(fs))
	for i, f := range fs {
		ps[i] = f.Provider
	}
	return ps
}

func TestResolveGroundSources_Frankenfare_CheaperSourceFieldsTravel(t *testing.T) {
	// Identity maker for ground: same type+stops+times+leg providers.
	mk := func(provider string, price float64, amenities []string, seats *int, priceMax float64) GroundRoute {
		return GroundRoute{
			Provider:  provider,
			Type:      "bus",
			Price:     price,
			PriceMax:  priceMax,
			Currency:  "EUR",
			Departure: GroundStop{City: "AMS", Station: "Amsterdam", Time: "2026-07-20T10:00:00Z"},
			Arrival:   GroundStop{City: "BRU", Station: "Brussels", Time: "2026-07-20T13:30:00Z"},
			Legs:      []GroundLeg{{Provider: "flix", Type: "bus"}},
			Amenities: amenities,
			SeatsLeft: seats,
		}
	}

	t.Run("amenities_and_seats_follow_cheapest", func(t *testing.T) {
		// Case 7 (ground mirror of bags): A has Amenities, B cheaper has nil Amenities + SeatsLeft=3.
		// Expect cheapest price + B's offer fields (Amenities==nil, SeatsLeft=3).
		a := mk("a", 30, []string{"wifi", "usb"}, nil, 0)
		b := mk("b", 20, nil, ptrInt(3), 25)
		out := ResolveGroundSources([]GroundRoute{a, b})
		if len(out) != 1 {
			t.Fatalf("expected 1, got %d", len(out))
		}
		r := out[0]
		if r.Price != 20 || r.Provider != "b" {
			t.Errorf("headline: price=%v provider=%q want 20/b", r.Price, r.Provider)
		}
		if r.Amenities != nil {
			t.Errorf("Amenities=%v want nil (cheaper had none)", r.Amenities)
		}
		if r.SeatsLeft == nil || *r.SeatsLeft != 3 {
			got := -999
			if r.SeatsLeft != nil {
				got = *r.SeatsLeft
			}
			t.Errorf("SeatsLeft = %d, want deref 3", got)
		}
		if len(r.Sources) != 2 {
			t.Errorf("len(Sources)=%d want 2", len(r.Sources))
		}
		// PriceMax belongs to the cheapest provider too.
		if r.PriceMax != 25 {
			t.Errorf("PriceMax=%v want 25 (from cheapest)", r.PriceMax)
		}
	})
}
