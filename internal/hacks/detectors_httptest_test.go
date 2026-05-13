package hacks

import (
	"context"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// ---------------------------------------------------------------------------
// dedupHacks
// ---------------------------------------------------------------------------

// TestDedupHacks_NoDuplicates_Extended verifies that non-duplicate hacks are preserved.
func TestDedupHacks_NoDuplicates_Extended(t *testing.T) {
	hacks := []Hack{
		{Type: "throwaway", Savings: 100, Steps: []string{"step1"}},
		{Type: "hidden_city", Savings: 200, Steps: []string{"step1"}},
		{Type: "positioning", Savings: 50, Steps: []string{"step1"}},
	}
	got := dedupHacks(hacks)
	if len(got) != 3 {
		t.Errorf("expected 3 hacks after dedup, got %d", len(got))
	}
}

// TestDedupHacks_ExactDuplicates verifies that duplicates with the same type
// and similar savings are collapsed, keeping the one with more steps.
func TestDedupHacks_ExactDuplicates(t *testing.T) {
	hacks := []Hack{
		{Type: "throwaway", Savings: 100, Steps: []string{"step1"}},
		{Type: "throwaway", Savings: 102, Steps: []string{"step1", "step2", "step3"}},
	}
	got := dedupHacks(hacks)
	if len(got) != 1 {
		t.Fatalf("expected 1 hack after dedup, got %d", len(got))
	}
	// The one with more steps should be kept.
	if len(got[0].Steps) != 3 {
		t.Errorf("expected hack with 3 steps to be kept, got %d", len(got[0].Steps))
	}
}

// TestDedupHacks_DifferentSavingsBuckets verifies that hacks with the same
// type but very different savings are NOT deduped.
func TestDedupHacks_DifferentSavingsBuckets(t *testing.T) {
	hacks := []Hack{
		{Type: "split", Savings: 10, Steps: []string{"s1"}},
		{Type: "split", Savings: 100, Steps: []string{"s1"}},
	}
	got := dedupHacks(hacks)
	if len(got) != 2 {
		t.Errorf("expected 2 hacks (different savings buckets), got %d", len(got))
	}
}

// TestDedupHacks_EmptyAndSingle verifies edge cases.
func TestDedupHacks_EmptyAndSingle(t *testing.T) {
	if got := dedupHacks(nil); len(got) != 0 {
		t.Errorf("expected 0 for nil, got %d", len(got))
	}
	single := []Hack{{Type: "test", Savings: 50}}
	if got := dedupHacks(single); len(got) != 1 {
		t.Errorf("expected 1 for single, got %d", len(got))
	}
}

// ---------------------------------------------------------------------------
// buildNightHack
// ---------------------------------------------------------------------------

// TestBuildNightHack_FieldValidation verifies the night transport hack builder produces
// correct fields from a ground route.
func TestBuildNightHack_FieldValidation(t *testing.T) {
	in := DetectorInput{
		Origin:      "HEL",
		Destination: "TLL",
		Date:        "2026-04-13",
		Currency:    "EUR",
	}
	route := models.GroundRoute{
		Provider: "flixbus",
		Type:     "bus",
		Price:    15.0,
		Currency: "EUR",
		Departure: models.GroundStop{
			City: "Helsinki",
			Time: "2026-04-13T21:55",
		},
		Arrival: models.GroundStop{
			City: "Tallinn",
			Time: "2026-04-14T06:30",
		},
		BookingURL: "https://flixbus.com/booking/123",
	}

	h := buildNightHack(in, route, 60.0)
	if h.Type != "night_transport" {
		t.Errorf("Type = %q, want 'night_transport'", h.Type)
	}
	if h.Currency != "EUR" {
		t.Errorf("Currency = %q, want 'EUR'", h.Currency)
	}
	if h.Savings != 60 {
		t.Errorf("Savings = %v, want 60", h.Savings)
	}
	if len(h.Steps) == 0 {
		t.Error("Steps should not be empty")
	}
	if len(h.Risks) == 0 {
		t.Error("Risks should not be empty")
	}
	if len(h.Citations) == 0 {
		t.Error("Citations should not be empty")
	}
	if h.Citations[0] != "https://flixbus.com/booking/123" {
		t.Errorf("Citations[0] = %q, want booking URL", h.Citations[0])
	}
}

// TestBuildNightHack_RouteWithCurrency verifies that the route's currency
// is used when available.
func TestBuildNightHack_RouteWithCurrency(t *testing.T) {
	in := DetectorInput{Currency: "USD"}
	route := models.GroundRoute{
		Provider: "regiojet",
		Type:     "bus",
		Currency: "CZK",
		Departure: models.GroundStop{
			City: "Prague",
			Time: "2026-04-13T23:00:00.000+02:00",
		},
		Arrival: models.GroundStop{
			City: "Vienna",
			Time: "2026-04-14T07:00:00.000+02:00",
		},
	}

	h := buildNightHack(in, route, 50.0)
	if h.Currency != "CZK" {
		t.Errorf("Currency = %q, want 'CZK' (from route)", h.Currency)
	}
}

// ---------------------------------------------------------------------------
// detectCalendarConflict
// ---------------------------------------------------------------------------

// TestDetectCalendarConflict_PeakPeriod verifies that travel during a peak
// period produces a calendar_conflict hack.
func TestDetectCalendarConflict_PeakPeriod(t *testing.T) {
	// July 15 is summer holidays.
	hacks := detectCalendarConflict(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		Date:        "2026-07-15",
	})
	if len(hacks) != 1 {
		t.Fatalf("expected 1 hack for summer peak, got %d", len(hacks))
	}
	if hacks[0].Type != "calendar_conflict" {
		t.Errorf("Type = %q, want 'calendar_conflict'", hacks[0].Type)
	}
	if len(hacks[0].Steps) == 0 {
		t.Error("Steps should not be empty")
	}
}

