package multimodal

import (
	"context"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// route2 builds a 2-leg discovered mode-chain with explicit per-leg endpoints
// and an indicative price range (Rome2Rio style).
func route2(modeA, hub, modeB, from, to string, indicativeLow float64, cur string) models.GroundRoute {
	return models.GroundRoute{
		Provider:   "rome2rio",
		Type:       "mixed",
		Price:      indicativeLow,
		Currency:   cur,
		Duration:   600,
		Transfers:  1,
		BookingURL: "https://www.rome2rio.com/map/" + from + "/" + to,
		Departure:  models.GroundStop{City: from},
		Arrival:    models.GroundStop{City: to},
		Legs: []models.GroundLeg{
			{Type: modeA, Departure: models.GroundStop{City: from}, Arrival: models.GroundStop{City: hub}},
			{Type: modeB, Departure: models.GroundStop{City: hub}, Arrival: models.GroundStop{City: to}},
		},
	}
}

// fakePricer prices a leg from a lookup keyed by mode; missing modes are
// unpriceable (ok=false), driving the estimate-fallback path.
type fakePricer map[string]PricedLeg

func (f fakePricer) price(_ context.Context, spec LegSpec) (PricedLeg, bool) {
	pl, ok := f[spec.Mode]
	if !ok {
		return PricedLeg{}, false
	}
	pl.Mode, pl.From, pl.To = spec.Mode, spec.From, spec.To
	return pl, true
}

func plannerWith(disc Discoverer, price LegPricer) *Planner {
	return &Planner{Discover: disc, Price: price}
}

func discoverFixed(routes ...models.GroundRoute) Discoverer {
	return func(_ context.Context, _, _ string, _ bool) ([]models.GroundRoute, error) {
		return routes, nil
	}
}

func TestPlan_TwoLegChain_SummedRealPrice(t *testing.T) {
	r := route2("ferry", "Hub", "fly", "Helsinki", "London", 200, "EUR")
	pricer := fakePricer{
		"ferry": {Price: 40, Currency: "EUR", DurationMin: 120, Provider: "tallink"},
		"fly":   {Price: 90, Currency: "EUR", DurationMin: 150, Provider: "Ryanair"},
	}
	p := plannerWith(discoverFixed(r), pricer.price)

	plan, err := p.Plan(context.Background(), "Helsinki", "London", "2026-07-01")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.Discovered != 1 || plan.Priced != 1 || len(plan.Itineraries) != 1 {
		t.Fatalf("expected 1 discovered/priced/itinerary, got %d/%d/%d", plan.Discovered, plan.Priced, len(plan.Itineraries))
	}
	it := plan.Itineraries[0]
	if it.Estimated {
		t.Errorf("itinerary should not be estimated when both legs priced")
	}
	if it.TotalPrice != 130 {
		t.Errorf("expected summed total 130, got %v", it.TotalPrice)
	}
	if it.Currency != "EUR" {
		t.Errorf("expected EUR, got %q", it.Currency)
	}
	if it.ModeChain != "ferry → fly" {
		t.Errorf("expected mode chain 'ferry → fly', got %q", it.ModeChain)
	}
	if it.DurationMin != 270 {
		t.Errorf("expected summed duration 270, got %d", it.DurationMin)
	}
	if it.Transfers != 1 {
		t.Errorf("expected 1 transfer, got %d", it.Transfers)
	}
}

func TestPlan_RankByTrueTotal(t *testing.T) {
	cheap := route2("ferry", "Hub", "fly", "Helsinki", "London", 300, "EUR")
	cheap.BookingURL = "cheap"
	expensive := route2("train", "Hub2", "fly", "Helsinki", "London", 300, "EUR")
	expensive.BookingURL = "expensive"

	pricer := fakePricer{
		"ferry": {Price: 40, Currency: "EUR"},
		"train": {Price: 150, Currency: "EUR"},
		"fly":   {Price: 90, Currency: "EUR"},
	}
	p := plannerWith(discoverFixed(expensive, cheap), pricer.price)

	plan, _ := p.Plan(context.Background(), "Helsinki", "London", "2026-07-01")
	if len(plan.Itineraries) != 2 {
		t.Fatalf("expected 2 itineraries, got %d", len(plan.Itineraries))
	}
	if plan.Itineraries[0].TotalPrice != 130 || plan.Itineraries[0].BookingURL != "cheap" {
		t.Errorf("cheapest (130/cheap) should rank first, got %v/%q", plan.Itineraries[0].TotalPrice, plan.Itineraries[0].BookingURL)
	}
	if plan.Itineraries[1].TotalPrice != 240 {
		t.Errorf("expected second total 240, got %v", plan.Itineraries[1].TotalPrice)
	}
}

func TestPlan_EstimateFallback_WhenLegUnpriceable(t *testing.T) {
	// indicative low 200; ferry priced 40 real; fly unpriceable → estimated
	// remainder = 200 - 40 = 160 attributed to the fly leg.
	r := route2("ferry", "Hub", "fly", "Helsinki", "London", 200, "EUR")
	pricer := fakePricer{
		"ferry": {Price: 40, Currency: "EUR", DurationMin: 120, Provider: "tallink"},
		// no "fly" → unpriceable
	}
	p := plannerWith(discoverFixed(r), pricer.price)

	plan, _ := p.Plan(context.Background(), "Helsinki", "London", "2026-07-01")
	if len(plan.Itineraries) != 1 {
		t.Fatalf("expected 1 itinerary, got %d", len(plan.Itineraries))
	}
	it := plan.Itineraries[0]
	if !it.Estimated {
		t.Fatalf("itinerary must be flagged Estimated when a leg falls back to indicative")
	}
	if it.TotalPrice != 200 {
		t.Errorf("expected total 200 (40 real + 160 indicative remainder), got %v", it.TotalPrice)
	}
	var flyLeg *PricedLeg
	for i := range it.Legs {
		if it.Legs[i].Mode == "fly" {
			flyLeg = &it.Legs[i]
		}
	}
	if flyLeg == nil {
		t.Fatal("fly leg missing")
	}
	if !flyLeg.Estimated {
		t.Errorf("fly leg should be Estimated")
	}
	if flyLeg.Price != 160 {
		t.Errorf("expected fly estimate 160, got %v", flyLeg.Price)
	}
	if flyLeg.Provider != "rome2rio (estimate)" {
		t.Errorf("estimate leg should be labelled rome2rio (estimate), got %q", flyLeg.Provider)
	}
	if len(it.Warnings) == 0 {
		t.Errorf("estimated itinerary must carry a warning")
	}
}

func TestPlan_EmptyDiscovery_Graceful(t *testing.T) {
	p := plannerWith(discoverFixed(), fakePricer{}.price)
	plan, err := p.Plan(context.Background(), "Helsinki", "London", "2026-07-01")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.Error != "" {
		t.Errorf("empty discovery is not an error, got %q", plan.Error)
	}
	if len(plan.Itineraries) != 0 {
		t.Errorf("expected no itineraries, got %d", len(plan.Itineraries))
	}
	if len(plan.Notes) == 0 {
		t.Errorf("expected a note explaining no routes")
	}
}

func TestPlan_DiscoveryError_Degrades(t *testing.T) {
	disc := func(_ context.Context, _, _ string, _ bool) ([]models.GroundRoute, error) {
		return nil, context.DeadlineExceeded
	}
	p := plannerWith(disc, fakePricer{}.price)
	plan, err := p.Plan(context.Background(), "Helsinki", "London", "2026-07-01")
	if err != nil {
		t.Fatalf("discovery error must degrade, not propagate: %v", err)
	}
	if plan.Error == "" {
		t.Errorf("expected plan.Error to carry the discovery failure")
	}
	if len(plan.Itineraries) != 0 {
		t.Errorf("expected no itineraries on discovery failure")
	}
}

func TestPlan_HackAnnotation_OnCheapest(t *testing.T) {
	r := route2("ferry", "Hub", "fly", "Helsinki", "London", 200, "EUR")
	pricer := fakePricer{
		"ferry": {Price: 40, Currency: "EUR"},
		"fly":   {Price: 90, Currency: "EUR"},
	}
	p := plannerWith(discoverFixed(r), pricer.price)
	p.Hacks = func(_ context.Context, from, to, date string, naive float64, cur string) *models.HackSaving {
		if naive != 130 {
			t.Errorf("hack baseline should be the true total 130, got %v", naive)
		}
		return &models.HackSaving{Type: "multimodal_skip_flight", Savings: 20, Price: 110, NaivePrice: naive, Currency: cur}
	}

	plan, _ := p.Plan(context.Background(), "Helsinki", "London", "2026-07-01")
	if len(plan.Itineraries) != 1 || plan.Itineraries[0].HackSaving == nil {
		t.Fatalf("expected a hack saving annotated on the cheapest itinerary")
	}
	if plan.Itineraries[0].HackSaving.Savings != 20 {
		t.Errorf("expected saving 20, got %v", plan.Itineraries[0].HackSaving.Savings)
	}
}

func TestPlan_MissingArgs(t *testing.T) {
	p := plannerWith(discoverFixed(), fakePricer{}.price)
	plan, _ := p.Plan(context.Background(), "", "London", "2026-07-01")
	if plan.Error == "" {
		t.Errorf("expected an error for missing origin")
	}
}

func TestAssemble_CurrencyMismatchDemotesToEstimate(t *testing.T) {
	r := route2("ferry", "Hub", "fly", "Helsinki", "London", 200, "EUR")
	legs := []PricedLeg{
		{Mode: "ferry", Price: 40, Currency: "EUR"},
		{Mode: "fly", Price: 100, Currency: "GBP"}, // mismatched real fare
	}
	it, ok := assembleItinerary(r, "Helsinki", "London", "2026-07-01", legs)
	if !ok {
		t.Fatal("expected an assembled itinerary")
	}
	if !it.Estimated {
		t.Errorf("currency-mismatched leg must demote the itinerary to estimated")
	}
	if it.Currency != "EUR" {
		t.Errorf("headline currency should be the first real EUR leg, got %q", it.Currency)
	}
	// remainder = 200 - 40 = 160 on the demoted fly leg → total 200.
	if it.TotalPrice != 200 {
		t.Errorf("expected total 200, got %v", it.TotalPrice)
	}
}

func TestLegSpecsForRoute_BoundaryFill(t *testing.T) {
	r := models.GroundRoute{
		Type: "mixed",
		Legs: []models.GroundLeg{
			{Type: "ferry"}, // no endpoints
			{Type: "fly"},   // no endpoints
		},
	}
	specs := legSpecsForRoute(r, "Helsinki", "London", "2026-07-01")
	if len(specs) != 2 {
		t.Fatalf("expected 2 specs, got %d", len(specs))
	}
	if specs[0].From != "Helsinki" {
		t.Errorf("first leg origin should default to overall from, got %q", specs[0].From)
	}
	if specs[1].To != "London" {
		t.Errorf("last leg destination should default to overall to, got %q", specs[1].To)
	}
}

func TestLegSpecsForRoute_SingleModeNoLegs(t *testing.T) {
	r := models.GroundRoute{Type: "train"}
	specs := legSpecsForRoute(r, "Paris", "Lyon", "2026-07-01")
	if len(specs) != 1 || specs[0].Mode != "train" || specs[0].From != "Paris" || specs[0].To != "Lyon" {
		t.Errorf("single-mode route should yield one end-to-end leg, got %+v", specs)
	}
}
