package tripwindow

import (
	"context"
	"strings"
	"testing"
	"time"
)

// ------------------------------------------------------------------ applyDefaults extra branches

func TestApplyDefaults_PreserveExplicit(t *testing.T) {
	t.Parallel()
	in := Input{MinNights: 2, MaxNights: 10, MaxCandidates: 3}
	in.applyDefaults()
	if in.MinNights != 2 {
		t.Errorf("MinNights = %d, want 2", in.MinNights)
	}
	if in.MaxNights != 10 {
		t.Errorf("MaxNights = %d, want 10", in.MaxNights)
	}
	if in.MaxCandidates != 3 {
		t.Errorf("MaxCandidates = %d, want 3", in.MaxCandidates)
	}
}

func TestApplyDefaults_MaxNightsEqualToMin(t *testing.T) {
	t.Parallel()
	in := Input{MinNights: 5, MaxNights: 5}
	in.applyDefaults()
	if in.MaxNights != 5 {
		t.Errorf("MaxNights = %d, want 5", in.MaxNights)
	}
}

// ------------------------------------------------------------------ ValidateInput extra branches

func TestValidateInput_MissingWindowStart(t *testing.T) {
	t.Parallel()
	err := ValidateInput(Input{Destination: "PRG", WindowEnd: "2026-06-30"})
	if err == nil {
		t.Fatal("expected error for missing window_start")
	}
}

func TestValidateInput_InvalidWindowStart(t *testing.T) {
	t.Parallel()
	err := ValidateInput(Input{
		Destination: "PRG",
		WindowStart: "not-a-date",
		WindowEnd:   "2026-06-30",
	})
	if err == nil {
		t.Fatal("expected error for invalid window_start date")
	}
}

func TestValidateInput_InvalidWindowEnd(t *testing.T) {
	t.Parallel()
	err := ValidateInput(Input{
		Destination: "PRG",
		WindowStart: "2026-05-01",
		WindowEnd:   "not-a-date",
	})
	if err == nil {
		t.Fatal("expected error for invalid window_end date")
	}
}

func TestValidateInput_SameDay(t *testing.T) {
	t.Parallel()
	// Same day is valid (end == start, not before start).
	err := ValidateInput(Input{
		Destination: "PRG",
		WindowStart: "2026-05-01",
		WindowEnd:   "2026-05-01",
	})
	if err != nil {
		t.Errorf("unexpected error for same-day window: %v", err)
	}
}

// ------------------------------------------------------------------ ParseBusyFlag extra branches

func TestParseBusyFlag_WrongLength(t *testing.T) {
	t.Parallel()
	// Too short (length != 21).
	_, err := ParseBusyFlag("2026-05-12")
	if err == nil {
		t.Fatal("expected error for too-short string")
	}
}

func TestParseBusyFlag_InvalidEndDate(t *testing.T) {
	t.Parallel()
	_, err := ParseBusyFlag("2026-05-01:2026-99-99")
	if err == nil {
		t.Fatal("expected error for invalid end date")
	}
}

func TestParseBusyFlag_WithReason(t *testing.T) {
	t.Parallel()
	// ParseBusyFlag only populates Start and End, not Reason.
	iv, err := ParseBusyFlag("2026-06-01:2026-06-07")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if iv.Start != "2026-06-01" || iv.End != "2026-06-07" {
		t.Errorf("got %+v", iv)
	}
	if iv.Reason != "" {
		t.Errorf("Reason should be empty, got %q", iv.Reason)
	}
}

// ------------------------------------------------------------------ mustParseIntervals extra branches

func TestMustParseIntervals_EmptySlice(t *testing.T) {
	t.Parallel()
	parsed := mustParseIntervals(nil)
	if len(parsed) != 0 {
		t.Errorf("expected 0 parsed intervals, got %d", len(parsed))
	}
}

func TestMustParseIntervals_SkipsMalformedEnd(t *testing.T) {
	t.Parallel()
	ivs := []Interval{
		{Start: "2026-05-01", End: "bad-end"},
	}
	parsed := mustParseIntervals(ivs)
	if len(parsed) != 0 {
		t.Errorf("expected 0 valid intervals when end is bad, got %d", len(parsed))
	}
}

func TestMustParseIntervals_AllValid(t *testing.T) {
	t.Parallel()
	ivs := []Interval{
		{Start: "2026-05-01", End: "2026-05-10"},
		{Start: "2026-06-01", End: "2026-06-15"},
	}
	parsed := mustParseIntervals(ivs)
	if len(parsed) != 2 {
		t.Errorf("expected 2 valid intervals, got %d", len(parsed))
	}
}

// ------------------------------------------------------------------ overlapsAny extra branches

