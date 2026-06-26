package hacks

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/ground"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// liveRailQuote is a test seam returning a single deterministic train quote.
func liveRailQuote(price float64, provider string) func(context.Context, string, string, string, ground.SearchOptions) (*models.GroundSearchResult, error) {
	return func(_ context.Context, _, _, _ string, _ ground.SearchOptions) (*models.GroundSearchResult, error) {
		return &models.GroundSearchResult{
			Success: true,
			Count:   1,
			Routes:  []models.GroundRoute{{Provider: provider, Type: "train", Price: price, Currency: "EUR"}},
		}, nil
	}
}

func railProviderError() func(context.Context, string, string, string, ground.SearchOptions) (*models.GroundSearchResult, error) {
	return func(_ context.Context, _, _, _ string, _ ground.SearchOptions) (*models.GroundSearchResult, error) {
		return nil, errors.New("ground provider unavailable")
	}
}

// --- MIK-3079.AC.1 ---

// TestRailFlyVirtualOrigins verifies ZYR, ANR, and BRU are recognised virtual
// origins for KL(->AMS) and AF(->CDG): the AMS and CDG hubs each surface at
// least one station, and the ANR/BRU airport-style aliases both resolve to a
// KL/AMS station.
func TestRailFlyVirtualOrigins(t *testing.T) {
	if got := railFlyStationsForHub("AMS"); len(got) < 1 {
		t.Errorf("railFlyStationsForHub(AMS) = %d stations, want >= 1", len(got))
	}
	if got := railFlyStationsForHub("CDG"); len(got) < 1 {
		t.Errorf("railFlyStationsForHub(CDG) = %d stations, want >= 1", len(got))
	}

	// ZYR (Brussels-Midi) is recognised for BOTH KL(->AMS) and AF(->CDG).
	var zyrAMS, zyrCDG bool
	for _, st := range railFlyStations {
		if st.IATA == "ZYR" && st.HubIATA == "AMS" && st.Airline == "KL" {
			zyrAMS = true
		}
		if st.IATA == "ZYR" && st.HubIATA == "CDG" && st.Airline == "AF" {
			zyrCDG = true
		}
	}
	if !zyrAMS {
		t.Error("ZYR should be a KL virtual origin to AMS")
	}
	if !zyrCDG {
		t.Error("ZYR should be an AF virtual origin to CDG")
	}

	// ANR and BRU both resolve to a KL/AMS station.
	resolvesToKLAMS := func(origin string) {
		t.Helper()
		stations := railFlyStationsForHub(origin)
		if len(stations) < 1 {
			t.Fatalf("railFlyStationsForHub(%q) = 0 stations, want >= 1", origin)
		}
		for _, st := range stations {
			if st.Airline == "KL" && st.HubIATA == "AMS" {
				return
			}
		}
		t.Errorf("origin %q should resolve to a KL/AMS station, got %+v", origin, stations)
	}
	resolvesToKLAMS("ANR")
	resolvesToKLAMS("BRU")
}

// --- MIK-3079.AC.2 ---

