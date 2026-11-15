package models

import "testing"

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
