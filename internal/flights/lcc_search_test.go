package flights

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// fakeLeg builds a minimal one-way FlightResult for the given route and price.
func fakeLeg(origin, dest string, price float64) models.FlightResult {
	return models.FlightResult{
		Price:    price,
		Currency: "EUR",
		Duration: 120,
		Stops:    0,
		Provider: "FakeLCC",
		Legs: []models.FlightLeg{{
			DepartureAirport: models.AirportInfo{Code: origin},
			ArrivalAirport:   models.AirportInfo{Code: dest},
		}},
	}
}

// TestSearchSingleLCC_OneWay verifies a one-way request wraps the single-leg
// results directly with trip type one_way and no composition.
func TestSearchSingleLCC_OneWay(t *testing.T) {
	search := func(_ context.Context, origin, dest, _, _ string, _ SearchOptions) ([]models.FlightResult, error) {
		return []models.FlightResult{fakeLeg(origin, dest, 50)}, nil
	}

	res, err := searchSingleLCC(context.Background(), "Ryanair", search, "STN", "DUB", "2026-07-01", SearchOptions{})
	if err != nil {
		t.Fatalf("searchSingleLCC one-way: %v", err)
	}
	if res.TripType != "one_way" {
		t.Errorf("trip type: want one_way, got %q", res.TripType)
	}
	if len(res.Flights) != 1 {
		t.Fatalf("expected 1 one-way flight, got %d", len(res.Flights))
	}
	if res.Flights[0].FareType != "" {
		t.Errorf("one-way fare type should be the empty default, got %q", res.Flights[0].FareType)
	}
}

// TestSearchSingleLCC_RoundTripComposesSplitTickets is the core guarantee: a
// round-trip request to a single low-cost carrier returns genuine return-ticket
// data — an outbound + inbound pair composed into a FareSplitTickets itinerary
// with both legs Direction-tagged — never a bare one-way.
func TestSearchSingleLCC_RoundTripComposesSplitTickets(t *testing.T) {
	// Return distinct prices per direction so we can assert the summed total.
	search := func(_ context.Context, origin, dest, _, _ string, _ SearchOptions) ([]models.FlightResult, error) {
		if origin == "STN" {
			return []models.FlightResult{fakeLeg(origin, dest, 40)}, nil // outbound
		}
		return []models.FlightResult{fakeLeg(origin, dest, 35)}, nil // inbound
	}

	opts := SearchOptions{ReturnDate: "2026-07-08"}
	res, err := searchSingleLCC(context.Background(), "Ryanair", search, "STN", "DUB", "2026-07-01", opts)
	if err != nil {
		t.Fatalf("searchSingleLCC round-trip: %v", err)
	}
	if res.TripType != "round_trip" {
		t.Errorf("trip type: want round_trip, got %q", res.TripType)
	}
	if len(res.Flights) == 0 {
		t.Fatal("expected at least one composed round-trip itinerary")
	}

	rt := res.Flights[0]
	if rt.FareType != models.FareSplitTickets {
		t.Errorf("composed fare type: want %q, got %q", models.FareSplitTickets, rt.FareType)
	}
	if rt.Price != 75 {
		t.Errorf("composed price: want 40+35=75, got %v", rt.Price)
	}
	if len(rt.Legs) != 2 {
		t.Fatalf("expected 2 legs (outbound + inbound), got %d", len(rt.Legs))
	}
	if rt.Legs[0].Direction != "outbound" {
		t.Errorf("leg 0 direction: want outbound, got %q", rt.Legs[0].Direction)
	}
	if rt.Legs[1].Direction != "inbound" {
		t.Errorf("leg 1 direction: want inbound, got %q", rt.Legs[1].Direction)
	}
	// The split-ticket warning must be present so the summed price is never
	// mistaken for a single bookable fare.
	var warned bool
	for _, w := range rt.Warnings {
		if strings.Contains(w, "two separate one-way tickets") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("composed round-trip missing split-ticket warning; got %v", rt.Warnings)
	}
}

// TestSearchSingleLCC_ErrorSurfacesProviderName verifies an explicit provider
// request surfaces the carrier's error (with its name) rather than swallowing it
// into an empty result — the honesty contract for explicit selection.
func TestSearchSingleLCC_ErrorSurfacesProviderName(t *testing.T) {
	search := func(_ context.Context, _, _, _, _ string, _ SearchOptions) ([]models.FlightResult, error) {
		return nil, errors.New("API key not configured")
	}

	_, err := searchSingleLCC(context.Background(), "Transavia", search, "AMS", "BCN", "2026-07-01", SearchOptions{})
	if err == nil {
		t.Fatal("expected an error to surface from the carrier search")
	}
	if !strings.Contains(err.Error(), "Transavia") {
		t.Errorf("error should name the provider; got: %v", err)
	}
}