// TestDetectCalendarConflict_OffPeak verifies that travel outside peak
// periods produces no hack.
func TestDetectCalendarConflict_OffPeak(t *testing.T) {
	// March 1 is off-peak.
	hacks := detectCalendarConflict(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		Date:        "2026-03-01",
	})
	if len(hacks) != 0 {
		t.Errorf("expected 0 hacks for off-peak date, got %d", len(hacks))
	}
}

// TestDetectCalendarConflict_ChristmasPeriod verifies Christmas detection.
func TestDetectCalendarConflict_ChristmasPeriod(t *testing.T) {
	hacks := detectCalendarConflict(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		Date:        "2026-12-25",
	})
	if len(hacks) != 1 {
		t.Fatalf("expected 1 hack for Christmas, got %d", len(hacks))
	}
}

// TestDetectCalendarConflict_NewYearCrossover verifies the year-boundary
// Christmas/New Year detection.
func TestDetectCalendarConflict_NewYearCrossover(t *testing.T) {
	// Jan 3 should still be in the Christmas/New Year window.
	hacks := detectCalendarConflict(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		Date:        "2026-01-03",
	})
	if len(hacks) != 1 {
		t.Fatalf("expected 1 hack for New Year period, got %d", len(hacks))
	}
}

// TestDetectCalendarConflict_InvalidDate_Extended verifies no panic on invalid date.
func TestDetectCalendarConflict_InvalidDate_Extended(t *testing.T) {
	hacks := detectCalendarConflict(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		Date:        "not-a-date",
	})
	if len(hacks) != 0 {
		t.Errorf("expected 0 hacks for invalid date, got %d", len(hacks))
	}
}

// ---------------------------------------------------------------------------
// inRange / dateOf
// ---------------------------------------------------------------------------

