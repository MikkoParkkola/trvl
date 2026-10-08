package trip

import (
	"context"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// ============================================================
// airportTransferDepartureMinutes — RFC3339 and fallback paths
// The fast-path covers "YYYY-MM-DDTHH:MM:SS" (len >= 16, ':' at 13).
// The RFC3339 path fires when len >= 16 but ':' is NOT at position 13.
// ============================================================

func TestAirportTransferDepartureMinutes_RFC3339(t *testing.T) {
	// "2026-07-01T09:30:00Z" — len=20, value[13]='3' (not ':') →
	// fast-path fails → time.Parse(RFC3339, value) succeeds.
	mins, ok := airportTransferDepartureMinutes("2026-07-01T09:30:00Z")
	if !ok {
		t.Fatal("expected ok=true for RFC3339 timestamp")
	}
	if mins != 9*60+30 {
		t.Errorf("minutes = %d, want %d", mins, 9*60+30)
	}
}

func TestAirportTransferDepartureMinutes_RFC3339_Midnight(t *testing.T) {
	mins, ok := airportTransferDepartureMinutes("2026-07-01T00:00:00Z")
	if !ok {
		t.Fatal("expected ok=true for RFC3339 midnight")
	}
	if mins != 0 {
		t.Errorf("minutes = %d, want 0", mins)
	}
}

func TestAirportTransferDepartureMinutes_ShortString(t *testing.T) {
	// len < 16 → falls through to RFC3339 parse → fails
	_, ok := airportTransferDepartureMinutes("10:30")
	if ok {
		t.Error("expected ok=false for short string")
	}
}

func TestAirportTransferDepartureMinutes_EmptyString(t *testing.T) {
	_, ok := airportTransferDepartureMinutes("")
	if ok {
		t.Error("expected ok=false for empty string")
	}
}

func TestAirportTransferDepartureMinutes_InvalidLongString(t *testing.T) {
	// Long enough but not a valid RFC3339 timestamp
	_, ok := airportTransferDepartureMinutes("not-a-valid-timestamp!!")
	if ok {
		t.Error("expected ok=false for invalid timestamp")
	}
}

// ============================================================
// convertPlanFlights — zero-price skip path
// (Same-currency skip and empty-currency skip already covered in
//  trip_coverage_test.go; this covers the zero-price guard.)
// ============================================================

func TestConvertPlanFlights_ZeroPriceSkipped(t *testing.T) {
	flights := []PlanFlight{
		{Price: 0, Currency: "USD"},
	}
	ctx := context.Background()
	convertPlanFlights(ctx, flights, "EUR", fakeFX)
	// Zero price should be skipped: currency should not change.
	if flights[0].Currency != "USD" {
		t.Errorf("zero-price flight had currency changed to %q", flights[0].Currency)
	}
}

// ============================================================
// buildInsights — weekday-cheaper-than-weekend branch (diff > 0)
// Complements TestBuildInsights_WeekendCheaperThanWeekday (diff < 0).
// ============================================================

func TestBuildInsights_WeekdayCheaperThanWeekend(t *testing.T) {
	target := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC) // Wednesday
	// Weekday prices lower than weekend → diff > 0 → "Weekday flights average..."
	dates := []models.DatePriceResult{
		{Date: "2026-07-13", Price: 80, Currency: "EUR"},  // Monday (weekday)
		{Date: "2026-07-14", Price: 90, Currency: "EUR"},  // Tuesday (weekday)
		{Date: "2026-07-18", Price: 200, Currency: "EUR"}, // Saturday (weekend)
		{Date: "2026-07-19", Price: 220, Currency: "EUR"}, // Sunday (weekend)
	}

	insights := buildInsights(dates, target, 147.5)

	hasPattern := false
	for _, ins := range insights {
		if ins.Type == "pattern" {
			hasPattern = true
			if ins.Savings <= 0 {
				t.Errorf("expected positive savings for weekday-cheaper pattern, got %v", ins.Savings)
			}
		}
	}
	if !hasPattern {
		t.Error("expected a 'pattern' insight for weekday cheaper than weekend")
	}
}

// ============================================================
// DiscoverOptions.applyDefaults — negative Top input
// ============================================================

func TestDiscoverOptions_ApplyDefaults_NegativeTop(t *testing.T) {
	opts := DiscoverOptions{Top: -5}
	opts.applyDefaults()
	if opts.Top != 5 {
		t.Errorf("Top = %d, want 5 for negative input", opts.Top)
	}
}
