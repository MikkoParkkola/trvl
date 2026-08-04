package counterfactual

import (
	"strings"
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
	// Currencies are labeled deliberately. Round 26 made a blank currency its own
	// reason to return nil, so an unlabeled fixture here would pass whether or not
	// minDelta worked at all -- the assertion would read as evidence while testing
	// nothing. Labeling them keeps the 5-below-10 delta the only thing that can
	// produce the nil.
	if SameDayAlternative([]models.FlightResult{
		{Price: 155, Currency: "EUR"},
		{Price: 150, Currency: "EUR"},
	}, 10, asOf) != nil {
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

	// Both unlabeled -> NO saving (trvl#549).
	//
	// This previously asserted the opposite, on a documented
	// unknown-unknown-is-compatible convention. Two rows can be currencyless
	// for unrelated reasons -- one provider omitting the currency on a EUR
	// quote, another on a JPY one -- so "both blank" is not evidence they share
	// a unit, it is the absence of evidence either way.
	//
	// Saving.Amount is money saved IN Currency. With no currency the number has
	// no defined unit and cannot honestly be shown: the description rendered
	// "save 120 " with a trailing space.
	bothBlank := []models.DatePriceResult{
		{Date: "2026-07-14", Price: 80},
		{Date: "2026-07-15", Price: 200}, // current
	}
	if out := ShiftDay(bothBlank, "2026-07-15", 5, asOf); len(out) != 0 {
		t.Fatalf("two currencyless rows produced a saving with no unit: %+v", out)
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

	// Both unlabeled -> NO saving (trvl#549), same reasoning as ShiftDay. It
	// bites harder here: this input is a flight result list, which can span
	// several providers, so two blank rows are more likely to be genuinely
	// different currencies than in a single date grid.
	//
	// trvl#548 asked for this case as a PARITY test asserting the old
	// "saving still allowed" behaviour. That expectation is now the bug, so the
	// case survives with its assertion inverted rather than as written.
	bothBlank := []models.FlightResult{
		{Price: 220}, // headline, no currency
		{Price: 150}, // cheaper, no currency
	}
	if s := SameDayAlternative(bothBlank, 10, asOf); s != nil {
		t.Fatalf("two currencyless flights produced a saving with no unit: %+v", s)
	}
}

// TRVL.549.SAMEDAY.THRESHOLD -- the blank-currency refusal must not depend on
// the value of minDelta.
//
// The guards above skip mismatched candidates, so a blank headline leaves
// `cheapest` equal to `headline` and a delta of 0. That returned nil only
// because `0 < minDelta` holds for a positive threshold. At minDelta 0 or below
// the function fell through and emitted Saving{Amount: 0, Currency: ""} --
// rendering "The cheapest same-day fare (200 ) saves 0 " -- which is precisely
// the unlabeled-money defect #549 exists to prevent, reachable through the one
// parameter nobody thought was load-bearing for currency handling.
//
// Not reachable from the shipped path: the only production caller passes
// MinDelta = 10 (pricefeed.go:29). Pinned anyway, because SameDayAlternative is
// exported and a guarantee that holds only while a constant in another package
// keeps its value is not a guarantee.
//
// Found by GPT second-opinion review; Grok had rated the same early-exit as
// optional polish and returned SHIP without it.
func TestSameDayBlankHeadlineRefusedAtAnyThreshold(t *testing.T) {
	blankHeadline := []models.FlightResult{
		{Price: 200}, // headline, no currency
		{Price: 150}, // cheaper, no currency
	}

	for _, minDelta := range []float64{10, 1, 0, -1} {
		if s := SameDayAlternative(blankHeadline, minDelta, asOf); s != nil {
			t.Errorf("minDelta=%v produced %+v -- an unlabeled headline must be refused on its own "+
				"terms, not by whether the threshold happens to catch a zero delta", minDelta, s)
		}
	}

	// A labeled pair at the same thresholds still works, so the guard above
	// refuses blankness rather than quietly disabling the whole function.
	labeled := []models.FlightResult{
		{Price: 200, Currency: "EUR"},
		{Price: 150, Currency: "EUR"},
	}
	if s := SameDayAlternative(labeled, 0, asOf); s == nil || s.Amount != 50 || s.Currency != "EUR" {
		t.Errorf("labeled pair at minDelta=0 = %+v, want a 50 EUR saving -- the fix must not "+
			"suppress comparable fares", s)
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

// TRVL.549.VSHISTORY.1 -- the third emission site must refuse a blank currency
// like the other two.
//
// This is reachable on the live path: pricefeed.Flight passes cheapestFlight's
// raw f.Currency, which is empty whenever the cheapest provider omits the field.
// The old code emitted Amount=50 with Currency="", rendering "is 50  below this
// route's typical" -- a money figure with no unit, and a double space where the
// currency belongs.
//
// It is also the weaker claim of the two: the median comes from
// RoutePrices(key, ""), which since trvl#564 exact-matches blank, so the series
// pools every currencyless observation on the route and those can be different
// real currencies. Refusing costs a claim that could not have been rendered
// honestly anyway.
func TestVsHistoryRefusesBlankCurrency(t *testing.T) {
	pos := &pricesignal.Position{Confident: true, Median: 200, Current: 150, Observations: 12}

	for _, blank := range []string{"", "   "} {
		if s := VsHistory(pos, blank, asOf); s != nil {
			t.Errorf("VsHistory(%q) = %+v, want nil -- an unlabeled amount is not money, and the "+
				"median behind it pools possibly-different currencies", blank, s)
		}
	}
}

// TRVL.549.VSHISTORY.2 -- and it must normalize casing, as the other two sites
// do. This site emitted the caller's raw string, so a provider reporting "eur"
// produced a lowercase Currency while an identical fare via ShiftDay produced
// "EUR" (the round-25 inconsistency, never applied here).
func TestVsHistoryNormalizesCurrencyCasing(t *testing.T) {
	s := VsHistory(&pricesignal.Position{Confident: true, Median: 200, Current: 150, Observations: 12}, " eur ", asOf)
	if s == nil {
		t.Fatal("a padded, lowercase but perfectly valid currency must still yield a saving")
	}
	if s.Currency != "EUR" {
		t.Errorf("Currency = %q, want EUR -- the other two sites always emit uppercase", s.Currency)
	}
	if !strings.Contains(s.Description, "50 EUR") {
		t.Errorf("description carries the unnormalized currency: %q", s.Description)
	}
}