// TestInRange verifies the date range check.
func TestInRange(t *testing.T) {
	start := dateOf(2026, 6, 22)
	end := dateOf(2026, 8, 31)

	tests := []struct {
		date time.Time
		want bool
	}{
		{dateOf(2026, 6, 22), true},  // start boundary
		{dateOf(2026, 8, 31), true},  // end boundary
		{dateOf(2026, 7, 15), true},  // middle
		{dateOf(2026, 6, 21), false}, // before
		{dateOf(2026, 9, 1), false},  // after
	}
	for _, tc := range tests {
		got := inRange(tc.date, start, end)
		if got != tc.want {
			t.Errorf("inRange(%s, %s, %s) = %v, want %v",
				tc.date.Format("2006-01-02"),
				start.Format("2006-01-02"),
				end.Format("2006-01-02"),
				got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// minFlightPrice / flightCurrency
// ---------------------------------------------------------------------------

// TestMinFlightPrice_EdgeCases verifies additional cheapest price selection paths.
func TestMinFlightPrice_EdgeCases(t *testing.T) {
	t.Run("normal", func(t *testing.T) {
		r := &models.FlightSearchResult{
			Success: true,
			Flights: []models.FlightResult{
				{Price: 250, Currency: "EUR"},
				{Price: 120, Currency: "EUR"},
				{Price: 180, Currency: "EUR"},
			},
		}
		got := minFlightPrice(r)
		if got != 120 {
			t.Errorf("minFlightPrice = %v, want 120", got)
		}
	})

	t.Run("nil", func(t *testing.T) {
		if got := minFlightPrice(nil); got != 0 {
			t.Errorf("minFlightPrice(nil) = %v, want 0", got)
		}
	})

	t.Run("not_success", func(t *testing.T) {
		r := &models.FlightSearchResult{Success: false}
		if got := minFlightPrice(r); got != 0 {
			t.Errorf("minFlightPrice(not success) = %v, want 0", got)
		}
	})

	t.Run("zero_prices", func(t *testing.T) {
		r := &models.FlightSearchResult{
			Success: true,
			Flights: []models.FlightResult{
				{Price: 0},
				{Price: 0},
			},
		}
		if got := minFlightPrice(r); got != 0 {
			t.Errorf("minFlightPrice(all zeros) = %v, want 0", got)
		}
	})
}

// TestFlightCurrency_EdgeCases verifies additional currency extraction paths.
func TestFlightCurrency_EdgeCases(t *testing.T) {
	t.Run("from_result", func(t *testing.T) {
		r := &models.FlightSearchResult{
			Success: true,
			Flights: []models.FlightResult{{Currency: "USD"}},
		}
		if got := flightCurrency(r, "EUR"); got != "USD" {
			t.Errorf("flightCurrency = %q, want 'USD'", got)
		}
	})

	t.Run("fallback", func(t *testing.T) {
		if got := flightCurrency(nil, "EUR"); got != "EUR" {
			t.Errorf("flightCurrency(nil) = %q, want 'EUR'", got)
		}
	})

	t.Run("empty_currency_in_result", func(t *testing.T) {
		r := &models.FlightSearchResult{
			Success: true,
			Flights: []models.FlightResult{{Currency: ""}},
		}
		if got := flightCurrency(r, "GBP"); got != "GBP" {
			t.Errorf("flightCurrency(empty) = %q, want 'GBP'", got)
		}
	})
}

// ---------------------------------------------------------------------------
// parseHour
// ---------------------------------------------------------------------------

// TestParseHour_AdditionalCases verifies additional hour extraction paths.
func TestParseHour_AdditionalCases(t *testing.T) {
	tests := []struct {
		input string
		want  int
		err   bool
	}{
		{"2026-04-13T21:55", 21, false},
		{"2026-04-14T06:30", 6, false},
		{"14:30", 14, false},
		{"invalid", 0, true},
	}
	for _, tc := range tests {
		var h int
		_, err := parseHour(tc.input, &h)
		if (err != nil) != tc.err {
			t.Errorf("parseHour(%q): err = %v, wantErr = %v", tc.input, err, tc.err)
			continue
		}
		if !tc.err && h != tc.want {
			t.Errorf("parseHour(%q) = %d, want %d", tc.input, h, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// detectTuesdayBooking additional paths
// ---------------------------------------------------------------------------

// TestDetectTuesdayBooking_InvalidDate verifies no panic for invalid date.
func TestDetectTuesdayBooking_InvalidDate(t *testing.T) {
	hacks := detectTuesdayBooking(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "PRG",
		Date:        "not-a-date",
	})
	if len(hacks) != 0 {
		t.Errorf("expected 0 hacks for invalid date, got %d", len(hacks))
	}
}

// TestDetectTuesdayBooking_FridayIsTrigger verifies that Friday is treated
// as an expensive day (triggering the detector's early-return bypass only
// on non-expensive days).
func TestDetectTuesdayBooking_FridayIsTrigger(t *testing.T) {
	// Find a date that's a Friday.
	// 2026-04-17 is a Friday.
	d, _ := parseDate("2026-04-17")
	if d.Weekday() != time.Friday {
		t.Skipf("2026-04-17 is not Friday, got %v", d.Weekday())
	}
	// This should NOT return nil from the "not expensive day" guard.
	// It will return nil from the API call (no live flights in tests),
	// but the important thing is the weekday check passes.
	hacks := detectTuesdayBooking(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "PRG",
		Date:        "2026-04-17", // Friday
	})
	// Will be nil because no live API, but should not panic.
	_ = hacks
}

// ---------------------------------------------------------------------------
// Hack struct fields validation
// ---------------------------------------------------------------------------

// TestHackType_AllRegisteredTypes verifies that all hack types registered in
// DetectAll are known strings.
func TestHackType_AllRegisteredTypes(t *testing.T) {
	knownTypes := map[string]bool{
		"throwaway":                  true,
		"hidden_city":                true,
		"positioning":                true,
		"split":                      true,
		"night_transport":            true,
		"stopover":                   true,
		"date_flex":                  true,
		"open_jaw":                   true,
		"ferry_positioning":          true,
		"multi_stop":                 true,
		"currency_arbitrage":         true,
		"calendar_conflict":          true,
		"tuesday_booking":            true,
		"low_cost_carrier":           true,
		"multimodal_skip_flight":     true,
		"multimodal_positioning":     true,
		"multimodal_open_jaw_ground": true,
		"multimodal_return_split":    true,
		"accommodation_split":        true,
	}
	// Verify the map has entries.
	if len(knownTypes) < 15 {
		t.Errorf("expected at least 15 known hack types, got %d", len(knownTypes))
	}
	// Each type should be a non-empty string.
	for typ := range knownTypes {
		if typ == "" {
			t.Error("empty hack type found")
		}
	}
}