// TestRailFlyBundleSingleTotal verifies the bundle composer prices the rail leg
// + flight leg as a SINGLE total — one Savings/total number, not two separate
// figures — and falls back to groundCostBetween when the ground provider fails.
func TestRailFlyBundleSingleTotal(t *testing.T) {
	// Provider error => deterministic groundCostBetween fallback for the rail leg.
	withRailGroundSearcher(t, railProviderError())

	const (
		flightCost = 200.0
		baseline   = 300.0
	)
	// Alias origin (ANR != hub AMS) so the rail leg is a real charged cost that
	// must fold into the single total.
	h := composeRailFlyBundle(context.Background(), "ANR", "BCN", "2026-05-01", "", testRailFlyStation, flightCost, baseline, "EUR")

	if h.Bundle == nil {
		t.Fatal("composed hack must carry a bundle")
	}
	estRail := groundCostBetween(testRailFlyStation.IATA, testRailFlyStation.HubIATA)
	wantTotal := roundSavings(flightCost + estRail)

	if h.Bundle.TotalCost != wantTotal {
		t.Errorf("bundle TotalCost = %v, want %v (flight %v + rail estimate %v)", h.Bundle.TotalCost, wantTotal, flightCost, estRail)
	}
	// The single total must equal the sum of every leg's cost — never two figures.
	if sum := roundSavings(h.Bundle.legCostSum()); sum != h.Bundle.TotalCost {
		t.Errorf("sum of leg costs %v != bundle TotalCost %v", sum, h.Bundle.TotalCost)
	}
	if len(h.Bundle.Legs) != 2 {
		t.Fatalf("one-way bundle should have 2 legs (rail + flight), got %d", len(h.Bundle.Legs))
	}
	// Savings is one combined number derived from the single total.
	if want := roundSavings(baseline - wantTotal); h.Savings != want {
		t.Errorf("Savings = %v, want %v (baseline %v - total %v)", h.Savings, want, baseline, wantTotal)
	}
	// The rail leg estimate must be honestly flagged.
	var railLeg *BundleLeg
	for i := range h.Bundle.Legs {
		if h.Bundle.Legs[i].Mode == BundleLegRail {
			railLeg = &h.Bundle.Legs[i]
		}
	}
	if railLeg == nil {
		t.Fatal("bundle should contain a rail leg")
	}
	if !railLeg.Estimated {
		t.Error("rail leg priced via the groundCostBetween fallback must be flagged Estimated")
	}
}

// --- MIK-3079.AC.4 ---

// TestRailFlyBundleLegTimingAndGuarantee verifies the bundle output includes
// BOTH legs with timing, a change window, and a connection-guarantee status —
// guaranteed for a hub (Air&Rail) origin, self-transfer for an alias origin.
func TestRailFlyBundleLegTimingAndGuarantee(t *testing.T) {
	withRailGroundSearcher(t, liveRailQuote(39, "eurostar"))

	// Hub origin (AMS == hub): the connection is an airline-guaranteed Air&Rail.
	hub := composeRailFlyBundle(context.Background(), "AMS", "BCN", "2026-05-01", "", testRailFlyStation, 240, 300, "EUR")
	if hub.Bundle == nil {
		t.Fatal("hub bundle must be present")
	}
	if !hub.Bundle.ConnectionGuaranteed {
		t.Error("hub-origin connection should be guaranteed (Air&Rail)")
	}
	if !strings.Contains(hub.Bundle.ConnectionStatus, "guaranteed") {
		t.Errorf("hub ConnectionStatus = %q, want it to mention guaranteed", hub.Bundle.ConnectionStatus)
	}
	if hub.Bundle.ChangeWindowMinutes <= 0 {
		t.Errorf("ChangeWindowMinutes = %d, want > 0", hub.Bundle.ChangeWindowMinutes)
	}

	var sawRail, sawFlight bool
	for _, l := range hub.Bundle.Legs {
		switch l.Mode {
		case BundleLegRail:
			sawRail = true
			if l.DurationMinutes <= 0 {
				t.Errorf("rail leg %s->%s missing timing (DurationMinutes=%d)", l.From, l.To, l.DurationMinutes)
			}
		case BundleLegFlight:
			sawFlight = true
		}
	}
	if !sawRail || !sawFlight {
		t.Errorf("bundle must include BOTH a rail and a flight leg (rail=%v flight=%v)", sawRail, sawFlight)
	}

	// Alias origin (ANR != hub): the rail leg is a self-transfer, not guaranteed.
	alias := composeRailFlyBundle(context.Background(), "ANR", "BCN", "2026-05-01", "", testRailFlyStation, 240, 300, "EUR")
	if alias.Bundle.ConnectionGuaranteed {
		t.Error("alias-origin connection must NOT be guaranteed (self-transfer)")
	}
	if !strings.Contains(alias.Bundle.ConnectionStatus, "self-transfer") {
		t.Errorf("alias ConnectionStatus = %q, want it to mention self-transfer", alias.Bundle.ConnectionStatus)
	}
}

