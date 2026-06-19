package flights

import (
	"context"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// withStubComposer temporarily installs a deterministic savings-engine seam so
// the auto-compose wiring is provable offline, then restores the previous one.
func withStubComposer(t *testing.T, fn func(ctx context.Context, req HackComposeRequest) *models.HackSaving) {
	t.Helper()
	prev := composeHackSaving
	composeHackSaving = fn
	t.Cleanup(func() { composeHackSaving = prev })
}

func okFlightResult() *models.FlightSearchResult {
	return &models.FlightSearchResult{
		Success: true,
		Count:   2,
		Flights: []models.FlightResult{
			{Price: 250, Currency: "EUR", Provider: "google_flights"},
			{Price: 300, Currency: "EUR", Provider: "kiwi"},
		},
	}
}

// (a) A search surfaces a hack-derived saving when one exists.
func TestAttachFlightHackSaving_SurfacesSaving(t *testing.T) {
	var gotNaive float64
	withStubComposer(t, func(_ context.Context, req HackComposeRequest) *models.HackSaving {
		gotNaive = req.NaivePrice
		return &models.HackSaving{Type: "hidden_city", Price: 200, NaivePrice: req.NaivePrice, Savings: 50, SavingsPct: 20, Currency: "EUR"}
	})

	res := okFlightResult()
	attachFlightHackSaving(context.Background(), res, "HEL", "AMS", "2026-09-01", SearchOptions{})

	if gotNaive != 250 {
		t.Errorf("composer NaivePrice = %v, want 250 (cheapest naive flight)", gotNaive)
	}
	if res.HackSaving == nil || res.HackSaving.Type != "hidden_city" {
		t.Fatalf("HackSaving not surfaced: %+v", res.HackSaving)
	}
	if res.HackSaving.Savings != 50 {
		t.Errorf("Savings = %v, want 50", res.HackSaving.Savings)
	}
}

// (b) No saving surfaced when the engine finds none.
func TestAttachFlightHackSaving_NoneWhenEngineEmpty(t *testing.T) {
	withStubComposer(t, func(context.Context, HackComposeRequest) *models.HackSaving { return nil })
	res := okFlightResult()
	attachFlightHackSaving(context.Background(), res, "HEL", "AMS", "2026-09-01", SearchOptions{})
	if res.HackSaving != nil {
		t.Fatalf("expected no HackSaving, got %+v", res.HackSaving)
	}
}

// (c) The naive results are never replaced; the saving is purely additive.
func TestAttachFlightHackSaving_NeverReplacesNaive(t *testing.T) {
	withStubComposer(t, func(_ context.Context, req HackComposeRequest) *models.HackSaving {
		return &models.HackSaving{Type: "split", Price: 180, NaivePrice: req.NaivePrice, Savings: 70}
	})
	res := okFlightResult()
	before := append([]models.FlightResult(nil), res.Flights...)

	attachFlightHackSaving(context.Background(), res, "HEL", "AMS", "2026-09-01", SearchOptions{})

	if len(res.Flights) != len(before) {
		t.Fatalf("Flights length changed: got %d, want %d", len(res.Flights), len(before))
	}
	for i := range before {
		if res.Flights[i].Price != before[i].Price || res.Flights[i].Provider != before[i].Provider {
			t.Errorf("naive flight %d mutated: %+v != %+v", i, res.Flights[i], before[i])
		}
	}
	if res.HackSaving == nil {
		t.Fatal("expected additive HackSaving alongside untouched naive flights")
	}
	// The cheapest naive flight (250) still leads; the saving does not overwrite it.
	if res.Flights[0].Price != 250 {
		t.Errorf("cheapest naive flight = %v, want 250 (unchanged)", res.Flights[0].Price)
	}
}

// (d) Opt-out via NoHacks runs a pure naive search.
func TestAttachFlightHackSaving_OptOut(t *testing.T) {
	called := false
	withStubComposer(t, func(context.Context, HackComposeRequest) *models.HackSaving {
		called = true
		return &models.HackSaving{Type: "x"}
	})
	res := okFlightResult()
	attachFlightHackSaving(context.Background(), res, "HEL", "AMS", "2026-09-01", SearchOptions{NoHacks: true})
	if called {
		t.Error("composer ran despite NoHacks opt-out")
	}
	if res.HackSaving != nil {
		t.Errorf("HackSaving set despite opt-out: %+v", res.HackSaving)
	}
}

// (e) Re-entrancy guard: a nested detector-initiated search does not recurse.
func TestAttachFlightHackSaving_ReentrancyGuard(t *testing.T) {
	called := false
	withStubComposer(t, func(context.Context, HackComposeRequest) *models.HackSaving {
		called = true
		return &models.HackSaving{Type: "x"}
	})
	res := okFlightResult()
	nested := disableHacksCompose(context.Background())
	attachFlightHackSaving(nested, res, "HEL", "AMS", "2026-09-01", SearchOptions{})
	if called {
		t.Error("composer ran inside a nested (hacks-disabled) search — recursion not guarded")
	}
	if res.HackSaving != nil {
		t.Errorf("HackSaving set in nested search: %+v", res.HackSaving)
	}
}

// Unpriced naive results yield no baseline, so no saving is claimed.
func TestAttachFlightHackSaving_NoNaivePrice(t *testing.T) {
	called := false
	withStubComposer(t, func(context.Context, HackComposeRequest) *models.HackSaving {
		called = true
		return &models.HackSaving{Type: "x"}
	})
	res := &models.FlightSearchResult{Success: true, Flights: []models.FlightResult{{Price: 0, Currency: "EUR"}}}
	attachFlightHackSaving(context.Background(), res, "HEL", "AMS", "2026-09-01", SearchOptions{})
	if called || res.HackSaving != nil {
		t.Error("composer ran / saving surfaced without a priced naive baseline")
	}
}
