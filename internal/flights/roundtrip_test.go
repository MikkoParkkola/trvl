package flights

import (
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

func owFlight(provider, currency string, price float64, dep, arr string) models.FlightResult {
	return models.FlightResult{
		Price:    price,
		Currency: currency,
		Duration: 120,
		Stops:    0,
		Provider: provider,
		Legs: []models.FlightLeg{{
			DepartureAirport: models.AirportInfo{Code: dep},
			ArrivalAirport:   models.AirportInfo{Code: arr},
			DepartureTime:    "2026-07-01T08:00",
			ArrivalTime:      "2026-07-01T10:00",
		}},
	}
}

func TestComposeRoundTrips_SumsAndConcatenates(t *testing.T) {
	out := []models.FlightResult{owFlight("Google Flights", "EUR", 100, "HEL", "BCN")}
	in := []models.FlightResult{owFlight("Ryanair", "EUR", 60, "BCN", "HEL")}

	composed, truncated := composeRoundTrips(out, in, SearchOptions{})
	if truncated {
		t.Fatalf("did not expect truncation for 1x1 pairing")
	}
	if len(composed) != 1 {
		t.Fatalf("composed count: got %d, want 1", len(composed))
	}
	rt := composed[0]
	if rt.Price != 160 {
		t.Errorf("price: got %v, want 160 (100+60)", rt.Price)
	}
	if rt.Duration != 240 {
		t.Errorf("duration: got %d, want 240", rt.Duration)
	}
	if len(rt.Legs) != 2 {
		t.Fatalf("legs: got %d, want 2 (outbound + inbound)", len(rt.Legs))
	}
	if rt.Legs[0].DepartureAirport.Code != "HEL" || rt.Legs[1].DepartureAirport.Code != "BCN" {
		t.Errorf("leg order wrong: %q then %q", rt.Legs[0].DepartureAirport.Code, rt.Legs[1].DepartureAirport.Code)
	}
	if !strings.Contains(rt.Provider, "Google Flights") || !strings.Contains(rt.Provider, "Ryanair") {
		t.Errorf("provider label missing source providers: %q", rt.Provider)
	}
	if len(rt.Warnings) == 0 || !strings.Contains(rt.Warnings[0], "two separate one-way tickets") {
		t.Errorf("expected separate-tickets warning, got %v", rt.Warnings)
	}
}

func TestComposeRoundTrips_CheapestTotalFirst(t *testing.T) {
	out := []models.FlightResult{
		owFlight("A", "EUR", 100, "HEL", "BCN"),
		owFlight("B", "EUR", 200, "HEL", "BCN"),
	}
	in := []models.FlightResult{
		owFlight("C", "EUR", 50, "BCN", "HEL"),
		owFlight("D", "EUR", 70, "BCN", "HEL"),
	}
	composed, _ := composeRoundTrips(out, in, SearchOptions{})
	if len(composed) != 4 {
		t.Fatalf("composed count: got %d, want 4 (2x2)", len(composed))
	}
	if composed[0].Price != 150 {
		t.Errorf("cheapest total: got %v, want 150 (100+50)", composed[0].Price)
	}
	// Verify ascending order.
	for i := 1; i < len(composed); i++ {
		if composed[i].Price < composed[i-1].Price {
			t.Errorf("not sorted ascending at %d: %v < %v", i, composed[i].Price, composed[i-1].Price)
		}
	}
}

func TestComposeRoundTrips_ExcludesUnpriced(t *testing.T) {
	out := []models.FlightResult{
		owFlight("A", "EUR", 100, "HEL", "BCN"),
		owFlight("B", "", 0, "HEL", "BCN"), // unpriced — must be dropped
	}
	in := []models.FlightResult{owFlight("C", "EUR", 50, "BCN", "HEL")}
	composed, _ := composeRoundTrips(out, in, SearchOptions{})
	if len(composed) != 1 {
		t.Fatalf("composed count: got %d, want 1 (unpriced outbound dropped)", len(composed))
	}
	if composed[0].Price != 150 {
		t.Errorf("price: got %v, want 150", composed[0].Price)
	}
}

func TestComposeRoundTrips_TagsLegDirection(t *testing.T) {
	out := []models.FlightResult{owFlight("Google Flights", "EUR", 100, "HEL", "BCN")}
	in := []models.FlightResult{owFlight("Ryanair", "EUR", 60, "BCN", "HEL")}

	composed, _ := composeRoundTrips(out, in, SearchOptions{})
	if len(composed) != 1 {
		t.Fatalf("composed count: got %d, want 1", len(composed))
	}
	legs := composed[0].Legs
	if len(legs) != 2 {
		t.Fatalf("legs: got %d, want 2", len(legs))
	}
	if legs[0].Direction != "outbound" {
		t.Errorf("first leg Direction: got %q, want outbound", legs[0].Direction)
	}
	if legs[1].Direction != "inbound" {
		t.Errorf("second leg Direction: got %q, want inbound", legs[1].Direction)
	}

	// The source one-way leg slices must stay untagged — they are cached/shared
	// upstream and a leaked round-trip tag would corrupt a later one-way response.
	if out[0].Legs[0].Direction != "" {
		t.Errorf("source outbound leg was mutated: Direction=%q", out[0].Legs[0].Direction)
	}
	if in[0].Legs[0].Direction != "" {
		t.Errorf("source inbound leg was mutated: Direction=%q", in[0].Legs[0].Direction)
	}
}

func TestComposeRoundTrips_SkipsCurrencyMismatch(t *testing.T) {
	out := []models.FlightResult{owFlight("A", "USD", 100, "HEL", "BCN")}
	in := []models.FlightResult{owFlight("C", "EUR", 50, "BCN", "HEL")}
	composed, _ := composeRoundTrips(out, in, SearchOptions{})
	if len(composed) != 0 {
		t.Fatalf("expected 0 composed (currency mismatch), got %d", len(composed))
	}
}

func TestComposeRoundTrips_EmptyInputs(t *testing.T) {
	if c, _ := composeRoundTrips(nil, []models.FlightResult{owFlight("C", "EUR", 50, "BCN", "HEL")}, SearchOptions{}); len(c) != 0 {
		t.Errorf("empty outbound should yield 0 composed, got %d", len(c))
	}
	if c, _ := composeRoundTrips([]models.FlightResult{owFlight("A", "EUR", 100, "HEL", "BCN")}, nil, SearchOptions{}); len(c) != 0 {
		t.Errorf("empty inbound should yield 0 composed, got %d", len(c))
	}
}

func TestComposeRoundTrips_BoundsAndReportsTruncation(t *testing.T) {
	// roundTripLegCandidates each side -> up to candidates^2 pairings, bounded to
	// roundTripMaxResults with truncated=true.
	var out, in []models.FlightResult
	for i := 0; i < roundTripLegCandidates+4; i++ {
		out = append(out, owFlight("A", "EUR", float64(100+i), "HEL", "BCN"))
		in = append(in, owFlight("C", "EUR", float64(50+i), "BCN", "HEL"))
	}
	composed, truncated := composeRoundTrips(out, in, SearchOptions{})
	if !truncated {
		t.Errorf("expected truncated=true when pairings exceed roundTripMaxResults")
	}
	if len(composed) != roundTripMaxResults {
		t.Errorf("composed count: got %d, want %d (bounded)", len(composed), roundTripMaxResults)
	}
}

func TestRoundTripComposerStatus_TruncationVisible(t *testing.T) {
	s := roundTripComposerStatus(8, 8, roundTripMaxResults, true)
	if s.Status != models.StatusOK {
		t.Errorf("status: got %q, want ok", s.Status)
	}
	if !strings.Contains(s.FixHint, "more pairings exist") {
		t.Errorf("truncation must be visible in fix hint, got %q", s.FixHint)
	}
}

func TestRoundTripComposerStatus_NoHit(t *testing.T) {
	s := roundTripComposerStatus(0, 0, 0, false)
	if s.Status != models.StatusCheckedNoHit {
		t.Errorf("status: got %q, want checked_no_hit", s.Status)
	}
	if s.Error == "" {
		t.Errorf("expected diagnostic error on zero pairings")
	}
}

func TestComposedProviderLabel(t *testing.T) {
	if got := composedProviderLabel("Google Flights", "Ryanair"); got != "composed (Google Flights + Ryanair)" {
		t.Errorf("label: got %q", got)
	}
	if got := composedProviderLabel("", ""); got != "composed (unknown + unknown)" {
		t.Errorf("empty label: got %q", got)
	}
}