// TestSearchSingleLCC_RoundTripBothLegsAcrossCarriers proves the round-trip
// both-legs guarantee holds for EVERY low-cost carrier exposed via the CLI
// `--provider` switch — including Vueling and Norwegian, which were registered
// and composition-capable but previously unreachable from the CLI. A round-trip
// request must return a FareSplitTickets itinerary carrying a real outbound AND
// a real inbound leg (Direction-tagged), never a bare one-way.
func TestSearchSingleLCC_RoundTripBothLegsAcrossCarriers(t *testing.T) {
	for _, name := range []string{"Ryanair", "Wizz Air", "Transavia", "easyJet", "Vueling", "Norwegian"} {
		t.Run(name, func(t *testing.T) {
			search := func(_ context.Context, origin, dest, _, _ string, _ SearchOptions) ([]models.FlightResult, error) {
				return []models.FlightResult{fakeLeg(origin, dest, 60)}, nil
			}
			opts := SearchOptions{ReturnDate: "2026-07-08"}
			res, err := searchSingleLCC(context.Background(), name, search, "BCN", "FCO", "2026-07-01", opts)
			if err != nil {
				t.Fatalf("%s round-trip: %v", name, err)
			}
			if res.TripType != "round_trip" {
				t.Fatalf("%s trip type: want round_trip, got %q", name, res.TripType)
			}
			if len(res.Flights) == 0 {
				t.Fatalf("%s: expected a composed round-trip itinerary", name)
			}
			rt := res.Flights[0]
			if rt.FareType != models.FareSplitTickets {
				t.Errorf("%s fare type: want %q, got %q", name, models.FareSplitTickets, rt.FareType)
			}
			if len(rt.Legs) != 2 {
				t.Fatalf("%s: expected 2 legs (outbound + inbound), got %d", name, len(rt.Legs))
			}
			if rt.Legs[0].Direction != "outbound" || rt.Legs[1].Direction != "inbound" {
				t.Errorf("%s leg directions: want outbound/inbound, got %q/%q", name, rt.Legs[0].Direction, rt.Legs[1].Direction)
			}
		})
	}
}

// TestSearchLowCostCarrier_VuelingNorwegianRoutable guards that the dispatcher
// (the public entry the CLI calls) recognises Vueling/Norwegian and their
// aliases, rather than rejecting them. It asserts routing, not a network result:
// the real carrier searchers are opt-in and error honestly when unconfigured,
// so we accept either composed results or a provider-named error — but never the
// "unrecognised provider" rejection that the missing CLI wiring produced.
func TestSearchLowCostCarrier_VuelingNorwegianRoutable(t *testing.T) {
	for _, name := range []string{"vueling", "vy", "norwegian", "dy"} {
		t.Run(name, func(t *testing.T) {
			_, err := SearchLowCostCarrier(context.Background(), name, "BCN", "FCO", "2026-07-01", SearchOptions{ReturnDate: "2026-07-08"})
			if err != nil && strings.Contains(err.Error(), "unrecognised low-cost carrier") {
				t.Fatalf("%s should be a recognised provider, got: %v", name, err)
			}
		})
	}
}

// TestSearchLowCostCarrier_UnrecognisedProvider guards the public dispatcher.
func TestSearchLowCostCarrier_UnrecognisedProvider(t *testing.T) {
	_, err := SearchLowCostCarrier(context.Background(), "spirit", "JFK", "LAX", "2026-07-01", SearchOptions{})
	if err == nil {
		t.Fatal("expected an error for an unrecognised provider")
	}
}

// TestLCCRegistryCoversCLIProviders ensures every low-cost carrier the CLI
// dispatches is registered, so a new CLI case can never reference a missing
// searcher.
func TestLCCRegistryCoversCLIProviders(t *testing.T) {
	for _, name := range []string{"ryanair", "wizzair", "wizz", "transavia", "easyjet", "vueling", "vy", "norwegian", "dy"} {
		if _, ok := lccRegistry[name]; !ok {
			t.Errorf("lccRegistry missing CLI provider %q", name)
		}
	}
}
