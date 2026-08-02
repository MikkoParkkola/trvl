package counterfactual

import (
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/pricesignal"
)

var asOf = time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)

// TRVL.CF.1: shift-day deltas computed purely from an already-fetched grid,
// zero provider calls (the function cannot reach the network).
func TestShiftDayFromGrid(t *testing.T) {
	grid := []models.DatePriceResult{
		{Date: "2026-07-14", Price: 90, Currency: "EUR"},
		{Date: "2026-07-15", Price: 150, Currency: "EUR"}, // current
		{Date: "2026-07-16", Price: 140, Currency: "EUR"},
		{Date: "2026-07-17", Price: 200, Currency: "EUR"},
	}
	out := ShiftDay(grid, "2026-07-15", 5, asOf)
	if len(out) != 2 {
		t.Fatalf("want 2 cheaper dates, got %d (%+v)", len(out), out)
	}
	// Sorted by biggest saving first: 14th (-60) before 16th (-10).
	if out[0].Amount != 60 || out[0].Kind != KindShiftDay {
		t.Fatalf("first saving should be 60 shift_day, got %+v", out[0])
	}
	if !out[0].CallFree {
		t.Fatalf("Tier-0 saving must be marked call-free")
	}
	if !out[0].AsOf.Equal(asOf) {
		t.Fatalf("as-of must be carried for staleness")
	}
}

func TestShiftDayNoCurrentDate(t *testing.T) {
	grid := []models.DatePriceResult{{Date: "2026-07-14", Price: 90, Currency: "EUR"}}
	if out := ShiftDay(grid, "2026-07-15", 5, asOf); out != nil {
		t.Fatalf("missing current date must yield nil, got %+v", out)
	}
}

func TestSameDayAlternative(t *testing.T) {
	// Headline (first) is pricier than a later, cheaper fare -> real saving.
	// Mimics a duration/time-sorted list where the fastest is not the cheapest.
	flights := []models.FlightResult{
		{Price: 220, Currency: "EUR"}, // headline (e.g. fastest)
		{Price: 150, Currency: "EUR"}, // cheapest
		{Price: 180, Currency: "EUR"},
	}
	s := SameDayAlternative(flights, 10, asOf)
	if s == nil || s.Amount != 70 {
		t.Fatalf("want headline-vs-cheapest saving 70, got %+v", s)
	}

	// Already cheapest-first -> headline IS cheapest -> no illusory saving.
	sorted := []models.FlightResult{
		{Price: 150, Currency: "EUR"},
		{Price: 180, Currency: "EUR"},
		{Price: 220, Currency: "EUR"},
	}
	if SameDayAlternative(sorted, 10, asOf) != nil {
		t.Fatalf("cheapest-first list must yield no saving (honesty)")
	}

	// Single flight or sub-minDelta -> nil.
	if SameDayAlternative(flights[:1], 10, asOf) != nil {
		t.Fatalf("single flight must yield nil")
	}
	if SameDayAlternative([]models.FlightResult{{Price: 155}, {Price: 150}}, 10, asOf) != nil {
		t.Fatalf("delta below minDelta must yield nil")
	}
}

