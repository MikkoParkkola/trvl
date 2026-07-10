package flights

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/preferences"
)

// policy_boost_test.go — exercises ApplySharedFlightPolicy on Success=false and on results carrying
// ProviderStatuses (raw counts). Verifies the anti-truncation invariant: after post-filter,
// ProviderStatuses keep the original RAW Results counts from providers, while Count and len(Flights)
// reflect only the post-policy filtered set.

func TestApplySharedFlightPolicy_SuccessFalseAndStatuses(t *testing.T) {
	// !Success: early return, no mutation
	r := &models.FlightSearchResult{Success: false, Flights: []models.FlightResult{{Price: 999}}, Count: 1}
	ApplySharedFlightPolicy(r, &preferences.Preferences{BudgetFlightMax: 10}, false)
	if len(r.Flights) != 1 || r.Count != 1 {
		t.Error("!Success must not filter")
	}

	// Success + statuses + filter active: statuses RAW, count = filtered
	r = &models.FlightSearchResult{
		Success: true,
		Flights: []models.FlightResult{
			{Price: 50, Currency: "EUR", Legs: []models.FlightLeg{{DepartureTime: "2026-08-01T10:00"}}},
			{Price: 500, Currency: "EUR", Legs: []models.FlightLeg{{DepartureTime: "2026-08-01T11:00"}}},
			{Price: 120, Currency: "EUR", Legs: []models.FlightLeg{{DepartureTime: "2026-08-01T09:00"}}},
		},
		ProviderStatuses: []models.ProviderStatus{
			{ID: "google_flights", Name: "Google Flights", Status: "ok", Results: 91},
			{ID: "kiwi", Name: "Kiwi", Status: "ok", Results: 15},
		},
		Count: 106,
	}
	prefs := &preferences.Preferences{BudgetFlightMax: 200, FlightTimeEarliest: "09:30", FlightTimeLatest: "22:00"}
	ApplySharedFlightPolicy(r, prefs, false)

	// filtered down by budget + time
	if r.Count != len(r.Flights) {
		t.Fatalf("Count must == len(Flights) after policy, %d vs %d", r.Count, len(r.Flights))
	}
	if r.Count == 106 {
		t.Error("policy should have filtered some")
	}

	// statuses must be untouched (raw provider counts)
	if len(r.ProviderStatuses) != 2 {
		t.Fatal("statuses dropped")
	}
	if r.ProviderStatuses[0].Results != 91 || r.ProviderStatuses[1].Results != 15 {
		t.Errorf("raw provider counts mutated: got %+v", r.ProviderStatuses)
	}
	// but result count is the filtered view
	if r.Count > 50 {
		t.Errorf("filtered count suspiciously high: %d", r.Count)
	}
}

func TestApplySharedFlightPolicy_PartialProviderCounts(t *testing.T) {
	// Simulate a result that had partial provider success (some statuses reflect 0 or checked no hit)
	r := &models.FlightSearchResult{
		Success: true,
		Flights: []models.FlightResult{{Price: 80}, {Price: 3000}},
		ProviderStatuses: []models.ProviderStatus{
			{ID: "google_flights", Status: "ok", Results: 2},
			{ID: "kiwi", Status: "checked_no_hit", Results: 0},
		},
		Count: 2,
	}
	ApplySharedFlightPolicy(r, &preferences.Preferences{BudgetFlightMax: 100}, false)
	if len(r.ProviderStatuses) != 2 || r.ProviderStatuses[1].Results != 0 {
		t.Error("partial raw status must survive")
	}
	if r.Count != 1 {
		t.Errorf("budget should leave only 1, count=%d", r.Count)
	}
}
