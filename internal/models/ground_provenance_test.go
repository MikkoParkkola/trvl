package models

import (
	"encoding/json"
	"testing"
)

// groundRouteFixture returns one physical connection as a provider reports it.
func groundRouteFixture(provider string, price float64, prov PriceProvenance, url string) GroundRoute {
	return GroundRoute{
		Provider:        provider,
		Type:            "bus",
		Price:           price,
		Currency:        "EUR",
		ComparablePrice: price,
		PriceProvenance: prov,
		Departure:       GroundStop{City: "Prague", Time: "2026-07-01T08:00:00"},
		Arrival:         GroundStop{City: "Vienna", Time: "2026-07-01T12:00:00"},
		BookingURL:      url,
	}
}

// TestResolveGroundSources_HeadlineProvenanceTierFirst pins D1: the headline
// is chosen by provenance tier first, price inside the tier, and every
// headline field comes from the same winning source. A cheaper estimate must
// not become the headline of a connection that has a live fare.
func TestResolveGroundSources_HeadlineProvenanceTierFirst(t *testing.T) {
	routes := []GroundRoute{
		groundRouteFixture("rome2rio", 20, PriceEstimated, "https://estimate.example/r"),
		groundRouteFixture("flixbus", 30, PriceLive, "https://live.example/f"),
	}
	got := ResolveGroundSources(routes)
	if len(got) != 1 {
		t.Fatalf("want 1 resolved route, got %d", len(got))
	}
	r := got[0]
	if r.PriceProvenance != PriceLive {
		t.Errorf("headline provenance = %q, want live", r.PriceProvenance)
	}
	if r.Price != 30 || r.Provider != "flixbus" || r.CheapestSource != "flixbus" {
		t.Errorf("headline = %v %s (cheapest_source %s), want 30 flixbus", r.Price, r.Provider, r.CheapestSource)
	}
	if r.BookingURL != "https://live.example/f" {
		t.Errorf("booking url = %q, want the live source's", r.BookingURL)
	}
	if r.ComparablePrice != 30 {
		t.Errorf("comparable price = %v, want 30 from the live source", r.ComparablePrice)
	}
	if r.Savings != 0 {
		t.Errorf("savings = %v, want 0: an estimate is not a saving against a live fare", r.Savings)
	}
}

// TestResolveGroundSources_PriceInsideTier pins that inside one provenance
// tier the cheapest source still wins and savings are measured in that tier.
func TestResolveGroundSources_PriceInsideTier(t *testing.T) {
	routes := []GroundRoute{
		groundRouteFixture("regiojet", 35, PriceLive, "https://live.example/r"),
		groundRouteFixture("rome2rio", 15, PriceEstimated, "https://estimate.example/r"),
		groundRouteFixture("flixbus", 30, PriceLive, "https://live.example/f"),
	}
	got := ResolveGroundSources(routes)
	if len(got) != 1 {
		t.Fatalf("want 1 resolved route, got %d", len(got))
	}
	r := got[0]
	if r.Provider != "flixbus" || r.Price != 30 || r.PriceProvenance != PriceLive {
		t.Errorf("headline = %s %v %q, want flixbus 30 live", r.Provider, r.Price, r.PriceProvenance)
	}
	if r.Savings != 5 {
		t.Errorf("savings = %v, want 5 (35-30 inside the live tier)", r.Savings)
	}
}

// TestFoldGroundSource_CopiesProvenanceAndComparablePrice pins that every
// PriceSource carries its own route's provenance and comparable price.
func TestFoldGroundSource_CopiesProvenanceAndComparablePrice(t *testing.T) {
	a := groundRouteFixture("rome2rio", 20, PriceEstimated, "https://estimate.example/r")
	a.ComparablePrice = 19.5
	b := groundRouteFixture("flixbus", 30, PriceLive, "https://live.example/f")
	got := ResolveGroundSources([]GroundRoute{a, b})
	if len(got) != 1 || len(got[0].Sources) != 2 {
		t.Fatalf("want 1 route with 2 sources, got %+v", got)
	}
	want := map[string]struct {
		prov PriceProvenance
		cmp  float64
	}{"rome2rio": {PriceEstimated, 19.5}, "flixbus": {PriceLive, 30}}
	for _, s := range got[0].Sources {
		w := want[s.Provider]
		if s.Provenance != w.prov || s.ComparablePrice != w.cmp {
			t.Errorf("source %s: provenance %q comparable %v, want %q %v", s.Provider, s.Provenance, s.ComparablePrice, w.prov, w.cmp)
		}
	}
}

// TestFoldGroundSource_FreshnessByProvenance pins that only a live fare gets a
// live freshness; an estimate is static and an unavailable price has none.
func TestFoldGroundSource_FreshnessByProvenance(t *testing.T) {
	cases := []struct {
		prov  PriceProvenance
		price float64
		want  string
	}{
		{PriceLive, 30, FreshnessLive},
		{PriceEstimated, 30, FreshnessStatic},
		{PriceUnavailable, 0, ""},
	}
	for _, c := range cases {
		r := groundRouteFixture("p", c.price, c.prov, "https://x.example")
		got := ResolveGroundSources([]GroundRoute{r})
		if len(got) != 1 || len(got[0].Sources) != 1 {
			t.Fatalf("%s: want 1 route with 1 source, got %+v", c.prov, got)
		}
		if f := got[0].Sources[0].Freshness; f != c.want {
			t.Errorf("%s source freshness = %q, want %q", c.prov, f, c.want)
		}
	}
}