func TestOverlapsAny_MultipleIntervalsNoOverlap(t *testing.T) {
	t.Parallel()
	ivs := mustParseIntervals([]Interval{
		{Start: "2026-01-01", End: "2026-01-10"},
		{Start: "2026-02-01", End: "2026-02-10"},
	})
	// Trip is in March — no overlap.
	if overlapsAny(testDate("2026-03-05"), testDate("2026-03-10"), ivs) {
		t.Error("expected no overlap with neither interval")
	}
}

func TestOverlapsAny_HitsSecondInterval(t *testing.T) {
	t.Parallel()
	ivs := mustParseIntervals([]Interval{
		{Start: "2026-01-01", End: "2026-01-10"},
		{Start: "2026-05-01", End: "2026-05-20"},
	})
	// Trip overlaps second interval only.
	if !overlapsAny(testDate("2026-05-10"), testDate("2026-05-15"), ivs) {
		t.Error("expected overlap with second interval")
	}
}

func TestOverlapsAny_TripStartsOnIntervalEnd(t *testing.T) {
	t.Parallel()
	ivs := mustParseIntervals([]Interval{
		{Start: "2026-05-01", End: "2026-05-10"},
	})
	// Trip starts exactly when busy interval ends — overlap (inclusive).
	if !overlapsAny(testDate("2026-05-10"), testDate("2026-05-15"), ivs) {
		t.Error("trip start == interval end should overlap (inclusive)")
	}
}

func TestOverlapsAny_TripEndsOnIntervalStart(t *testing.T) {
	t.Parallel()
	ivs := mustParseIntervals([]Interval{
		{Start: "2026-05-15", End: "2026-05-20"},
	})
	// Trip ends exactly when busy interval starts — overlap (inclusive).
	if !overlapsAny(testDate("2026-05-10"), testDate("2026-05-15"), ivs) {
		t.Error("trip end == interval start should overlap (inclusive)")
	}
}

// ------------------------------------------------------------------ buildReasoning extra branches

func TestBuildReasoning_AllCases(t *testing.T) {
	t.Parallel()
	tests := []struct {
		start, end time.Time
		nights     int
		total      float64
		curr       string
		preferred  bool
		wantSubs   []string
	}{
		{
			start:     testDate("2026-06-01"),
			end:       testDate("2026-06-05"),
			nights:    4,
			total:     250,
			curr:      "EUR",
			preferred: false,
			wantSubs:  []string{"Jun", "EUR 250"},
		},
		{
			start:     testDate("2026-07-10"),
			end:       testDate("2026-07-15"),
			nights:    5,
			total:     0,
			curr:      "",
			preferred: false,
			wantSubs:  []string{"price unavailable"},
		},
		{
			start:     testDate("2026-08-01"),
			end:       testDate("2026-08-06"),
			nights:    5,
			total:     300,
			curr:      "EUR",
			preferred: true,
			wantSubs:  []string{"preferred", "EUR 300"},
		},
	}

	for _, tc := range tests {
		r := buildReasoning(tc.start, tc.end, tc.nights, tc.total, tc.curr, tc.preferred)
		for _, sub := range tc.wantSubs {
			if !strings.Contains(r, sub) {
				t.Errorf("buildReasoning(%v,%v,%d,%v,%q,%v) = %q, missing %q",
					tc.start.Format("2006-01-02"), tc.end.Format("2006-01-02"),
					tc.nights, tc.total, tc.curr, tc.preferred, r, sub)
			}
		}
	}
}

// ------------------------------------------------------------------ cheapestHotel (zero-path guards)

func TestCheapestHotel_EmptyDestination(t *testing.T) {
	t.Parallel()
	// Empty dest causes immediate (0, "") return without any HTTP call.
	price, name := cheapestHotel(context.Background(), "", "2026-05-01", "2026-05-05", 4, nil)
	if price != 0 || name != "" {
		t.Errorf("expected (0, '') for empty dest, got (%v, %q)", price, name)
	}
}

func TestCheapestHotel_EmptyCheckIn(t *testing.T) {
	t.Parallel()
	price, name := cheapestHotel(context.Background(), "PRG", "", "2026-05-05", 4, nil)
	if price != 0 || name != "" {
		t.Errorf("expected (0, '') for empty checkIn, got (%v, %q)", price, name)
	}
}

func TestCheapestHotel_EmptyCheckOut(t *testing.T) {
	t.Parallel()
	price, name := cheapestHotel(context.Background(), "PRG", "2026-05-01", "", 4, nil)
	if price != 0 || name != "" {
		t.Errorf("expected (0, '') for empty checkOut, got (%v, %q)", price, name)
	}
}