// TestRailFlyBundleRoundTripHasReturnLegs verifies a round-trip composes four
// legs (rail + flight out, flight + rail return) with the return rail priced.
func TestRailFlyBundleRoundTripHasReturnLegs(t *testing.T) {
	withRailGroundSearcher(t, railProviderError())

	h := composeRailFlyBundle(context.Background(), "ANR", "BCN", "2026-05-01", "2026-05-08", testRailFlyStation, 200, 400, "EUR")
	if h.Bundle == nil || len(h.Bundle.Legs) != 4 {
		t.Fatalf("round-trip bundle should have 4 legs, got %d", len(h.Bundle.Legs))
	}
	estRail := groundCostBetween(testRailFlyStation.IATA, testRailFlyStation.HubIATA)
	// Two charged rail legs (out + return) + the round-trip flight fare.
	wantTotal := roundSavings(200 + 2*estRail)
	if h.Bundle.TotalCost != wantTotal {
		t.Errorf("round-trip TotalCost = %v, want %v", h.Bundle.TotalCost, wantTotal)
	}
}

// --- MIK-3079.AC.3 ---

// TestOpenJawRailReturn verifies the open-jaw rail-return composer produces a
// hack whose outbound is a flight and whose return composes a rail leg (fly into
// one city, train out of another), priced offline via the groundCostBetween
// fallback.
func TestOpenJawRailReturn(t *testing.T) {
	withRailGroundSearcher(t, railProviderError())

	const (
		flightCost = 150.0
		baseline   = 400.0
		origin     = "HEL"
		flyInto    = "BCN"
		trainOut   = "GRO"
	)
	h := composeOpenJawRailReturn(context.Background(), origin, flyInto, trainOut, flightCost, baseline, "EUR", "2026-05-01", "2026-05-08")

	if h.Type != "open_jaw_rail_return" {
		t.Errorf("Type = %q, want open_jaw_rail_return", h.Type)
	}
	if h.Bundle == nil || len(h.Bundle.Legs) != 2 {
		t.Fatalf("open-jaw bundle should have 2 legs (flight out, rail return), got %v", h.Bundle)
	}

	out := h.Bundle.Legs[0]
	ret := h.Bundle.Legs[1]
	if out.Mode != BundleLegFlight {
		t.Errorf("outbound leg Mode = %q, want flight", out.Mode)
	}
	if out.From != origin || out.To != flyInto {
		t.Errorf("outbound leg = %s->%s, want %s->%s", out.From, out.To, origin, flyInto)
	}
	if ret.Mode != BundleLegRail {
		t.Errorf("return leg Mode = %q, want rail", ret.Mode)
	}
	if ret.From != trainOut || ret.To != origin {
		t.Errorf("return leg = %s->%s, want %s->%s", ret.From, ret.To, trainOut, origin)
	}
	if !ret.Estimated {
		t.Error("rail return priced via the groundCostBetween fallback must be flagged Estimated")
	}

	estRail := groundCostBetween(trainOut, origin)
	wantTotal := roundSavings(flightCost + estRail)
	if h.Bundle.TotalCost != wantTotal {
		t.Errorf("open-jaw TotalCost = %v, want %v (flight %v + rail %v)", h.Bundle.TotalCost, wantTotal, flightCost, estRail)
	}
	if want := roundSavings(baseline - wantTotal); h.Savings != want {
		t.Errorf("Savings = %v, want %v", h.Savings, want)
	}
}

// TestOpenJawRailReturn_liveQuoteWins verifies a live ground quote is used (and
// not flagged as an estimate) when the provider returns a price.
func TestOpenJawRailReturn_liveQuoteWins(t *testing.T) {
	withRailGroundSearcher(t, liveRailQuote(48, "renfe"))

	h := composeOpenJawRailReturn(context.Background(), "HEL", "BCN", "GRO", 150, 400, "EUR", "2026-05-01", "2026-05-08")
	ret := h.Bundle.Legs[1]
	if ret.Estimated {
		t.Error("with a live quote the rail return should not be flagged Estimated")
	}
	if ret.Cost != 48 {
		t.Errorf("rail return Cost = %v, want 48 (live quote)", ret.Cost)
	}
	if ret.Provider != "renfe" {
		t.Errorf("rail return Provider = %q, want renfe", ret.Provider)
	}
}
