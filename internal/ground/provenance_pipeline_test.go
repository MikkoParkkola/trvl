package ground

import (
	"context"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestClassifyGroundProvenance pins the fail-closed provenance pass: Price 0
// is unavailable whatever the parser said, a priced route without provenance
// is estimated, and an explicit provenance on a priced route is kept.
func TestClassifyGroundProvenance(t *testing.T) {
	cases := []struct {
		name  string
		price float64
		in    models.PriceProvenance
		want  models.PriceProvenance
	}{
		{"zero price, none", 0, "", models.PriceUnavailable},
		{"zero price, parser said live", 0, models.PriceLive, models.PriceUnavailable},
		{"priced, none", 22, "", models.PriceEstimated},
		{"priced, live", 22, models.PriceLive, models.PriceLive},
		{"priced, estimated", 22, models.PriceEstimated, models.PriceEstimated},
	}
	for _, c := range cases {
		routes := []models.GroundRoute{{Provider: "p", Price: c.price, Currency: "EUR", PriceProvenance: c.in}}
		classifyGroundProvenance(routes)
		if got := routes[0].PriceProvenance; got != c.want {
			t.Errorf("%s: provenance = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestSearchByNameCore_StaticProviderIsEstimated drives the real merge with
// Viking Line, whose prices come from a static table and which never dials
// out. Every route must come out estimated, with static freshness on its
// sources and an unrated "estimated fare" confidence: a table price must
// never read as a live quote.
func TestSearchByNameCore_StaticProviderIsEstimated(t *testing.T) {
	groundBreaker.RecordSuccess("vikingline")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	opts := SearchOptions{Currency: "EUR", Providers: []string{"vikingline"}, NoCache: true, NoHacks: true}
	res, err := searchByNameCore(ctx, "Helsinki", "Tallinn", "2026-07-15", opts, "provenance-static", false)
	if err != nil {
		t.Fatalf("searchByNameCore: %v", err)
	}
	if res == nil || len(res.Routes) == 0 {
		t.Fatalf("want Viking Line routes, got %+v", res)
	}
	for i, r := range res.Routes {
		if r.PriceProvenance != models.PriceEstimated {
			t.Errorf("route %d provenance = %q, want estimated", i, r.PriceProvenance)
		}
		for _, s := range r.Sources {
			if s.Freshness != models.FreshnessStatic || s.Provenance != models.PriceEstimated {
				t.Errorf("route %d source %s: freshness %q provenance %q, want static/estimated", i, s.Provider, s.Freshness, s.Provenance)
			}
		}
		if r.Confidence == nil {
			t.Errorf("route %d: confidence missing", i)
			continue
		}
		if r.Confidence.Rated || r.Confidence.Basis != "estimated fare" || r.Confidence.Freshness == models.FreshnessLive {
			t.Errorf("route %d confidence = %+v, want unrated with basis \"estimated fare\" and no live freshness", i, *r.Confidence)
		}
	}
}

// TestSortGroundRoutes_ProvenanceFirstInComparableGroup pins the ranking key:
// inside the comparable group, live fares rank before estimates, and price
// orders routes inside each tier.
func TestSortGroundRoutes_ProvenanceFirstInComparableGroup(t *testing.T) {
	routes := []models.GroundRoute{
		{Provider: "a-estimate", Price: 10, ComparablePrice: 10, Currency: "EUR", PriceProvenance: models.PriceEstimated},
		{Provider: "b-live", Price: 30, ComparablePrice: 30, Currency: "EUR", PriceProvenance: models.PriceLive},
		{Provider: "c-live", Price: 20, ComparablePrice: 20, Currency: "EUR", PriceProvenance: models.PriceLive},
	}
	sortGroundRoutes(routes)
	got := []string{routes[0].Provider, routes[1].Provider, routes[2].Provider}
	want := []string{"c-live", "b-live", "a-estimate"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

// TestSortGroundRoutes_ProvenanceOnlyInsideComparableGroup pins that
// provenance orders routes inside the comparable group and never lifts an
// unconvertible live fare above a comparable estimate: the comparable group
// still ranks first, and an unavailable route sinks last.
func TestSortGroundRoutes_ProvenanceOnlyInsideComparableGroup(t *testing.T) {
	routes := []models.GroundRoute{
		{Provider: "d-unavailable", Price: 0, PriceProvenance: models.PriceUnavailable},
		{Provider: "c-czk-live", Price: 400, Currency: "CZK", PriceProvenance: models.PriceLive},
		{Provider: "b-estimate", Price: 10, ComparablePrice: 10, Currency: "EUR", PriceProvenance: models.PriceEstimated},
		{Provider: "a-live", Price: 30, ComparablePrice: 30, Currency: "EUR", PriceProvenance: models.PriceLive},
	}
	sortGroundRoutes(routes)
	want := []string{"a-live", "b-estimate", "c-czk-live", "d-unavailable"}
	for i := range want {
		if routes[i].Provider != want[i] {
			got := make([]string, len(routes))
			for k, r := range routes {
				got[k] = r.Provider
			}
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

// TestFilterGroundRoutes_MaxPriceComparesComparablePrice pins that the budget
// is checked against the converted price, not the raw amount in a foreign
// currency.
func TestFilterGroundRoutes_MaxPriceComparesComparablePrice(t *testing.T) {
	routes := []models.GroundRoute{{
		Provider: "regiojet", Price: 500, Currency: "CZK", ComparablePrice: 20,
		PriceProvenance: models.PriceLive,
		Departure:       models.GroundStop{Time: "2026-07-01T08:00:00"},
	}}
	got := filterGroundRoutes(routes, SearchOptions{MaxPrice: 30})
	if len(got) != 1 {
		t.Fatalf("CZK 500 (= EUR 20) under a 30 budget was dropped; got %d routes", len(got))
	}
}

// TestFilterGroundRoutes_BudgetDropsUnavailableAndUnconverted pins that a set
// budget drops routes it cannot check: no fare at all, or a fare that could
// not be converted into the comparison currency.
func TestFilterGroundRoutes_BudgetDropsUnavailableAndUnconverted(t *testing.T) {
	routes := []models.GroundRoute{
		{Provider: "transitous", Price: 0, PriceProvenance: models.PriceUnavailable, Departure: models.GroundStop{Time: "2026-07-01T08:00:00"}},
		{Provider: "flixbus", Price: 15, Currency: "PLN", ComparablePrice: 0, PriceProvenance: models.PriceLive, Departure: models.GroundStop{Time: "2026-07-01T09:00:00"}},
		{Provider: "regiojet", Price: 20, Currency: "EUR", ComparablePrice: 20, PriceProvenance: models.PriceLive, Departure: models.GroundStop{Time: "2026-07-01T10:00:00"}},
	}
	got := filterGroundRoutes(routes, SearchOptions{MaxPrice: 30})
	if len(got) != 1 || got[0].Provider != "regiojet" {
		names := make([]string, 0, len(got))
		for _, r := range got {
			names = append(names, r.Provider)
		}
		t.Fatalf("kept %v under a budget, want only [regiojet]", names)
	}
}

// TestShouldKeepGroundRoute_ZeroPriceKeptAsUnavailable pins that the
// schedule-only allowlist is gone: a Price 0 route from a provider outside
// the old list is kept and shown as unavailable, not silently dropped. A
// RegioJet Price 0 row means sold out and is still dropped.
func TestShouldKeepGroundRoute_ZeroPriceKeptAsUnavailable(t *testing.T) {
	for _, p := range []string{"flixbus", "trainline", "rome2rio"} {
		r := models.GroundRoute{Provider: p, Price: 0, PriceProvenance: models.PriceUnavailable}
		if !shouldKeepGroundRoute(r) {
			t.Errorf("zero-price %s route dropped; want it kept as unavailable", p)
		}
	}
	if shouldKeepGroundRoute(models.GroundRoute{Provider: "regiojet", Price: 0}) {
		t.Error("zero-price regiojet route kept; a RegioJet zero price is sold out")
	}
}

// TestNormalizeGroundCurrencies_ConvertsPriceMax pins that the normalise pass
// converts the top of a price range along with Price, so a range never mixes
// two currencies.
func TestNormalizeGroundCurrencies_ConvertsPriceMax(t *testing.T) {
	routes := []models.GroundRoute{{Provider: "regiojet", Price: 500, PriceMax: 700, Currency: "CZK"}}
	conv := func(_ context.Context, amount float64, _, _ string) (float64, bool) { return amount / 25, true }
	normalizeGroundCurrencies(context.Background(), routes, "EUR", conv)
	r := routes[0]
	if r.Currency != "EUR" || r.Price != 20 || r.ComparablePrice != 20 {
		t.Fatalf("price = %v %s (comparable %v), want EUR 20 comparable 20", r.Price, r.Currency, r.ComparablePrice)
	}
	if r.PriceMax != 28 {
		t.Errorf("PriceMax = %v, want 28 (700 CZK converted)", r.PriceMax)
	}
}
