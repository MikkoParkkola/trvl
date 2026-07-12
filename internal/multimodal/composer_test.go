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

	plan, err := p.Plan(context.Background(), "Helsinki", "London", "2026-07-01", "EUR")
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

	plan, _ := p.Plan(context.Background(), "Helsinki", "London", "2026-07-01", "EUR")
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

	plan, _ := p.Plan(context.Background(), "Helsinki", "London", "2026-07-01", "EUR")
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
	plan, err := p.Plan(context.Background(), "Helsinki", "London", "2026-07-01", "EUR")
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
	plan, err := p.Plan(context.Background(), "Helsinki", "London", "2026-07-01", "EUR")
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

	plan, _ := p.Plan(context.Background(), "Helsinki", "London", "2026-07-01", "EUR")
	if len(plan.Itineraries) != 1 || plan.Itineraries[0].HackSaving == nil {
		t.Fatalf("expected a hack saving annotated on the cheapest itinerary")
	}
	if plan.Itineraries[0].HackSaving.Savings != 20 {
		t.Errorf("expected saving 20, got %v", plan.Itineraries[0].HackSaving.Savings)
	}
}

func TestPlan_MissingArgs(t *testing.T) {
	p := plannerWith(discoverFixed(), fakePricer{}.price)
	plan, _ := p.Plan(context.Background(), "", "London", "2026-07-01", "EUR")
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

func TestProductionSeamsAndPurePickers(t *testing.T) {
	planner := NewPlanner(true)
	if planner.Discover == nil || planner.Price == nil || planner.Hacks == nil || !planner.AllowBrowser {
		t.Fatalf("NewPlanner did not wire production seams: %#v", planner)
	}

	pricer := productionLegPricer(false)
	if _, ok := pricer(context.Background(), LegSpec{Mode: "drive", From: "A", To: "B"}); ok {
		t.Fatal("drive legs should not be treated as bookable provider fares")
	}
	if _, ok := pricer(context.Background(), LegSpec{Mode: "walk", From: "A", To: "B"}); ok {
		t.Fatal("walk legs should not be treated as bookable provider fares")
	}
	if _, ok := pricer(context.Background(), LegSpec{Mode: "fly", From: "Unknownville", To: "Nowhere"}); ok {
		t.Fatal("unresolvable flight endpoints should be unpriced")
	}
	if _, ok := pricer(context.Background(), LegSpec{Mode: "train", From: "", To: "Paris"}); ok {
		t.Fatal("ground legs with missing endpoints should be unpriced")
	}

	if code, ok := resolveAirport("hel"); !ok || code != "HEL" {
		t.Fatalf("resolveAirport IATA = %q/%v", code, ok)
	}
	if code, ok := resolveAirport("Helsinki"); !ok || code != "HEL" {
		t.Fatalf("resolveAirport city = %q/%v", code, ok)
	}
	if _, ok := resolveAirport("Unknownville"); ok {
		t.Fatal("unknown city should not resolve to an airport")
	}

	bestFlight, ok := cheapestFlight([]models.FlightResult{
		{Price: 0, Currency: "EUR"},
		{Price: 200, Currency: ""},
		{Price: 300, ComparablePrice: 180, Currency: "EUR", Provider: "google"},
		{Price: 190, Currency: "EUR", Provider: "kiwi"},
	}, "")
	if !ok || bestFlight.Provider != "google" {
		t.Fatalf("cheapestFlight = %#v/%v", bestFlight, ok)
	}
	if _, ok := cheapestFlight([]models.FlightResult{{Price: 0, Currency: "EUR"}}, ""); ok {
		t.Fatal("empty priced flight set should not produce a cheapest flight")
	}

	bestRoute, ok := cheapestRoute([]models.GroundRoute{
		{Price: 0, Currency: "EUR"},
		{Price: 40, Currency: ""},
		{Price: 55, Currency: "EUR", Provider: "flixbus"},
		{Price: 45, Currency: "EUR", Provider: "sncf"},
	}, "")
	if !ok || bestRoute.Provider != "sncf" {
		t.Fatalf("cheapestRoute = %#v/%v", bestRoute, ok)
	}
	if _, ok := cheapestRoute([]models.GroundRoute{{Price: 0, Currency: "EUR"}}, ""); ok {
		t.Fatal("empty priced route set should not produce a cheapest route")
	}

	if got := flightProvider(models.FlightResult{}); got != "flights" {
		t.Fatalf("flightProvider default = %q", got)
	}
	if got := flightProvider(models.FlightResult{Provider: "kiwi"}); got != "kiwi" {
		t.Fatalf("flightProvider explicit = %q", got)
	}
}

func TestBoundRoutesAndIntegerFormatting(t *testing.T) {
	routes := make([]models.GroundRoute, 0, 12)
	for i := 0; i < 12; i++ {
		price := float64(200 - i)
		if i == 0 {
			price = 0
		}
		routes = append(routes, models.GroundRoute{Price: price})
	}
	bounded := boundRoutes(routes)
	if len(bounded) != maxRoutesPriced {
		t.Fatalf("bounded len = %d, want %d", len(bounded), maxRoutesPriced)
	}
	for i := 1; i < len(bounded); i++ {
		if indicativeSortKey(bounded[i-1]) > indicativeSortKey(bounded[i]) {
			t.Fatalf("routes not sorted by indicative price: %#v", bounded)
		}
	}
	if got := itoa(0); got != "0" {
		t.Fatalf("itoa(0) = %q", got)
	}
	if got := itoa(-42); got != "-42" {
		t.Fatalf("itoa(-42) = %q", got)
	}
	if got := itoa(12345); got != "12345" {
		t.Fatalf("itoa(12345) = %q", got)
	}
}

func TestPlan_BoundsLargeDiscoverySet(t *testing.T) {
	routes := make([]models.GroundRoute, 0, 12)
	for i := 0; i < 12; i++ {
		r := route2("bus", "Hub", "train", "Helsinki", "London", float64(100+i), "EUR")
		r.BookingURL = itoa(i)
		routes = append(routes, r)
	}
	p := plannerWith(discoverFixed(routes...), fakePricer{
		"bus":   {Price: 10, Currency: "EUR"},
		"train": {Price: 20, Currency: "EUR"},
	}.price)

	plan, err := p.Plan(context.Background(), "Helsinki", "London", "2026-07-01", "EUR")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.Discovered != 12 || plan.Priced != maxRoutesPriced {
		t.Fatalf("discovered/priced = %d/%d", plan.Discovered, plan.Priced)
	}
	if len(plan.Notes) == 0 {
		t.Fatalf("expected truncation note for large discovery set")
	}
}

// TestCheapestSelectorsExcludeForeignStragglers proves the currency-cohort honesty
// fix: a nominally-small fare in a minority currency must not win over the cheapest
// fare in the dominant cohort. Without cohort ranking, the raw numeric minimum would
// pick the foreign straggler and lie about which option is actually cheapest.
func TestCheapestSelectorsExcludeForeignStragglers(t *testing.T) {
	// GBP 5 is nominally the smallest number but is a lone straggler; the EUR cohort
	// (two entries) is dominant and its cheapest real fare is EUR 60.
	best, ok := cheapestFlight([]models.FlightResult{
		{Price: 60, Currency: "EUR", Provider: "eur-cheap"},
		{Price: 90, Currency: "EUR", Provider: "eur-dear"},
		{Price: 5, Currency: "GBP", Provider: "gbp-straggler"},
	}, "")
	if !ok || best.Provider != "eur-cheap" {
		t.Fatalf("cheapestFlight picked %#v/%v; want eur-cheap (GBP straggler must not win)", best, ok)
	}

	route, ok := cheapestRoute([]models.GroundRoute{
		{Price: 45, Currency: "EUR", Provider: "eur-cheap"},
		{Price: 70, Currency: "EUR", Provider: "eur-dear"},
		{Price: 3, Currency: "GBP", Provider: "gbp-straggler"},
	}, "")
	if !ok || route.Provider != "eur-cheap" {
		t.Fatalf("cheapestRoute picked %#v/%v; want eur-cheap (GBP straggler must not win)", route, ok)
	}
}

// TestCheapestSelectorsPreferTargetCurrency proves the second honesty guarantee:
// when the plan's target currency is present only as a minority (a provider mostly
// returned another currency), the selector still ranks within the target cohort so
// the leg stays in the requested currency instead of being repriced into whatever
// the provider returned most of. The dominant-currency fallback only applies when
// the target is absent.
func TestCheapestSelectorsPreferTargetCurrency(t *testing.T) {
	// GBP dominates (two entries) but the plan targets EUR, present as one entry.
	// Prefer must pick the EUR entry, not the cheaper-looking dominant GBP fare.
	best, ok := cheapestFlight([]models.FlightResult{
		{Price: 40, Currency: "GBP", Provider: "gbp-cheap"},
		{Price: 50, Currency: "GBP", Provider: "gbp-dear"},
		{Price: 70, Currency: "EUR", Provider: "eur-target"},
	}, "EUR")
	if !ok || best.Provider != "eur-target" {
		t.Fatalf("cheapestFlight picked %#v/%v; want eur-target (target currency must win over dominant GBP)", best, ok)
	}

	route, ok := cheapestRoute([]models.GroundRoute{
		{Price: 30, Currency: "GBP", Provider: "gbp-cheap"},
		{Price: 35, Currency: "GBP", Provider: "gbp-dear"},
		{Price: 55, Currency: "EUR", Provider: "eur-target"},
	}, "EUR")
	if !ok || route.Provider != "eur-target" {
		t.Fatalf("cheapestRoute picked %#v/%v; want eur-target (target currency must win over dominant GBP)", route, ok)
	}

	// When the target is absent, fall back to the dominant cohort.
	fallback, ok := cheapestFlight([]models.FlightResult{
		{Price: 40, Currency: "GBP", Provider: "gbp-cheap"},
		{Price: 50, Currency: "GBP", Provider: "gbp-dear"},
	}, "EUR")
	if !ok || fallback.Provider != "gbp-cheap" {
		t.Fatalf("cheapestFlight fallback picked %#v/%v; want gbp-cheap (dominant cohort when target absent)", fallback, ok)
	}
}
