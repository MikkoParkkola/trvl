package multimodal

import "testing"

func TestDedupeItineraries(t *testing.T) {
	in := []Itinerary{
		{ModeChain: "ferry", Currency: "EUR", TotalPrice: 12, DurationMin: 120, Source: "a"},
		{ModeChain: "ferry", Currency: "EUR", TotalPrice: 12, DurationMin: 120, Source: "b"},       // dup of first
		{ModeChain: "ferry", Currency: "EUR", TotalPrice: 14, DurationMin: 120, Source: "c"},       // different price
		{ModeChain: "ferry → fly", Currency: "EUR", TotalPrice: 12, DurationMin: 120, Source: "d"}, // different chain
		{ModeChain: "ferry", Currency: "EUR", TotalPrice: 12, DurationMin: 159, Source: "e"},       // different duration
	}
	out := dedupeItineraries(in)
	if len(out) != 4 {
		t.Fatalf("got %d itineraries, want 4: %+v", len(out), out)
	}
	// The first of a duplicate pair is kept (rank order already preferred it).
	if out[0].Source != "a" {
		t.Errorf("kept %q for the ferry/12/120 row, want the first (a)", out[0].Source)
	}
	for _, it := range out {
		if it.Source == "b" {
			t.Error("duplicate itinerary (b) was not collapsed")
		}
	}
}

func TestDedupeItineraries_Empty(t *testing.T) {
	if out := dedupeItineraries(nil); len(out) != 0 {
		t.Errorf("dedupeItineraries(nil) = %v, want empty", out)
	}
}
