package flights

import "testing"

// TestKiwiLegEndpoints covers the summary-first / segment-fallback resolution
// that keeps a leg usable after a routing-summary rename (review finding #2).
func TestKiwiLegEndpoints(t *testing.T) {
	cases := []struct {
		name           string
		leg            *kiwiLeg
		wantFrom, want string
		wantOK         bool
	}{
		{"nil", nil, "", "", false},
		{"summary", &kiwiLeg{From: "HEL", To: "BCN"}, "HEL", "BCN", true},
		{
			"segment fallback",
			&kiwiLeg{Segments: []kiwiSegment{{From: "HEL", To: "ARN"}, {From: "ARN", To: "BCN"}}},
			"HEL", "BCN", true,
		},
		{"routeless", &kiwiLeg{}, "", "", false},
	}
	for _, tc := range cases {
		from, to, ok := kiwiLegEndpoints(tc.leg)
		if ok != tc.wantOK || from != tc.wantFrom || to != tc.want {
			t.Errorf("%s: kiwiLegEndpoints = (%q,%q,%v), want (%q,%q,%v)",
				tc.name, from, to, ok, tc.wantFrom, tc.want, tc.wantOK)
		}
	}
}

// TestKiwiItineraryUsable checks routeless entries are rejected (finding #1).
func TestKiwiItineraryUsable(t *testing.T) {
	if kiwiItineraryUsable(kiwiItinerary{Outbound: nil}) {
		t.Error("nil outbound must be unusable")
	}
	if kiwiItineraryUsable(kiwiItinerary{Outbound: &kiwiLeg{}}) {
		t.Error("routeless outbound must be unusable")
	}
	if !kiwiItineraryUsable(kiwiItinerary{Outbound: &kiwiLeg{From: "HEL", To: "BCN"}}) {
		t.Error("outbound with endpoints must be usable")
	}
}

// TestMapKiwiItinerarySelfConnectFromStops locks self-connect derivation from
// the stops count for a summary-only connected leg (review finding #5).
func TestMapKiwiItinerarySelfConnectFromStops(t *testing.T) {
	it := kiwiItinerary{
		Price:                120,
		TotalDurationSeconds: 10800,
		Outbound: &kiwiLeg{
			From: "HEL", To: "BCN",
			DepartureTime: "2026-07-31T08:00:00", ArrivalTime: "2026-07-31T11:00:00",
			Stops: 1, // connection, but NO segments array
		},
	}
	fr := mapKiwiItinerary(it, "EUR")
	if !fr.SelfConnect {
		t.Error("stops>0 with no segments must still mark SelfConnect")
	}
	if len(fr.Warnings) == 0 {
		t.Error("self-connect must attach the bag-recheck warning")
	}
	if fr.Stops != 1 {
		t.Errorf("Stops = %d, want 1 (from Outbound.Stops)", fr.Stops)
	}
}

// TestMapKiwiItineraryDurationFallback covers the layered duration fallback
// (review finding #4): trip total -> per-leg DurationSeconds -> summed legs.
func TestMapKiwiItineraryDurationFallback(t *testing.T) {
	// No trip total; outbound leg carries its own DurationSeconds (7200 = 120min).
	it := kiwiItinerary{
		Price: 90,
		Outbound: &kiwiLeg{
			From: "HEL", To: "BCN",
			DepartureTime: "2026-07-31T08:00:00", ArrivalTime: "2026-07-31T10:00:00",
			DurationSeconds: 7200,
		},
	}
	if fr := mapKiwiItinerary(it, "EUR"); fr.Duration != 120 {
		t.Errorf("Duration = %d, want 120 (per-leg DurationSeconds fallback)", fr.Duration)
	}

	// No total, no leg DurationSeconds: fall back to summed mapped-leg durations
	// (single segment 08:00->10:00 = 120min).
	it2 := kiwiItinerary{
		Price: 90,
		Outbound: &kiwiLeg{
			From: "HEL", To: "BCN",
			Segments: []kiwiSegment{{
				From: "HEL", To: "BCN",
				DepartureTime: "2026-07-31T08:00:00", ArrivalTime: "2026-07-31T10:00:00",
			}},
		},
	}
	if fr := mapKiwiItinerary(it2, "EUR"); fr.Duration != 120 {
		t.Errorf("Duration = %d, want 120 (summed-leg fallback)", fr.Duration)
	}
}

// TestParseKiwiRPCResponseMultilineSSE verifies a single JSON-RPC payload split
// across multiple SSE data: lines is joined and parsed (review finding #6).
func TestParseKiwiRPCResponseMultilineSSE(t *testing.T) {
	body := []byte("event: message\n" +
		"data: {\"jsonrpc\":\"2.0\",\"id\":1,\n" +
		"data: \"result\":{\"content\":[{\"type\":\"text\",\"text\":\"ok\"}]}}\n" +
		"\n")
	rpc, err := parseKiwiRPCResponse(body)
	if err != nil {
		t.Fatalf("parseKiwiRPCResponse: %v", err)
	}
	if rpc.Result == nil {
		t.Fatal("expected a Result from the joined multi-line data frame")
	}
}
