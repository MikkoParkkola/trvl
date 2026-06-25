package trip

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// ============================================================
// PlanTrip validation paths
// ============================================================

func TestPlanTrip_MissingOrigin(t *testing.T) {
	_, err := PlanTrip(t.Context(), PlanInput{
		Destination: "BCN",
		DepartDate:  "2026-07-01",
		ReturnDate:  "2026-07-08",
		Guests:      2,
	})
	if err == nil {
		t.Error("expected error for missing origin")
	}
}

func TestPlanTrip_MissingDestination(t *testing.T) {
	_, err := PlanTrip(t.Context(), PlanInput{
		Origin:     "HEL",
		DepartDate: "2026-07-01",
		ReturnDate: "2026-07-08",
		Guests:     2,
	})
	if err == nil {
		t.Error("expected error for missing destination")
	}
}

func TestPlanTrip_MissingDates(t *testing.T) {
	_, err := PlanTrip(t.Context(), PlanInput{
		Origin:      "HEL",
		Destination: "BCN",
		Guests:      2,
	})
	if err == nil {
		t.Error("expected error for missing dates")
	}
}

func TestPlanTrip_InvalidDepartDate(t *testing.T) {
	_, err := PlanTrip(t.Context(), PlanInput{
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "not-a-date",
		ReturnDate:  "2026-07-08",
		Guests:      2,
	})
	if err == nil {
		t.Error("expected error for invalid depart date")
	}
}

func TestPlanTrip_InvalidReturnDate(t *testing.T) {
	_, err := PlanTrip(t.Context(), PlanInput{
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-01",
		ReturnDate:  "not-a-date",
		Guests:      2,
	})
	if err == nil {
		t.Error("expected error for invalid return date")
	}
}

func TestPlanTrip_ReturnBeforeDepart(t *testing.T) {
	_, err := PlanTrip(t.Context(), PlanInput{
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-08",
		ReturnDate:  "2026-07-01",
		Guests:      2,
	})
	if err == nil {
		t.Error("expected error when return is before depart")
	}
}

func TestPlanTrip_SameDay(t *testing.T) {
	_, err := PlanTrip(t.Context(), PlanInput{
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-01",
		ReturnDate:  "2026-07-01",
		Guests:      2,
	})
	if err == nil {
		t.Error("expected error for same-day trip")
	}
}

// TestMergeFlightProviders_WorstStatusWins proves the two-leg union keeps the
// most severe status per provider: a provider that succeeded outbound but was
// rate-limited or failed on the return leg must surface as degraded, never as
// the rosier outbound result. This is what makes the flight-coverage note honest.
func TestMergeFlightProviders_WorstStatusWins(t *testing.T) {
	out := &models.FlightSearchResult{ProviderStatuses: []models.ProviderStatus{
		{ID: "kiwi", Name: "Kiwi", Status: models.StatusCheckedHit, Results: 3},
		{ID: "google_flights", Name: "Google Flights", Status: models.StatusCheckedHit, Results: 2},
		{ID: "ryanair", Name: "Ryanair", Status: models.StatusRateLimited},
	}}
	ret := &models.FlightSearchResult{ProviderStatuses: []models.ProviderStatus{
		{ID: "kiwi", Name: "Kiwi", Status: models.StatusCheckedHit, Results: 1},
		{ID: "google_flights", Name: "Google Flights", Status: models.StatusRateLimited}, // degraded on return
		{ID: "ryanair", Name: "Ryanair", Status: models.StatusFailed},                    // worse on return
		{ID: "wizzair", Name: "Wizz Air", Status: models.StatusCheckedHit, Results: 4},   // only on return leg
	}}

	merged := mergeFlightProviders(out, ret)

	byID := map[string]models.ProviderStatus{}
	for _, s := range merged {
		byID[s.ID] = s
	}
	if len(merged) != 4 {
		t.Fatalf("expected 4 unioned providers, got %d: %+v", len(merged), merged)
	}
	if got := byID["kiwi"].Status; got != models.StatusCheckedHit {
		t.Errorf("kiwi clean on both legs should stay ok, got %q", got)
	}
	if got := byID["google_flights"].Status; got != models.StatusRateLimited {
		t.Errorf("google_flights rate-limited on return should win, got %q", got)
	}
	if got := byID["ryanair"].Status; got != models.StatusFailed {
		t.Errorf("ryanair failed on return should beat rate-limited outbound, got %q", got)
	}
	if got := byID["wizzair"].Status; got != models.StatusCheckedHit {
		t.Errorf("wizzair seen only on return leg should appear, got %q", got)
	}
}

// TestMergeFlightProviders_NilLegs proves a missing leg (search errored, nil
// result) never panics and simply contributes nothing to the union.
func TestMergeFlightProviders_NilLegs(t *testing.T) {
	if got := mergeFlightProviders(nil, nil); len(got) != 0 {
		t.Errorf("two nil legs should merge to empty, got %+v", got)
	}
	only := &models.FlightSearchResult{ProviderStatuses: []models.ProviderStatus{
		{ID: "kiwi", Name: "Kiwi", Status: models.StatusCheckedHit},
	}}
	if got := mergeFlightProviders(nil, only); len(got) != 1 || got[0].ID != "kiwi" {
		t.Errorf("one nil + one real leg should yield the real leg, got %+v", got)
	}
}