func TestCheapestHotel_ZeroNights(t *testing.T) {
	t.Parallel()
	price, name := cheapestHotel(context.Background(), "PRG", "2026-05-01", "2026-05-05", 0, nil)
	if price != 0 || name != "" {
		t.Errorf("expected (0, '') for zero nights, got (%v, %q)", price, name)
	}
}

// ------------------------------------------------------------------ cheapestFlightWithBudget (zero-path guards)

func TestCheapestFlightWithBudget_EmptyOrigin(t *testing.T) {
	t.Parallel()
	price, curr := cheapestFlightWithBudget(context.Background(), "", "PRG", "2026-05-01", "2026-05-05", 0)
	if price != 0 || curr != "" {
		t.Errorf("expected (0, '') for empty origin, got (%v, %q)", price, curr)
	}
}

func TestCheapestFlightWithBudget_EmptyDest(t *testing.T) {
	t.Parallel()
	price, curr := cheapestFlightWithBudget(context.Background(), "HEL", "", "2026-05-01", "2026-05-05", 0)
	if price != 0 || curr != "" {
		t.Errorf("expected (0, '') for empty dest, got (%v, %q)", price, curr)
	}
}

func TestCheapestFlightWithBudget_EmptyDepDate(t *testing.T) {
	t.Parallel()
	price, curr := cheapestFlightWithBudget(context.Background(), "HEL", "PRG", "", "2026-05-05", 0)
	if price != 0 || curr != "" {
		t.Errorf("expected (0, '') for empty depDate, got (%v, %q)", price, curr)
	}
}

// ------------------------------------------------------------------ Find validation paths (fast — no network)

func TestFind_InvalidWindowStart(t *testing.T) {
	t.Parallel()
	_, err := Find(context.Background(), Input{
		Destination: "PRG",
		WindowStart: "not-a-date",
		WindowEnd:   "2026-06-30",
	})
	if err == nil {
		t.Fatal("expected error for invalid window_start")
	}
}

func TestFind_InvalidWindowEnd(t *testing.T) {
	t.Parallel()
	_, err := Find(context.Background(), Input{
		Destination: "PRG",
		WindowStart: "2026-05-01",
		WindowEnd:   "not-a-date",
	})
	if err == nil {
		t.Fatal("expected error for invalid window_end")
	}
}

func TestFind_WindowEndBeforeStartExact(t *testing.T) {
	t.Parallel()
	_, err := Find(context.Background(), Input{
		Destination: "PRG",
		WindowStart: "2026-06-15",
		WindowEnd:   "2026-05-01",
	})
	if err == nil {
		t.Fatal("expected error when window_end is before window_start")
	}
}

func TestFind_MissingWindowStart(t *testing.T) {
	t.Parallel()
	_, err := Find(context.Background(), Input{
		Destination: "PRG",
		WindowEnd:   "2026-06-30",
	})
	if err == nil {
		t.Fatal("expected error for missing window_start")
	}
}

func TestFind_MissingWindowEnd(t *testing.T) {
	t.Parallel()
	_, err := Find(context.Background(), Input{
		Destination: "PRG",
		WindowStart: "2026-05-01",
	})
	if err == nil {
		t.Fatal("expected error for missing window_end")
	}
}

// TestFind_AllBusyMalformed verifies that malformed busy intervals are silently
// skipped (not panicking) and that a fully-busy valid window still yields 0 candidates.
func TestFind_AllBusyMalformed(t *testing.T) {
	t.Parallel()
	candidates, err := Find(context.Background(), Input{
		Origin:      "HEL",
		Destination: "PRG",
		WindowStart: "2026-05-01",
		WindowEnd:   "2026-05-10",
		MinNights:   3,
		MaxNights:   3,
		BusyIntervals: []Interval{
			{Start: "bad-date", End: "2026-05-31"},   // malformed — skipped
			{Start: "2026-05-01", End: "2026-05-31"}, // valid — covers whole window
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(candidates) != 0 {
		t.Errorf("expected 0 candidates (valid busy interval covers all), got %d", len(candidates))
	}
}

// TestFind_NarrowWindowContextCancelled verifies Find respects context cancellation
// without hanging or panicking. With a cancelled ctx the goroutines exit via
// ctx.Done() and the function returns.
func TestFind_NarrowWindowContextCancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	// A cancelled context should not cause a panic or hang.
	_, err := Find(ctx, Input{
		Origin:        "HEL",
		Destination:   "PRG",
		WindowStart:   "2026-05-01",
		WindowEnd:     "2026-05-04",
		MinNights:     3,
		MaxNights:     3,
		MaxCandidates: 5,
	})
	_ = err // outcome is non-deterministic; must not panic
}
