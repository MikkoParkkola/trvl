package ground

import (
	"context"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

func withStubComposer(t *testing.T, fn func(ctx context.Context, req HackComposeRequest) *models.HackSaving) {
	t.Helper()
	prev := composeHackSaving
	composeHackSaving = fn
	t.Cleanup(func() { composeHackSaving = prev })
}

func okGroundResult() *models.GroundSearchResult {
	return &models.GroundSearchResult{
		Success: true,
		Count:   2,
		Routes: []models.GroundRoute{
			// Shaped as the merge leaves them: live fares in the target currency.
			{Provider: "flixbus", Price: 39, Currency: "EUR", ComparablePrice: 39, PriceProvenance: models.PriceLive},
			{Provider: "regiojet", Price: 55, Currency: "EUR", ComparablePrice: 55, PriceProvenance: models.PriceLive},
		},
	}
}

// (a) A ground search surfaces a hack-derived saving when one exists.
func TestAttachGroundHackSaving_SurfacesSaving(t *testing.T) {
	var gotNaive float64
	withStubComposer(t, func(_ context.Context, req HackComposeRequest) *models.HackSaving {
		gotNaive = req.NaivePrice
		return &models.HackSaving{Type: "cross_border_rail", Price: 25, NaivePrice: req.NaivePrice, Savings: 14, Currency: "EUR"}
	})
	res := okGroundResult()
	attachGroundHackSaving(context.Background(), res, "Helsinki", "Tallinn", "2026-09-01", SearchOptions{})

	if gotNaive != 39 {
		t.Errorf("composer NaivePrice = %v, want 39 (cheapest route)", gotNaive)
	}
	if res.HackSaving == nil || res.HackSaving.Type != "cross_border_rail" {
		t.Fatalf("HackSaving not surfaced: %+v", res.HackSaving)
	}
}

// (b) No saving surfaced when the engine finds none.
func TestAttachGroundHackSaving_NoneWhenEngineEmpty(t *testing.T) {
	withStubComposer(t, func(context.Context, HackComposeRequest) *models.HackSaving { return nil })
	res := okGroundResult()
	attachGroundHackSaving(context.Background(), res, "Helsinki", "Tallinn", "2026-09-01", SearchOptions{})
	if res.HackSaving != nil {
		t.Fatalf("expected no HackSaving, got %+v", res.HackSaving)
	}
}

// (c) The naive routes are never replaced; the saving is purely additive.
func TestAttachGroundHackSaving_NeverReplacesNaive(t *testing.T) {
	withStubComposer(t, func(_ context.Context, req HackComposeRequest) *models.HackSaving {
		return &models.HackSaving{Type: "multimodal", Price: 20, NaivePrice: req.NaivePrice, Savings: 19}
	})
	res := okGroundResult()
	before := append([]models.GroundRoute(nil), res.Routes...)
	attachGroundHackSaving(context.Background(), res, "Helsinki", "Tallinn", "2026-09-01", SearchOptions{})

	if len(res.Routes) != len(before) {
		t.Fatalf("Routes length changed: got %d, want %d", len(res.Routes), len(before))
	}
	if res.Routes[0].Price != 39 || res.Routes[0].Provider != "flixbus" {
		t.Errorf("cheapest naive route mutated: %+v", res.Routes[0])
	}
	if res.HackSaving == nil {
		t.Fatal("expected additive HackSaving alongside untouched naive routes")
	}
}

// (d) Opt-out via NoHacks.
func TestAttachGroundHackSaving_OptOut(t *testing.T) {
	called := false
	withStubComposer(t, func(context.Context, HackComposeRequest) *models.HackSaving {
		called = true
		return &models.HackSaving{Type: "x"}
	})
	res := okGroundResult()
	attachGroundHackSaving(context.Background(), res, "Helsinki", "Tallinn", "2026-09-01", SearchOptions{NoHacks: true})
	if called || res.HackSaving != nil {
		t.Error("composer ran despite NoHacks opt-out")
	}
}

// (e) Re-entrancy guard.
func TestAttachGroundHackSaving_ReentrancyGuard(t *testing.T) {
	called := false
	withStubComposer(t, func(context.Context, HackComposeRequest) *models.HackSaving {
		called = true
		return &models.HackSaving{Type: "x"}
	})
	res := okGroundResult()
	attachGroundHackSaving(disableHacksCompose(context.Background()), res, "Helsinki", "Tallinn", "2026-09-01", SearchOptions{})
	if called || res.HackSaving != nil {
		t.Error("composer ran inside a nested (hacks-disabled) search — recursion not guarded")
	}
}

// (f) Only a live fare is a naive baseline: an estimate (or a fare that could
// not be converted) is never what a saving is measured against.
func TestAttachGroundHackSaving_NaiveIsLiveOnly(t *testing.T) {
	var gotNaive float64
	called := false
	withStubComposer(t, func(_ context.Context, req HackComposeRequest) *models.HackSaving {
		called, gotNaive = true, req.NaivePrice
		return nil
	})
	res := &models.GroundSearchResult{Success: true, Count: 3, Routes: []models.GroundRoute{
		{Provider: "rome2rio", Price: 12, Currency: "EUR", ComparablePrice: 12, PriceProvenance: models.PriceEstimated},
		{Provider: "regiojet", Price: 300, Currency: "CZK", PriceProvenance: models.PriceLive},
		{Provider: "flixbus", Price: 39, Currency: "EUR", ComparablePrice: 39, PriceProvenance: models.PriceLive},
	}}
	attachGroundHackSaving(context.Background(), res, "Prague", "Vienna", "2026-09-01", SearchOptions{Currency: "EUR"})
	if !called || gotNaive != 39 {
		t.Errorf("composer called %v with NaivePrice %v, want 39 (the live EUR fare)", called, gotNaive)
	}

	called = false
	estimatesOnly := &models.GroundSearchResult{Success: true, Count: 1, Routes: res.Routes[:1]}
	attachGroundHackSaving(context.Background(), estimatesOnly, "Prague", "Vienna", "2026-09-01", SearchOptions{Currency: "EUR"})
	if called {
		t.Error("composer ran with only an estimated fare as the naive baseline")
	}
}
