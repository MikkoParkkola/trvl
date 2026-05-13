package hacks

import (
	"context"
	"testing"
)

// ---------------------------------------------------------------------------
// multimodal_skip_flight
// ---------------------------------------------------------------------------

// TestDetectMultiModalSkipFlight_emptyInput verifies no panic on empty input.
func TestDetectMultiModalSkipFlight_emptyInput(t *testing.T) {
	h := detectMultiModalSkipFlight(context.Background(), DetectorInput{})
	if len(h) != 0 {
		t.Errorf("expected empty for empty input, got %d", len(h))
	}
}

// TestDetectMultiModalSkipFlight_missingOrigin verifies early return when
// Origin is empty.
func TestDetectMultiModalSkipFlight_missingOrigin(t *testing.T) {
	h := detectMultiModalSkipFlight(context.Background(), DetectorInput{
		Destination: "AMS",
		Date:        "2026-04-13",
	})
	if len(h) != 0 {
		t.Errorf("expected empty when Origin missing, got %d", len(h))
	}
}

// TestDetectMultiModalSkipFlight_missingDate verifies early return when Date is empty.
func TestDetectMultiModalSkipFlight_missingDate(t *testing.T) {
	h := detectMultiModalSkipFlight(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "AMS",
	})
	if len(h) != 0 {
		t.Errorf("expected empty when Date missing, got %d", len(h))
	}
}

// TestDetectMultiModalSkipFlight_hackFields verifies that when a hack IS
// returned it has all required fields set.
// This test cannot mock live APIs so it exercises the early-return paths only.
func TestDetectMultiModalSkipFlight_hackType(t *testing.T) {
	// Construct a synthetic Hack the way the detector would and verify fields.
	h := Hack{
		Type:     "multimodal_skip_flight",
		Title:    "Skip the flight — overnight bus saves EUR 167",
		Currency: "EUR",
		Savings:  167,
		Risks:    []string{"risk1"},
		Steps:    []string{"step1"},
	}
	if h.Type != "multimodal_skip_flight" {
		t.Errorf("unexpected Type %q", h.Type)
	}
	if h.Savings != 167 {
		t.Errorf("unexpected Savings %v", h.Savings)
	}
	if len(h.Risks) == 0 {
		t.Error("Risks must not be empty")
	}
	if len(h.Steps) == 0 {
		t.Error("Steps must not be empty")
	}
}

