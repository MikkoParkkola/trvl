package models

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestHackSaving_CandidatesRoundTrip guards the Candidates field added to
// HackSaving (rail_fly concrete bookable results). It must survive a JSON
// round-trip with real legs + price intact, so the ApplySharedFlightPolicy
// layer can append genuine searched flights into the ranked list.
func TestHackSaving_CandidatesRoundTrip(t *testing.T) {
	in := HackSaving{
		Type:     "rail_fly_arbitrage",
		Savings:  160,
		Currency: "EUR",
		Candidates: []FlightResult{
			{
				Price:    210,
				Currency: "EUR",
				Legs:     []FlightLeg{{Airline: "KLM", AirlineCode: "KL"}},
			},
		},
	}

	blob, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(blob), `"candidates"`) {
		t.Fatalf("populated Candidates must serialize under \"candidates\", got %s", blob)
	}

	var out HackSaving
	if err := json.Unmarshal(blob, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Candidates) != 1 {
		t.Fatalf("Candidates len = %d, want 1", len(out.Candidates))
	}
	c := out.Candidates[0]
	if c.Price != 210 || c.Currency != "EUR" {
		t.Errorf("candidate price/currency = %v %q, want 210 EUR", c.Price, c.Currency)
	}
	if len(c.Legs) != 1 || c.Legs[0].AirlineCode != "KL" {
		t.Errorf("candidate legs not preserved: %+v", c.Legs)
	}
}

// TestHackSaving_CandidatesOmitEmpty ensures the new field is omitempty, so an
// unpopulated saving keeps the wire shape unchanged (no fabricated candidates).
func TestHackSaving_CandidatesOmitEmpty(t *testing.T) {
	blob, err := json.Marshal(HackSaving{Type: "hidden_city"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(blob), "candidates") {
		t.Errorf("empty Candidates must be omitted, got %s", blob)
	}
}
