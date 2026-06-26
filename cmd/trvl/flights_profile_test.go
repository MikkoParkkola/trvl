package main

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/preferences"
)

// makeFlight builds a minimal priced one-way FlightResult for filter tests.
func makeFlight(price float64, departHHMM string) models.FlightResult {
	return models.FlightResult{
		Price:    price,
		Currency: "EUR",
		Legs: []models.FlightLeg{{
			DepartureTime: "2026-06-15T" + departHHMM,
		}},
	}
}

// TestApplyFlightProfileFiltersBudget proves the CLI applies the traveller's
// saved BudgetFlightMax as a post-search filter, dropping over-budget flights —
// the parity behaviour the CLI previously lacked (the MCP surface already did
// this in mcp/tools_flights.go).
func TestApplyFlightProfileFiltersBudget(t *testing.T) {
	prefs := preferences.Default()
	prefs.BudgetFlightMax = 500

	result := &models.FlightSearchResult{
		Success: true,
		Count:   3,
		Flights: []models.FlightResult{
			makeFlight(300, "10:00"), // under budget — kept
			makeFlight(500, "11:00"), // exactly at budget — kept
			makeFlight(900, "12:00"), // over budget — filtered
		},
	}

	// No explicit --max-price flag, so the profile budget applies.
	applyFlightProfileFilters(result, prefs, false)

	if result.Count != 2 {
		t.Fatalf("expected Count 2 after budget filter, got %d", result.Count)
	}
	if len(result.Flights) != 2 {
		t.Fatalf("expected 2 flights after budget filter, got %d", len(result.Flights))
	}
	for _, f := range result.Flights {
		if f.Price > prefs.BudgetFlightMax {
			t.Errorf("flight priced %.0f exceeds profile budget %.0f and should have been filtered", f.Price, prefs.BudgetFlightMax)
		}
	}
}

// TestApplyFlightProfileFiltersExplicitFlagWins proves an explicit --max-price
// command-line flag overrides the saved profile budget: when budgetFlagSet is
// true the profile BudgetFlightMax fallback is skipped, so a stored preference
// never silently overrides what the user typed.
func TestApplyFlightProfileFiltersExplicitFlagWins(t *testing.T) {
	prefs := preferences.Default()
	prefs.BudgetFlightMax = 500

	result := &models.FlightSearchResult{
		Success: true,
		Count:   2,
		Flights: []models.FlightResult{
			makeFlight(300, "10:00"),
			makeFlight(900, "12:00"), // over the profile cap, but the user passed an explicit flag
		},
	}

	// Explicit --max-price flag set: the profile budget post-filter is skipped
	// (the explicit value is already applied server-side via opts.MaxPrice).
	applyFlightProfileFilters(result, prefs, true)

	if result.Count != 2 {
		t.Fatalf("expected explicit flag to skip the profile budget filter (Count 2), got %d", result.Count)
	}
	if len(result.Flights) != 2 {
		t.Fatalf("expected both flights retained when explicit flag is set, got %d", len(result.Flights))
	}
}

// TestApplyFlightProfileFiltersTimeWindow proves the preferred departure-time
// window from the profile is applied, matching MCP parity.
func TestApplyFlightProfileFiltersTimeWindow(t *testing.T) {
	prefs := preferences.Default()
	prefs.BudgetFlightMax = 0 // no budget cap, isolate the time-window behaviour
	prefs.FlightTimeEarliest = "08:00"
	prefs.FlightTimeLatest = "20:00"

	result := &models.FlightSearchResult{
		Success: true,
		Count:   3,
		Flights: []models.FlightResult{
			makeFlight(300, "06:00"), // before window — filtered
			makeFlight(300, "10:00"), // within window — kept
			makeFlight(300, "22:00"), // after window — filtered
		},
	}

	applyFlightProfileFilters(result, prefs, false)

	if result.Count != 1 {
		t.Fatalf("expected Count 1 after time-window filter, got %d", result.Count)
	}
}

// TestApplyFlightProfileFiltersNilSafe verifies the helper is a no-op on a nil
// or unsuccessful result, so callers can invoke it unconditionally.
func TestApplyFlightProfileFiltersNilSafe(t *testing.T) {
	applyFlightProfileFilters(nil, preferences.Default(), false)

	failed := &models.FlightSearchResult{Success: false, Flights: []models.FlightResult{makeFlight(900, "12:00")}}
	prefs := preferences.Default()
	prefs.BudgetFlightMax = 500
	applyFlightProfileFilters(failed, prefs, false)
	if len(failed.Flights) != 1 {
		t.Errorf("expected unsuccessful result untouched, got %d flights", len(failed.Flights))
	}
}

// TestFlightsMaxPriceFlagRegistered guards the --max-price flag wiring that
// gives explicit-flag precedence over the saved profile budget.
func TestFlightsMaxPriceFlagRegistered(t *testing.T) {
	if flightsCmd().Flags().Lookup("max-price") == nil {
		t.Error("--max-price flag not registered on flights command")
	}
}