// TestShiftDayCrossCurrencyMismatch: round 25 (GPT + Grok convergent
// second-opinion review, 2026-07-31) -- an unlabeled grid row must never be
// compared by raw magnitude against a labeled reference currency, in either
// direction, and a known currency mismatch must never produce a saving.
func TestShiftDayCrossCurrencyMismatch(t *testing.T) {
	// Labeled reference, unlabeled cheaper row -> no fabricated saving.
	blankCheaper := []models.DatePriceResult{
		{Date: "2026-07-14", Price: 80},                   // no currency
		{Date: "2026-07-15", Price: 200, Currency: "EUR"}, // current
	}
	if out := ShiftDay(blankCheaper, "2026-07-15", 5, asOf); out != nil {
		t.Fatalf("labeled-vs-blank must yield no saving, got %+v", out)
	}

	// Unlabeled reference, labeled cheaper row -> no fabricated saving.
	refBlank := []models.DatePriceResult{
		{Date: "2026-07-14", Price: 80, Currency: "EUR"},
		{Date: "2026-07-15", Price: 200}, // current, no currency
	}
	if out := ShiftDay(refBlank, "2026-07-15", 5, asOf); out != nil {
		t.Fatalf("blank-vs-labeled must yield no saving, got %+v", out)
	}

	// Known mismatch -> no saving (already covered by round 24, kept for
	// regression alongside the new blank-vs-labeled cases above).
	mismatch := []models.DatePriceResult{
		{Date: "2026-07-14", Price: 80, Currency: "JPY"},
		{Date: "2026-07-15", Price: 200, Currency: "EUR"},
	}
	if out := ShiftDay(mismatch, "2026-07-15", 5, asOf); out != nil {
		t.Fatalf("cross-currency mismatch must yield no saving, got %+v", out)
	}

	// Both unlabeled -> saving still allowed (documented unknown-unknown
	// convention: two currencyless rows are treated as compatible).
	bothBlank := []models.DatePriceResult{
		{Date: "2026-07-14", Price: 80},
		{Date: "2026-07-15", Price: 200}, // current
	}
	if out := ShiftDay(bothBlank, "2026-07-15", 5, asOf); len(out) != 1 || out[0].Amount != 120 {
		t.Fatalf("both-blank rows should still allow a saving, got %+v", out)
	}
}

// TestSameDayAlternativeCrossCurrencyMismatch: same bug class, same fix, on
// the same-day selector. Round 25.
func TestSameDayAlternativeCrossCurrencyMismatch(t *testing.T) {
	// Labeled headline, unlabeled cheaper candidate -> must not win.
	blankCandidate := []models.FlightResult{
		{Price: 200, Currency: "EUR"},
		{Price: 50}, // cheaper by raw number, no currency
	}
	if s := SameDayAlternative(blankCandidate, 10, asOf); s != nil {
		t.Fatalf("labeled headline vs blank candidate must yield no saving, got %+v", s)
	}

	// Unlabeled headline, labeled cheaper candidate -> must not win.
	blankHeadline := []models.FlightResult{
		{Price: 200}, // no currency
		{Price: 50, Currency: "EUR"},
	}
	if s := SameDayAlternative(blankHeadline, 10, asOf); s != nil {
		t.Fatalf("blank headline vs labeled candidate must yield no saving, got %+v", s)
	}

	// Known mismatch -> no saving.
	mismatch := []models.FlightResult{
		{Price: 200, Currency: "EUR"},
		{Price: 50, Currency: "JPY"},
	}
	if s := SameDayAlternative(mismatch, 10, asOf); s != nil {
		t.Fatalf("cross-currency mismatch must yield no saving, got %+v", s)
	}

	// Both unlabeled -> saving still allowed (same documented unknown-unknown
	// convention as ShiftDay's bothBlank case above). Grok round-25 optional
	// finding #4: ShiftDay had an explicit positive both-blank test but
	// SameDayAlternative did not, even though the production logic (line
	// `fCur != headlineCur`, "" == "" is true) already takes this path --
	// parity-only, no behavior change. Fixed as trvl#548.
	bothBlank := []models.FlightResult{
		{Price: 220}, // headline, no currency
		{Price: 150}, // cheaper, no currency
	}
	if s := SameDayAlternative(bothBlank, 10, asOf); s == nil || s.Amount != 70 {
		t.Fatalf("both-blank flights should still allow a saving, got %+v", s)
	}
}

func TestVsHistoryHonesty(t *testing.T) {
	// Not confident -> no claim.
	if VsHistory(&pricesignal.Position{Confident: false, Median: 200, Current: 150}, "EUR", asOf) != nil {
		t.Fatalf("non-confident position must not produce a history claim")
	}
	// Confident and below median -> saving.
	s := VsHistory(&pricesignal.Position{Confident: true, Median: 200, Current: 150, Observations: 12}, "EUR", asOf)
	if s == nil || s.Amount != 50 || s.Kind != KindVsHistory {
		t.Fatalf("want 50 vs_history saving, got %+v", s)
	}
	// Confident but at/above median -> nil (no fake saving).
	if VsHistory(&pricesignal.Position{Confident: true, Median: 200, Current: 220}, "EUR", asOf) != nil {
		t.Fatalf("price above median must not produce a saving")
	}
	if VsHistory(nil, "EUR", asOf) != nil {
		t.Fatalf("nil position must yield nil")
	}
}