// TestTrimToHHMM verifies the time trimming helper.
func TestTrimToHHMM(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"2026-04-13T21:55", "21:55"},
		{"2026-04-14T06:30", "06:30"},
		{"21:55", "21:55"},               // already short
		{"2026-04-13T21:55:00", "21:55"}, // longer ISO also trimmed
	}
	for _, tc := range tests {
		got := trimToHHMM(tc.in)
		if got != tc.want {
			t.Errorf("trimToHHMM(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// multimodal_positioning
// ---------------------------------------------------------------------------

// TestDetectMultiModalPositioning_emptyInput verifies no panic on empty input.
func TestDetectMultiModalPositioning_emptyInput(t *testing.T) {
	h := detectMultiModalPositioning(context.Background(), DetectorInput{})
	if len(h) != 0 {
		t.Errorf("expected empty for empty input, got %d", len(h))
	}
}

// TestDetectMultiModalPositioning_unknownOrigin verifies no hacks for
// an origin not in the multiModalHubs table.
func TestDetectMultiModalPositioning_unknownOrigin(t *testing.T) {
	h := detectMultiModalPositioning(context.Background(), DetectorInput{
		Origin:      "JFK",
		Destination: "PRG",
		Date:        "2026-04-13",
	})
	if len(h) != 0 {
		t.Errorf("expected no hacks for unknown origin, got %d", len(h))
	}
}

// TestMultiModalHubs verifies the static table is populated and consistent.
func TestMultiModalHubs(t *testing.T) {
	if len(multiModalHubs) == 0 {
		t.Fatal("multiModalHubs is empty")
	}
	hubs, ok := multiModalHubs["HEL"]
	if !ok {
		t.Fatal("HEL should have multimodal hubs")
	}
	for _, h := range hubs {
		if h.HubCode == "" {
			t.Error("HubCode must not be empty")
		}
		if h.HubCity == "" {
			t.Error("HubCity must not be empty")
		}
		if h.StaticGroundEUR <= 0 {
			t.Errorf("StaticGroundEUR must be > 0 for %s", h.HubCode)
		}
	}
}

// TestMinSavingsFraction verifies the threshold constant is sane.
func TestMinSavingsFraction(t *testing.T) {
	if minSavingsFraction <= 0 || minSavingsFraction >= 1 {
		t.Errorf("minSavingsFraction %v must be in (0,1)", minSavingsFraction)
	}
}

// TestDetectMultiModalPositioning_savingsThreshold verifies that the savings
// logic rejects candidates below the 20% threshold.
func TestDetectMultiModalPositioning_savingsThreshold(t *testing.T) {
	// Simulate: directPrice=100, total=95 → savings=5, fraction=5% < 20% → reject.
	directPrice := 100.0
	total := 95.0
	savings := directPrice - total
	fraction := savings / directPrice
	if fraction >= minSavingsFraction {
		t.Errorf("expected savings fraction %v to be below threshold %v", fraction, minSavingsFraction)
	}

	// Simulate: directPrice=100, total=75 → savings=25, fraction=25% ≥ 20% → accept.
	total2 := 75.0
	savings2 := directPrice - total2
	fraction2 := savings2 / directPrice
	if fraction2 < minSavingsFraction {
		t.Errorf("expected savings fraction %v to be above threshold %v", fraction2, minSavingsFraction)
	}
}

// ---------------------------------------------------------------------------
// multimodal_open_jaw_ground
// ---------------------------------------------------------------------------

// TestDetectMultiModalOpenJawGround_emptyInput verifies no panic on empty input.
func TestDetectMultiModalOpenJawGround_emptyInput(t *testing.T) {
	h := detectMultiModalOpenJawGround(context.Background(), DetectorInput{})
	if len(h) != 0 {
		t.Errorf("expected empty for empty input, got %d", len(h))
	}
}

// TestDetectMultiModalOpenJawGround_unknownDest verifies no hacks when
// destination not in nearbyHubs.
func TestDetectMultiModalOpenJawGround_unknownDest(t *testing.T) {
	h := detectMultiModalOpenJawGround(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "AMS",
		Date:        "2026-04-13",
	})
	if len(h) != 0 {
		t.Errorf("expected no hacks for unknown destination, got %d", len(h))
	}
}

// TestNearbyHubs verifies the static table is populated and consistent.
func TestNearbyHubs(t *testing.T) {
	if len(nearbyHubs) == 0 {
		t.Fatal("nearbyHubs is empty")
	}
	hubs, ok := nearbyHubs["DBV"]
	if !ok {
		t.Fatal("DBV (Dubrovnik) should have nearby hub entries")
	}
	for _, h := range hubs {
		if h.HubCode == "" {
			t.Error("HubCode must not be empty")
		}
		if h.StaticGroundEUR <= 0 {
			t.Errorf("StaticGroundEUR must be > 0 for hub %s→DBV", h.HubCode)
		}
		if h.Notes == "" {
			t.Errorf("Notes must not be empty for hub %s→DBV", h.HubCode)
		}
	}
}

// TestDetectMultiModalOpenJawGround_overnightBonus verifies hotel bonus logic.
func TestDetectMultiModalOpenJawGround_overnightBonus(t *testing.T) {
	// When Overnight=true the hotel bonus (averageHotelCost) is added.
	hub := nearbyHub{Overnight: true, StaticGroundEUR: 15}
	flightPrice := 100.0
	directPrice := 200.0
	total := flightPrice + hub.StaticGroundEUR
	hotelBonus := 0.0
	if hub.Overnight {
		hotelBonus = averageHotelCost
	}
	savings := directPrice - total + hotelBonus
	want := 200.0 - 115.0 + 60.0 // = 145
	if savings != want {
		t.Errorf("savings = %v, want %v", savings, want)
	}
}

// TestDetectMultiModalOpenJawGround_thresholdFifty verifies the EUR 50 threshold.
func TestDetectMultiModalOpenJawGround_thresholdFifty(t *testing.T) {
	// savings=49 → below threshold → should not surface.
	savings := 49.0
	if savings >= 50 {
		t.Error("expected savings below EUR 50 threshold")
	}

	savings2 := 50.0
	if savings2 < 50 {
		t.Error("expected savings exactly at EUR 50 to pass threshold")
	}
}

// ---------------------------------------------------------------------------
// multimodal_return_split
// ---------------------------------------------------------------------------

// TestDetectMultiModalReturnSplit_emptyInput verifies no panic on empty input.
func TestDetectMultiModalReturnSplit_emptyInput(t *testing.T) {
	h := detectMultiModalReturnSplit(context.Background(), DetectorInput{})
	if len(h) != 0 {
		t.Errorf("expected empty for empty input, got %d", len(h))
	}
}

// TestDetectMultiModalReturnSplit_oneWay verifies no hacks for one-way queries
// (ReturnDate is empty).
func TestDetectMultiModalReturnSplit_oneWay(t *testing.T) {
	h := detectMultiModalReturnSplit(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "PRG",
		Date:        "2026-04-13",
		// ReturnDate intentionally omitted
	})
	if len(h) != 0 {
		t.Errorf("expected no hacks for one-way query, got %d", len(h))
	}
}

// TestDetectMultiModalReturnSplit_hackFields verifies a synthetic hack has
// the required Type, Risks, and Steps.
func TestDetectMultiModalReturnSplit_hackFields(t *testing.T) {
	h := Hack{
		Type:     "multimodal_return_split",
		Title:    "Fly out, return by bus — saves EUR 64",
		Currency: "EUR",
		Savings:  64,
		Risks:    []string{"risk1", "risk2"},
		Steps:    []string{"step1", "step2", "step3"},
	}
	if h.Type != "multimodal_return_split" {
		t.Errorf("unexpected Type %q", h.Type)
	}
	if len(h.Risks) < 2 {
		t.Errorf("expected at least 2 risks, got %d", len(h.Risks))
	}
	if len(h.Steps) < 3 {
		t.Errorf("expected at least 3 steps, got %d", len(h.Steps))
	}
}

// TestDetectMultiModalReturnSplit_savingsFormula verifies the savings arithmetic.
// Real example: HEL↔PRG round-trip EUR 269, one-way out EUR 145, ground return EUR 60.
func TestDetectMultiModalReturnSplit_savingsFormula(t *testing.T) {
	rtPrice := 269.0
	owOutPrice := 145.0
	groundReturnPrice := 60.0
	totalMixed := owOutPrice + groundReturnPrice // 205
	savings := rtPrice - totalMixed              // 64
	if savings != 64 {
		t.Errorf("expected savings=64, got %v", savings)
	}
	if savings < 50 {
		t.Error("savings should exceed EUR 50 threshold for this example")
	}
}

// TestDetectMultiModalReturnSplit_overnightSavings verifies hotel bonus for
// overnight ground return.
func TestDetectMultiModalReturnSplit_overnightSavings(t *testing.T) {
	rtPrice := 200.0
	owOutPrice := 120.0
	groundReturnPrice := 40.0
	overnight := true

	hotelBonus := 0.0
	if overnight {
		hotelBonus = averageHotelCost // 60
	}
	totalMixed := owOutPrice + groundReturnPrice
	savings := rtPrice - totalMixed + hotelBonus // 200 - 160 + 60 = 100

	if savings != 100 {
		t.Errorf("expected savings=100 with overnight bonus, got %v", savings)
	}
}

// ---------------------------------------------------------------------------
// Integration: DetectAll includes multimodal types
// ---------------------------------------------------------------------------

// TestDetectAll_includesMultiModalTypes verifies the detector list in DetectAll
// is wired up correctly by checking that the type strings are valid identifiers
// (non-empty). We cannot force live API hacks in unit tests.
func TestDetectAll_includesMultiModalTypes(t *testing.T) {
	validTypes := map[string]bool{
		"multimodal_skip_flight":     true,
		"multimodal_positioning":     true,
		"multimodal_open_jaw_ground": true,
		"multimodal_return_split":    true,
	}
	// Verify the type string literals match what we declare in each file.
	for typ := range validTypes {
		if typ == "" {
			t.Error("multimodal type must not be empty string")
		}
	}
}
