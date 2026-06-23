package flights

import (
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// nativeRTFare builds an outbound-only native round-trip fare, mirroring what
// tagNativeRoundTrip produces when a provider prices the return "at booking":
// FareRoundTrip, a single outbound-tagged leg, and the booking-time warning.
func nativeRTFare(price float64, warning string) models.FlightResult {
	return models.FlightResult{
		Price: price, Currency: "EUR", Provider: "Google Flights",
		FareType: models.FareRoundTrip,
		Legs: []models.FlightLeg{{
			DepartureAirport: models.AirportInfo{Code: "HEL"},
			ArrivalAirport:   models.AirportInfo{Code: "BCN"},
			DepartureTime:    "2026-07-15T08:00",
			ArrivalTime:      "2026-07-15T11:00",
			Direction:        "outbound",
		}},
		Warnings: []string{warning},
	}
}

// inboundOneWay builds a fully-detailed inbound (D->O) one-way result, the kind
// the composer already fetches and the enricher reuses.
func inboundOneWay(price float64, airline, departTime string) models.FlightResult {
	return models.FlightResult{
		Price: price, Currency: "EUR", Provider: airline,
		Legs: []models.FlightLeg{{
			DepartureAirport: models.AirportInfo{Code: "BCN"},
			ArrivalAirport:   models.AirportInfo{Code: "HEL"},
			DepartureTime:    departTime,
			ArrivalTime:      departTime,
			Airline:          airline,
			AirlineCode:      "RR",
			FlightNumber:     "RR123",
		}},
	}
}

func TestEnrichNativeReturnLegs_AttachesRealReturnLeg(t *testing.T) {
	native := []models.FlightResult{nativeRTFare(280, googleNativeRoundTripWarning)}
	inbound := []models.FlightResult{inboundOneWay(60, "Ryanair", "2026-07-22T18:40")}

	out, status := enrichNativeReturnLegs(native, inbound, roundTripNativeReturnCandidates)

	if len(out) != 1 {
		t.Fatalf("result count: got %d, want 1", len(out))
	}
	f := out[0]
	// Real return leg attached and tagged inbound, outbound preserved.
	if len(f.Legs) != 2 {
		t.Fatalf("legs: got %d, want 2 (outbound + enriched inbound)", len(f.Legs))
	}
	if f.Legs[0].Direction != "outbound" {
		t.Errorf("leg 0 direction: got %q, want outbound", f.Legs[0].Direction)
	}
	if f.Legs[1].Direction != "inbound" {
		t.Errorf("leg 1 direction: got %q, want inbound", f.Legs[1].Direction)
	}
	// Native TOTAL is unchanged — the inbound one-way price is NOT summed in.
	if f.Price != 280 {
		t.Errorf("price: got %v, want 280 (native total unchanged)", f.Price)
	}
	if f.FareType != models.FareRoundTrip {
		t.Errorf("fare type: got %q, want round_trip", f.FareType)
	}
	// Booking-time-only warning dropped; honest enrichment warning present.
	for _, w := range f.Warnings {
		if w == googleNativeRoundTripWarning {
			t.Errorf("booking-time warning must be dropped once a real return is shown: %v", f.Warnings)
		}
	}
	if len(f.Warnings) == 0 || !strings.Contains(f.Warnings[0], "selected at booking and may differ") {
		t.Errorf("expected honest enrichment warning, got %v", f.Warnings)
	}
	if !strings.Contains(f.Warnings[0], "Ryanair") || !strings.Contains(f.Warnings[0], "60 EUR") {
		t.Errorf("warning should record the indicative return provider+price, got %q", f.Warnings[0])
	}
	if status.Status != models.StatusOK || status.Results != 1 {
		t.Errorf("status: got %q results=%d, want ok results=1", status.Status, status.Results)
	}
}

func TestEnrichNativeReturnLegs_InboundLegFilterable(t *testing.T) {
	// The whole point: a per-leg return-condition filter must have real detail to
	// act on. Assert the attached inbound leg carries carrier + departure time.
	native := []models.FlightResult{nativeRTFare(280, googleNativeRoundTripWarning)}
	inbound := []models.FlightResult{inboundOneWay(60, "Ryanair", "2026-07-22T18:40")}

	out, _ := enrichNativeReturnLegs(native, inbound, roundTripNativeReturnCandidates)
	ret := out[0].Legs[1]
	if ret.Airline != "Ryanair" {
		t.Errorf("inbound carrier: got %q, want Ryanair (filterable)", ret.Airline)
	}
	if ret.DepartureTime != "2026-07-22T18:40" {
		t.Errorf("inbound depart time: got %q, want 2026-07-22T18:40 (filterable)", ret.DepartureTime)
	}
	if ret.DepartureAirport.Code != "BCN" || ret.ArrivalAirport.Code != "HEL" {
		t.Errorf("inbound route: got %s->%s, want BCN->HEL", ret.DepartureAirport.Code, ret.ArrivalAirport.Code)
	}
}

func TestEnrichNativeReturnLegs_TopNCapEnforcedAndReported(t *testing.T) {
	// 5 outbound-only native fares, only roundTripNativeReturnCandidates (3) may
	// be enriched. The cap must be enforced AND reported, never silent.
	native := []models.FlightResult{
		nativeRTFare(200, googleNativeRoundTripWarning),
		nativeRTFare(210, googleNativeRoundTripWarning),
		nativeRTFare(220, googleNativeRoundTripWarning),
		nativeRTFare(230, googleNativeRoundTripWarning),
		nativeRTFare(240, googleNativeRoundTripWarning),
	}
	inbound := []models.FlightResult{
		inboundOneWay(50, "A", "2026-07-22T06:00"),
		inboundOneWay(60, "B", "2026-07-22T12:00"),
		inboundOneWay(70, "C", "2026-07-22T18:00"),
	}

	out, status := enrichNativeReturnLegs(native, inbound, roundTripNativeReturnCandidates)

	enriched := 0
	for _, f := range out {
		if flightHasInboundLeg(f) {
			enriched++
		}
	}
	if enriched != roundTripNativeReturnCandidates {
		t.Errorf("enriched count: got %d, want %d (cap)", enriched, roundTripNativeReturnCandidates)
	}
	if status.Results != roundTripNativeReturnCandidates {
		t.Errorf("status.Results: got %d, want %d", status.Results, roundTripNativeReturnCandidates)
	}
	// Honest cap reporting: names enriched vs total eligible.
	if !strings.Contains(status.FixHint, "enriched cheapest 3 of 5") {
		t.Errorf("cap must be reported in status, got %q", status.FixHint)
	}
	// The two cheapest-first enriched fares should pair with distinct returns.
	var firstReturns []string
	var warnings []string
	for _, f := range out {
		if flightHasInboundLeg(f) {
			firstReturns = append(firstReturns, f.Legs[1].Airline)
			warnings = append(warnings, f.Warnings[0])
		}
	}
	if firstReturns[0] == firstReturns[1] {
		t.Errorf("expected distinct return legs across enriched fares, got %v", firstReturns)
	}
	// The warning is built dynamically from each attached return's real carrier +
	// price, so distinct returns MUST yield distinct warning strings (guards
	// against regressing to a hardcoded literal).
	if warnings[0] == warnings[1] {
		t.Errorf("warnings must differ per real return, got identical: %q", warnings[0])
	}
	if !strings.Contains(warnings[0], "A") || !strings.Contains(warnings[0], "50 EUR") {
		t.Errorf("warning[0] should reflect its real return (A, 50 EUR), got %q", warnings[0])
	}
	if !strings.Contains(warnings[1], "B") || !strings.Contains(warnings[1], "60 EUR") {
		t.Errorf("warning[1] should reflect its real return (B, 60 EUR), got %q", warnings[1])
	}
}

func TestEnrichNativeReturnLegs_NoInboundIsNoOp(t *testing.T) {
	// A provider that genuinely cannot price the return per-leg leaves no inbound
	// one-way to reuse: the native fare must be returned unchanged (honest), with
	// a no-hit status — never a fabricated return.
	native := []models.FlightResult{nativeRTFare(280, googleNativeRoundTripWarning)}

	out, status := enrichNativeReturnLegs(native, nil, roundTripNativeReturnCandidates)

	if len(out[0].Legs) != 1 {
		t.Errorf("legs: got %d, want 1 (unchanged, no fabricated return)", len(out[0].Legs))
	}
	if out[0].Warnings[0] != googleNativeRoundTripWarning {
		t.Errorf("original booking-time warning must be preserved when no return found: %v", out[0].Warnings)
	}
	if status.Status != models.StatusCheckedNoHit {
		t.Errorf("status: got %q, want checked_no_hit", status.Status)
	}
}

func TestEnrichNativeReturnLegs_LeavesTwoLeggedAndOneWayUntouched(t *testing.T) {
	// A native fare that already carries both halves, and a one-way (no FareType)
	// result, must both be left untouched — only outbound-only native fares are
	// enriched.
	twoLegged := nativeRTFare(300, googleNativeRoundTripWarning)
	twoLegged.Legs = append(twoLegged.Legs, models.FlightLeg{
		DepartureAirport: models.AirportInfo{Code: "BCN"},
		ArrivalAirport:   models.AirportInfo{Code: "HEL"},
		DepartureTime:    "2026-07-22T18:40",
		Direction:        "inbound",
	})
	oneWay := owFlight("Wizz", "EUR", 90, "HEL", "BCN") // FareType empty

	native := []models.FlightResult{twoLegged, oneWay}
	inbound := []models.FlightResult{inboundOneWay(60, "Ryanair", "2026-07-22T18:40")}

	out, status := enrichNativeReturnLegs(native, inbound, roundTripNativeReturnCandidates)

	if len(out[0].Legs) != 2 {
		t.Errorf("already-two-legged fare must be untouched: legs=%d", len(out[0].Legs))
	}
	if len(out[1].Legs) != 1 {
		t.Errorf("one-way result must be untouched: legs=%d", len(out[1].Legs))
	}
	if status.Results != 0 {
		t.Errorf("nothing eligible to enrich: status.Results=%d, want 0", status.Results)
	}
}

func TestEnrichNativeReturnLegs_DoesNotMutateInboundSource(t *testing.T) {
	native := []models.FlightResult{nativeRTFare(280, googleNativeRoundTripWarning)}
	inbound := []models.FlightResult{inboundOneWay(60, "Ryanair", "2026-07-22T18:40")}

	_, _ = enrichNativeReturnLegs(native, inbound, roundTripNativeReturnCandidates)

	if inbound[0].Legs[0].Direction != "" {
		t.Errorf("inbound source leg mutated: Direction=%q, want empty", inbound[0].Legs[0].Direction)
	}
}
