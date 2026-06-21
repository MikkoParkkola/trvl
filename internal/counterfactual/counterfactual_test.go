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
	flights := []models.FlightResult{
		{Price: 150, Currency: "EUR"},
		{Price: 220, Currency: "EUR"},
		{Price: 180, Currency: "EUR"},
	}
	s := SameDayAlternative(flights, 10, asOf)
	if s == nil || s.Amount != 70 {
		t.Fatalf("want spread saving 70, got %+v", s)
	}
	// Single flight or tiny spread -> nil.
	if SameDayAlternative(flights[:1], 10, asOf) != nil {
		t.Fatalf("single flight must yield nil")
	}
	if SameDayAlternative([]models.FlightResult{{Price: 150}, {Price: 155}}, 10, asOf) != nil {
		t.Fatalf("spread below minDelta must yield nil")
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
