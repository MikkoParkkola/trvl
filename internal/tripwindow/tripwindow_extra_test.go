package tripwindow

import (
	"context"
	"strings"
	"testing"
	"time"
)

// ============================================================
// Find — budget filter path
// ============================================================

func TestFind_BudgetFilter(t *testing.T) {
	t.Parallel()
	// With an impossibly low budget, all candidates should be filtered out
	// (or returned with 0 cost since the searches will fail for fake cities).
	candidates, err := Find(context.Background(), Input{
		Origin:      "HEL",
		Destination: "PRG",
		WindowStart: "2026-05-01",
		WindowEnd:   "2026-05-05",
		MinNights:   3,
		MaxNights:   3,
		BudgetEUR:   0.01, // impossibly low — any priced result is filtered out
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Either 0 candidates or candidates with 0 cost (search failures).
	for _, c := range candidates {
		if c.EstimatedCost > 0.01 {
			t.Errorf("candidate cost %v exceeds budget 0.01", c.EstimatedCost)
		}
	}
}

// ============================================================
// Find — single night window
// ============================================================

func TestFind_SingleNightWindow(t *testing.T) {
	t.Parallel()
	candidates, err := Find(context.Background(), Input{
		Origin:      "HEL",
		Destination: "PRG",
		WindowStart: "2026-05-01",
		WindowEnd:   "2026-05-03", // room for 1 or 2 night trips
		MinNights:   1,
		MaxNights:   1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, c := range candidates {
		if c.Nights != 1 {
			t.Errorf("expected 1-night trip, got %d", c.Nights)
		}
	}
}

// ============================================================
// applyDefaults — additional branches
// ============================================================

func TestApplyDefaults_NegativeValues(t *testing.T) {
	t.Parallel()
	in := Input{MinNights: -5, MaxNights: -3, MaxCandidates: -1}
	in.applyDefaults()
	if in.MinNights != 3 {
		t.Errorf("MinNights = %d, want 3", in.MinNights)
	}
	if in.MaxNights != 7 {
		t.Errorf("MaxNights = %d, want 7", in.MaxNights)
	}
	if in.MaxCandidates != 5 {
		t.Errorf("MaxCandidates = %d, want 5", in.MaxCandidates)
	}
}

func TestApplyDefaults_OnlyMaxCandidatesZero(t *testing.T) {
	t.Parallel()
	in := Input{MinNights: 2, MaxNights: 4, MaxCandidates: 0}
	in.applyDefaults()
	if in.MaxCandidates != 5 {
		t.Errorf("MaxCandidates = %d, want 5", in.MaxCandidates)
	}
	if in.MinNights != 2 {
		t.Errorf("MinNights = %d, want 2 (should not change)", in.MinNights)
	}
}

// ============================================================
// ValidateInput — additional date edge cases
// ============================================================

func TestValidateInput_SameDayValid(t *testing.T) {
	t.Parallel()
	err := ValidateInput(Input{
		Destination: "PRG",
		WindowStart: "2026-06-15",
		WindowEnd:   "2026-06-15",
	})
	if err != nil {
		t.Errorf("same day should be valid: %v", err)
	}
}

func TestValidateInput_OneDayApart(t *testing.T) {
	t.Parallel()
	err := ValidateInput(Input{
		Destination: "PRG",
		WindowStart: "2026-06-15",
		WindowEnd:   "2026-06-16",
	})
	if err != nil {
		t.Errorf("one day apart should be valid: %v", err)
	}
}

// ============================================================
// ParseBusyFlag — boundary cases
// ============================================================

func TestParseBusyFlag_ExactLength21(t *testing.T) {
	t.Parallel()
	iv, err := ParseBusyFlag("2026-01-01:2026-01-10")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if iv.Start != "2026-01-01" {
		t.Errorf("Start = %q", iv.Start)
	}
}

func TestParseBusyFlag_TooLong(t *testing.T) {
	t.Parallel()
	_, err := ParseBusyFlag("2026-01-01:2026-01-10X")
	if err == nil {
		t.Error("expected error for input longer than 21")
	}
}

func TestParseBusyFlag_NoColonAtPosition10(t *testing.T) {
	t.Parallel()
	_, err := ParseBusyFlag("2026-01-01-2026-01-10")
	if err == nil {
		t.Error("expected error when position 10 is not a colon")
	}
}

// ============================================================
// buildReasoning — comprehensive output validation
// ============================================================

func TestBuildReasoning_ContainsNights(t *testing.T) {
	t.Parallel()
	r := buildReasoning(testDate("2026-06-01"), testDate("2026-06-04"), 3, 200, "EUR", false)
	if !strings.Contains(r, "3 nights") {
		t.Errorf("reasoning %q should contain '3 nights'", r)
	}
}

func TestBuildReasoning_ContainsDates(t *testing.T) {
	t.Parallel()
	r := buildReasoning(testDate("2026-06-01"), testDate("2026-06-04"), 3, 200, "EUR", false)
	if !strings.Contains(r, "Jun") {
		t.Errorf("reasoning %q should contain month name", r)
	}
}

func TestBuildReasoning_PreferredLabel(t *testing.T) {
	t.Parallel()
	r := buildReasoning(testDate("2026-06-01"), testDate("2026-06-04"), 3, 200, "EUR", true)
	if !strings.Contains(r, "preferred") {
		t.Errorf("reasoning %q should contain 'preferred'", r)
	}
}

func TestBuildReasoning_PriceUnavailable(t *testing.T) {
	t.Parallel()
	r := buildReasoning(testDate("2026-06-01"), testDate("2026-06-04"), 3, 0, "", false)
	if !strings.Contains(r, "price unavailable") {
		t.Errorf("reasoning %q should contain 'price unavailable'", r)
	}
}

// ============================================================
// mustParseIntervals — mixed valid and invalid
// ============================================================

func TestMustParseIntervals_MixedValidInvalid(t *testing.T) {
	t.Parallel()
	ivs := []Interval{
		{Start: "2026-05-01", End: "2026-05-10"},
		{Start: "bad", End: "2026-06-10"},
		{Start: "2026-07-01", End: "bad"},
		{Start: "2026-08-01", End: "2026-08-15"},
	}
	parsed := mustParseIntervals(ivs)
	if len(parsed) != 2 {
		t.Errorf("expected 2 valid intervals, got %d", len(parsed))
	}
}

// ============================================================
// overlapsAny — edge case: trip and interval share single day boundary
// ============================================================

func TestOverlapsAny_SingleDayInterval(t *testing.T) {
	t.Parallel()
	ivs := mustParseIntervals([]Interval{
		{Start: "2026-05-10", End: "2026-05-10"}, // single-day busy
	})
	// Trip ending on the busy day should overlap (inclusive).
	if !overlapsAny(testDate("2026-05-08"), testDate("2026-05-10"), ivs) {
		t.Error("trip ending on single-day busy should overlap")
	}
	// Trip starting on the busy day should overlap.
	if !overlapsAny(testDate("2026-05-10"), testDate("2026-05-12"), ivs) {
		t.Error("trip starting on single-day busy should overlap")
	}
	// Trip not touching should not overlap.
	if overlapsAny(testDate("2026-05-11"), testDate("2026-05-15"), ivs) {
		t.Error("trip after single-day busy should not overlap")
	}
}

// ============================================================
// cheapestFlightWithBudget — negative budget path
// ============================================================

func TestCheapestFlightWithBudget_NegativeBudget(t *testing.T) {
	t.Parallel()
	// Negative budget effectively means no budget filter.
	price, curr := cheapestFlightWithBudget(context.Background(), "HEL", "PRG", "2026-05-01", "2026-05-05", -100)
	// Will return 0 because there are no real search results.
	_ = price
	_ = curr
}

// ============================================================
// cheapestHotel — negative nights
// ============================================================

func TestCheapestHotel_NegativeNights(t *testing.T) {
	t.Parallel()
	price, name := cheapestHotel(context.Background(), "PRG", "2026-05-01", "2026-05-05", -1, nil)
	if price != 0 || name != "" {
		t.Errorf("expected (0, '') for negative nights, got (%v, %q)", price, name)
	}
}

// ============================================================
// Candidate struct — field verification
// ============================================================

func TestCandidateFields(t *testing.T) {
	t.Parallel()
	c := Candidate{
		Start:             "2026-06-01",
		End:               "2026-06-05",
		Nights:            4,
		EstimatedCost:     500,
		FlightCost:        300,
		HotelCost:         200,
		HotelName:         "Hotel Test",
		Currency:          "EUR",
		OverlapsPreferred: true,
		Reasoning:         "test reasoning",
	}
	if c.Nights != 4 {
		t.Errorf("Nights = %d, want 4", c.Nights)
	}
	if c.EstimatedCost != c.FlightCost+c.HotelCost {
		t.Errorf("EstimatedCost (%v) != FlightCost (%v) + HotelCost (%v)", c.EstimatedCost, c.FlightCost, c.HotelCost)
	}
}

// ============================================================
// Interval struct — Reason field
// ============================================================

func TestInterval_WithReason(t *testing.T) {
	t.Parallel()
	iv := Interval{Start: "2026-05-01", End: "2026-05-10", Reason: "vacation"}
	if iv.Reason != "vacation" {
		t.Errorf("Reason = %q, want 'vacation'", iv.Reason)
	}
}

// ============================================================
// Find — context cancellation does not panic
// ============================================================

func TestFind_CancelledContextSafe(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	// Very brief timeout — should not panic.
	_, _ = Find(ctx, Input{
		Origin:      "HEL",
		Destination: "PRG",
		WindowStart: "2026-05-01",
		WindowEnd:   "2026-05-10",
		MinNights:   3,
		MaxNights:   3,
	})
}