// TestGroundProvenance_WireNames pins the JSON field names consumers read:
// price_provenance on the route and on each source, comparable_price on each
// source.
func TestGroundProvenance_WireNames(t *testing.T) {
	r := GroundRoute{
		Provider: "flixbus", Price: 20, Currency: "EUR", PriceProvenance: PriceLive,
		Sources: []PriceSource{{Provider: "rome2rio", Price: 19, Currency: "EUR", Provenance: PriceEstimated, ComparablePrice: 19.5}},
	}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got struct {
		PriceProvenance string `json:"price_provenance"`
		Sources         []struct {
			PriceProvenance string  `json:"price_provenance"`
			ComparablePrice float64 `json:"comparable_price"`
		} `json:"sources"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.PriceProvenance != "live" {
		t.Errorf("route price_provenance = %q, want live; JSON %s", got.PriceProvenance, raw)
	}
	if len(got.Sources) != 1 || got.Sources[0].PriceProvenance != "estimated" || got.Sources[0].ComparablePrice != 19.5 {
		t.Errorf("source wire fields = %+v, want price_provenance estimated and comparable_price 19.5; JSON %s", got.Sources, raw)
	}
}

// TestResolveGroundSources_HeadlineFromComparableGroup shapes the sources the
// way the normalise pass leaves them: converted sources are in the target
// currency with ComparablePrice set, unconvertible ones keep their own
// currency with ComparablePrice 0. Two unconvertible CZK sources must not make
// CZK the headline currency over a comparable EUR fare, and every headline
// field comes from that fare.
func TestResolveGroundSources_HeadlineFromComparableGroup(t *testing.T) {
	czkA := groundRouteFixture("regiojet", 450, PriceLive, "https://czk.example/a")
	czkA.Currency, czkA.ComparablePrice = "CZK", 0
	czkB := groundRouteFixture("regiojet-b", 500, PriceLive, "https://czk.example/b")
	czkB.Currency, czkB.ComparablePrice = "CZK", 0
	eur := groundRouteFixture("flixbus", 22, PriceLive, "https://eur.example/f")
	eur.PriceMax = 30
	got := ResolveGroundSources([]GroundRoute{czkA, czkB, eur})
	if len(got) != 1 {
		t.Fatalf("want 1 resolved route, got %d", len(got))
	}
	r := got[0]
	if r.Provider != "flixbus" || r.Price != 22 || r.Currency != "EUR" || r.CheapestSource != "flixbus" {
		t.Errorf("headline = %s %v %s (cheapest_source %s), want flixbus 22 EUR", r.Provider, r.Price, r.Currency, r.CheapestSource)
	}
	if r.BookingURL != "https://eur.example/f" || r.ComparablePrice != 22 || r.PriceMax != 30 || r.PriceProvenance != PriceLive {
		t.Errorf("headline fields = url %q comparable %v max %v provenance %q, want the EUR fare's", r.BookingURL, r.ComparablePrice, r.PriceMax, r.PriceProvenance)
	}
}

// TestResolveGroundSources_AllFieldsFromWinnerNotLastFold pins that the
// headline's fare-risk fields come from the winning source even when a
// cheaper estimate is folded after it, and that no field leaks from the
// sources folded before or after.
func TestResolveGroundSources_AllFieldsFromWinnerNotLastFold(t *testing.T) {
	seats := func(n int) *int { return &n }
	first := groundRouteFixture("db", 40, PriceLive, "https://live.example/db")
	first.PriceMax, first.Amenities, first.SeatsLeft = 50, []string{"first"}, seats(1)
	winner := groundRouteFixture("flixbus", 25, PriceLive, "https://live.example/f")
	winner.PriceMax, winner.Amenities, winner.SeatsLeft = 35, []string{"winner"}, seats(9)
	last := groundRouteFixture("rome2rio", 10, PriceEstimated, "https://estimate.example/r")
	last.PriceMax, last.Amenities, last.SeatsLeft = 12, []string{"last"}, seats(2)
	got := ResolveGroundSources([]GroundRoute{first, winner, last})
	if len(got) != 1 {
		t.Fatalf("want 1 resolved route, got %d", len(got))
	}
	r := got[0]
	if r.Provider != "flixbus" || r.CheapestSource != "flixbus" || r.Price != 25 || r.BookingURL != "https://live.example/f" {
		t.Errorf("headline = %s (cheapest_source %s) %v %q, want flixbus 25 and its booking url", r.Provider, r.CheapestSource, r.Price, r.BookingURL)
	}
	if r.PriceMax != 35 || r.ComparablePrice != 25 || r.PriceProvenance != PriceLive {
		t.Errorf("price_max %v comparable %v provenance %q, want 35 25 live", r.PriceMax, r.ComparablePrice, r.PriceProvenance)
	}
	if len(r.Amenities) != 1 || r.Amenities[0] != "winner" || r.SeatsLeft == nil || *r.SeatsLeft != 9 {
		t.Errorf("amenities %v seats %v, want the winner's [winner] and 9", r.Amenities, r.SeatsLeft)
	}
	if r.Savings != 15 {
		t.Errorf("savings = %v, want 15 (40-25 inside the live tier)", r.Savings)
	}
}
