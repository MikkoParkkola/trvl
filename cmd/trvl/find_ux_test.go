package main

import (
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/tripsearch"
)

func TestInferFindArgs_OneArgDestOnly(t *testing.T) {
	t.Parallel()
	origin, dest, date := inferFindArgs([]string{"PRG"})
	if origin != "home" {
		t.Errorf("origin = %q, want 'home' when only destination supplied", origin)
	}
	if dest != "PRG" {
		t.Errorf("dest = %q, want PRG", dest)
	}
	if date == "" {
		t.Errorf("date should be auto-filled, got empty")
	}
	if _, err := time.Parse("2006-01-02", date); err != nil {
		t.Errorf("auto-date %q is not ISO 8601: %v", date, err)
	}
}

func TestInferFindArgs_TwoArgsDateAuto(t *testing.T) {
	t.Parallel()
	origin, dest, date := inferFindArgs([]string{"AMS", "PRG"})
	if origin != "AMS" || dest != "PRG" {
		t.Errorf("got (%q,%q), want (AMS,PRG)", origin, dest)
	}
	if date == "" {
		t.Errorf("date should be auto-filled when two args supplied")
	}
}

func TestInferFindArgs_ThreeArgsPassThrough(t *testing.T) {
	t.Parallel()
	origin, dest, date := inferFindArgs([]string{"home", "PRG", "2026-04-23"})
	if origin != "home" || dest != "PRG" || date != "2026-04-23" {
		t.Errorf("three-arg shape should pass through, got (%q,%q,%q)", origin, dest, date)
	}
}

func TestNextSaturdayISO_AtLeast14DaysOut(t *testing.T) {
	t.Parallel()
	// Start on a Wednesday — next Saturday is 3 days away, but the 14-day
	// buffer must push it out by at least 2 more weeks.
	wed := time.Date(2026, 5, 6, 0, 0, 0, 0, time.UTC) // Wed 2026-05-06
	got := nextSaturdayISO(wed)
	parsed, err := time.Parse("2006-01-02", got)
	if err != nil {
		t.Fatalf("not ISO 8601: %v", err)
	}
	if parsed.Weekday() != time.Saturday {
		t.Errorf("nextSaturday returned %v, want Saturday", parsed.Weekday())
	}
	diff := parsed.Sub(wed).Hours() / 24
	if diff < 14 {
		t.Errorf("nextSaturday %s is only %.0f days out; want >= 14", got, diff)
	}
	if diff > 21 {
		t.Errorf("nextSaturday %s is %.0f days out; want <= 21 (same or next week after 14d buffer)", got, diff)
	}
}

func TestNextSaturdayISO_OnSaturdayGivesFollowingWeek(t *testing.T) {
	t.Parallel()
	// Start on a Saturday — after the 14-day buffer we land on a Saturday
	// exactly; nextSaturdayISO should keep it on that day.
	sat := time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC) // Sat 2026-05-02
	got := nextSaturdayISO(sat)
	parsed, _ := time.Parse("2006-01-02", got)
	if parsed.Weekday() != time.Saturday {
		t.Errorf("expected Saturday, got %v", parsed.Weekday())
	}
	diff := parsed.Sub(sat).Hours() / 24
	if diff < 14 {
		t.Errorf("expected >=14 days out, got %.0f", diff)
	}
}

func TestBaselineDirectPrice_ExcludesRailFly(t *testing.T) {
	t.Parallel()
	flts := []models.FlightResult{
		// Direct AMS origin — counts as baseline.
		{Price: 291, Legs: []models.FlightLeg{{DepartureAirport: models.AirportInfo{Code: "AMS"}}}},
		// Rail+fly BRU — excluded.
		{Price: 159, Legs: []models.FlightLeg{{DepartureAirport: models.AirportInfo{Code: "BRU"}}}},
		// Another direct — cheaper, should become baseline.
		{Price: 250, Legs: []models.FlightLeg{{DepartureAirport: models.AirportInfo{Code: "EIN"}}}},
	}
	got := baselineDirectPrice(flts)
	if got != 250 {
		t.Errorf("baseline = %.0f, want 250 (cheapest non-rail+fly)", got)
	}
}

func TestBaselineDirectPrice_AllRailFlyReturnsZero(t *testing.T) {
	t.Parallel()
	flts := []models.FlightResult{
		{Price: 159, Legs: []models.FlightLeg{{DepartureAirport: models.AirportInfo{Code: "BRU"}}}},
		{Price: 170, Legs: []models.FlightLeg{{DepartureAirport: models.AirportInfo{Code: "ZYR"}}}},
	}
	if got := baselineDirectPrice(flts); got != 0 {
		t.Errorf("expected 0 when all bundles are rail+fly, got %.0f", got)
	}
}

func TestApplyRelax_TogglesCorrectFilters(t *testing.T) {
	t.Parallel()
	req := tripsearch.Request{LoungeRequired: true, NoEarlyConnection: true, MinLayoverMinutes: 720, LayoverAirports: []string{"AMS"}}
	applyRelax(&req, []string{"lounge", "no-early-connection", "layover"})
	if req.LoungeRequired {
		t.Error("lounge should be relaxed")
	}
	if req.NoEarlyConnection {
		t.Error("no-early-connection should be relaxed")
	}
	if req.MinLayoverMinutes != 0 || len(req.LayoverAirports) != 0 {
		t.Errorf("layover should be cleared, got mins=%d airports=%v", req.MinLayoverMinutes, req.LayoverAirports)
	}
}

func TestApplyRelax_UnrecognisedNameIsSkipped(t *testing.T) {
	t.Parallel()
	req := tripsearch.Request{LoungeRequired: true}
	applyRelax(&req, []string{"bogus-filter-name", "lounge"})
	if req.LoungeRequired {
		t.Error("known name after bogus should still apply")
	}
}

func TestSweepSaturdays_CapsAndAlignsToSaturday(t *testing.T) {
	t.Parallel()
	// Wed 2026-05-06 + 30d window, cap 4.
	got := sweepSaturdays("2026-05-06", 30, 4)
	if len(got) == 0 || len(got) > 4 {
		t.Fatalf("got %d dates, want 1..4: %v", len(got), got)
	}
	for _, d := range got {
		dt, err := time.Parse("2006-01-02", d)
		if err != nil {
			t.Fatalf("date %q not ISO 8601: %v", d, err)
		}
		if dt.Weekday() != time.Saturday {
			t.Errorf("date %q is %v, not Saturday", d, dt.Weekday())
		}
	}
	// First date must be at/after the input Wed.
	if got[0] < "2026-05-06" {
		t.Errorf("first sweep date %q should be on/after 2026-05-06", got[0])
	}
}

func TestSweepSaturdays_BadInputReturnsAsIs(t *testing.T) {
	t.Parallel()
	got := sweepSaturdays("not-a-date", 30, 4)
	if len(got) != 1 || got[0] != "not-a-date" {
		t.Errorf("bad input should round-trip as singleton: %v", got)
	}
}
