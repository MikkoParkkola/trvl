package travelgraph_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/travelgraph"
	"github.com/MikkoParkkola/trvl/internal/watch"
)

// now is a fixed reference timestamp for test data.
var now = time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

func pt(watchID string, price float64, currency string, hoursAgo float64) watch.PricePoint {
	return watch.PricePoint{
		WatchID:   watchID,
		Price:     price,
		Currency:  currency,
		Timestamp: now.Add(-time.Duration(hoursAgo * float64(time.Hour))),
	}
}

// historyWithSpread builds n points for a given watchID and currency, spread
// around basePrice with ±spread, oldest first, all before now.
func historyWithSpread(watchID, currency string, n int, basePrice, spread float64) []watch.PricePoint {
	pts := make([]watch.PricePoint, n)
	for i := range pts {
		delta := spread * float64(i-n/2) / float64(n)
		pts[i] = watch.PricePoint{
			WatchID:   watchID,
			Price:     basePrice + delta,
			Currency:  currency,
			Timestamp: now.Add(-time.Duration((n - i) * int(time.Hour))),
		}
	}
	return pts
}

// --- AC.1: below-threshold watch produces exactly one grounded nudge ---

func TestBelowThreshold_OneNudgeWithWatchIDInSources(t *testing.T) {
	// GIVEN: a watch whose LastPrice has crossed below BelowPrice
	w := watch.Watch{
		ID:          "w1",
		Origin:      "HEL",
		Destination: "BER",
		BelowPrice:  200.0,
		LastPrice:   180.0,
		Currency:    "EUR",
	}
	history := []watch.PricePoint{pt("w1", 180.0, "EUR", 1)}

	// WHEN
	g := travelgraph.Build([]watch.Watch{w}, history, nil, nil)
	nudges := travelgraph.Nudges(g, now)

	// THEN: exactly one nudge, cites the watch ID
	if len(nudges) != 1 {
		t.Fatalf("expected 1 nudge, got %d: %v", len(nudges), nudges)
	}
	n := nudges[0]
	if n.Kind != travelgraph.KindBelowThreshold {
		t.Errorf("expected KindBelowThreshold, got %q", n.Kind)
	}
	if !containsSource(n.Sources, "w1") {
		t.Errorf("expected Sources to contain %q, got %v", "w1", n.Sources)
	}
}

// --- AC.1 edge: LastPrice exactly at threshold still fires ---

func TestBelowThreshold_AtExactThreshold_Fires(t *testing.T) {
	w := watch.Watch{
		ID:          "w-exact",
		Origin:      "AMS",
		Destination: "LIS",
		BelowPrice:  150.0,
		LastPrice:   150.0,
		Currency:    "EUR",
	}

	g := travelgraph.Build([]watch.Watch{w}, nil, nil, nil)
	nudges := travelgraph.Nudges(g, now)

	if len(nudges) != 1 {
		t.Fatalf("expected 1 nudge at exact threshold, got %d", len(nudges))
	}
	if nudges[0].Kind != travelgraph.KindBelowThreshold {
		t.Errorf("expected KindBelowThreshold, got %q", nudges[0].Kind)
	}
}

// --- AC.2: no grounded trigger → zero nudges (anti-speculation guarantee) ---

func TestNoGroundedTrigger_ZeroNudges(t *testing.T) {
	// GIVEN: a watch above its threshold, flat history → fareintel "watch" not "buy"
	w := watch.Watch{
		ID:          "w2",
		Origin:      "HEL",
		Destination: "LHR",
		BelowPrice:  200.0,
		LastPrice:   250.0,
		Currency:    "EUR",
	}
	history := []watch.PricePoint{
		pt("w2", 250.0, "EUR", 72),
		pt("w2", 252.0, "EUR", 48),
		pt("w2", 248.0, "EUR", 24),
	}

	// WHEN
	g := travelgraph.Build([]watch.Watch{w}, history, nil, nil)
	nudges := travelgraph.Nudges(g, now)

	// THEN: no nudges
	if len(nudges) != 0 {
		t.Errorf("expected 0 nudges, got %d: %v", len(nudges), nudges)
	}
}

// --- AC.2 variant: watch above threshold, no history → zero nudges ---

func TestWatchAboveThresholdNoHistory_ZeroNudges(t *testing.T) {
	w := watch.Watch{
		ID:          "w3",
		Origin:      "CPH",
		Destination: "CDG",
		BelowPrice:  100.0,
		LastPrice:   130.0,
		Currency:    "EUR",
	}

	g := travelgraph.Build([]watch.Watch{w}, nil, nil, nil)
	nudges := travelgraph.Nudges(g, now)

	if len(nudges) != 0 {
		t.Errorf("expected 0 nudges, got %d", len(nudges))
	}
}

// --- AC.3: historic-low route (confident observations, current in low band) ---

func TestHistoricLow_ConfidentAndLowBand_FiresNudge(t *testing.T) {
	// GIVEN: 11 historical points near 300 EUR median + a 220 EUR current
	// fareintel.Analyze needs >= 10 points for confidence="high"
	w := watch.Watch{
		ID:          "w-hist",
		Origin:      "HEL",
		Destination: "NYC",
		BelowPrice:  0,
		LastPrice:   220.0,
		Currency:    "EUR",
	}
	history := historyWithSpread("w-hist", "EUR", 11, 300.0, 40.0)
	history = append(history, pt("w-hist", 220.0, "EUR", 0.5))

	// WHEN
	g := travelgraph.Build([]watch.Watch{w}, history, nil, nil)
	nudges := travelgraph.Nudges(g, now)

	// THEN: exactly one KindHistoricLow nudge citing the route key
	lowNudges := filterKind(nudges, travelgraph.KindHistoricLow)
	if len(lowNudges) == 0 {
		t.Fatalf("expected a KindHistoricLow nudge, got %d nudge(s): %v", len(nudges), nudges)
	}
	n := lowNudges[0]
	expectedKey := "HEL-NYC"
	if !containsSource(n.Sources, expectedKey) {
		t.Errorf("expected Sources to contain %q, got %v", expectedKey, n.Sources)
	}
}

// --- AC.4: every emitted nudge has len(Sources) >= 1 ---

func TestAllNudgesHaveAtLeastOneSource(t *testing.T) {
	// GIVEN: a below-threshold watch and a historic-low route on distinct routes
	wBelow := watch.Watch{
		ID: "wb1", Origin: "AMS", Destination: "BCN",
		BelowPrice: 80.0, LastPrice: 75.0, Currency: "EUR",
	}
	wHist := watch.Watch{
		ID: "wh1", Origin: "HEL", Destination: "DXB",
		BelowPrice: 0, LastPrice: 400.0, Currency: "EUR",
	}
	histHist := historyWithSpread("wh1", "EUR", 11, 600.0, 80.0)
	histHist = append(histHist, pt("wh1", 400.0, "EUR", 1))

	g := travelgraph.Build(
		[]watch.Watch{wBelow, wHist},
		append([]watch.PricePoint{pt("wb1", 75.0, "EUR", 2)}, histHist...),
		nil, nil,
	)
	nudges := travelgraph.Nudges(g, now)

	if len(nudges) == 0 {
		t.Fatal("expected at least one nudge")
	}
	for i, n := range nudges {
		if len(n.Sources) < 1 {
			t.Errorf("nudge[%d] has empty Sources: %+v", i, n)
		}
	}
}

// --- AC.4 invariant: empty inputs → zero nudges, no panic ---

func TestEmptyInputs_NoPanicZeroNudges(t *testing.T) {
	g := travelgraph.Build(nil, nil, nil, nil)
	nudges := travelgraph.Nudges(g, now)
	if len(nudges) != 0 {
		t.Errorf("expected 0 nudges on empty graph, got %d", len(nudges))
	}
}

// --- MIK-6233.AC.2 (TRVL.PTG.2): every emitted nudge carries >=1 SourceRef ---

// TestNudgesCarrySources asserts the core grounding invariant: every nudge that
// Nudges emits carries a non-empty Sources slice, so a caller can always trace
// a nudge back to the source record that produced it. The graph mixes a
// below-threshold watch and a historic-low route to exercise both producers.
func TestNudgesCarrySources(t *testing.T) {
	wBelow := watch.Watch{
		ID: "carry-below", Origin: "AMS", Destination: "BCN",
		BelowPrice: 80.0, LastPrice: 75.0, Currency: "EUR",
	}
	wHist := watch.Watch{
		ID: "carry-hist", Origin: "HEL", Destination: "DXB",
		BelowPrice: 0, LastPrice: 400.0, Currency: "EUR",
	}
	histHist := historyWithSpread("carry-hist", "EUR", 11, 600.0, 80.0)
	histHist = append(histHist, pt("carry-hist", 400.0, "EUR", 1))

	g := travelgraph.Build(
		[]watch.Watch{wBelow, wHist},
		append([]watch.PricePoint{pt("carry-below", 75.0, "EUR", 2)}, histHist...),
		nil, nil,
	)
	nudges := travelgraph.Nudges(g, now)

	if len(nudges) == 0 {
		t.Fatal("expected at least one nudge to assert the Sources invariant against")
	}
	for i, n := range nudges {
		if len(n.Sources) == 0 {
			t.Errorf("nudge[%d] (%s) has empty Sources: %+v", i, n.Kind, n)
			continue
		}
		for j, s := range n.Sources {
			if s.ID == "" {
				t.Errorf("nudge[%d].Sources[%d] has empty ID: %+v", i, j, s)
			}
		}
	}
}

// --- MIK-6233.AC.3 (TRVL.PTG.3): nudges fire ONLY on grounded triggers ---

// TestNudgesGroundedOnly asserts the anti-speculation guarantee: a graph with no
// grounded trigger (a watch above its target and flat price history that
// fareintel does not rate as a confident "buy") yields zero nudges.
func TestNudgesGroundedOnly(t *testing.T) {
	w := watch.Watch{
		ID:          "no-trigger",
		Origin:      "HEL",
		Destination: "LHR",
		BelowPrice:  200.0, // target far below current — not crossed
		LastPrice:   250.0,
		Currency:    "EUR",
	}
	// Flat history near the current price → fareintel verdict is "watch", not a
	// confident "buy", so no historic-low trigger either.
	history := historyWithSpread("no-trigger", "EUR", 11, 250.0, 5.0)
	history = append(history, pt("no-trigger", 250.0, "EUR", 0.5))

	graphWithNoTrigger := travelgraph.Build([]watch.Watch{w}, history, nil, nil)

	if got := travelgraph.Nudges(graphWithNoTrigger, now); len(got) != 0 {
		t.Errorf("expected 0 nudges from a graph with no grounded trigger, got %d: %v", len(got), got)
	}
}

// --- No BelowPrice configured → threshold check never fires ---

func TestNoBelowPrice_NeverFiresThreshold(t *testing.T) {
	w := watch.Watch{
		ID:          "w-nobp",
		Origin:      "FRA",
		Destination: "JFK",
		BelowPrice:  0,
		LastPrice:   350.0,
		Currency:    "EUR",
	}

	g := travelgraph.Build([]watch.Watch{w}, nil, nil, nil)
	nudges := filterKind(travelgraph.Nudges(g, now), travelgraph.KindBelowThreshold)

	if len(nudges) != 0 {
		t.Errorf("expected no threshold nudges when BelowPrice=0, got %d", len(nudges))
	}
}

// --- orphan price point (unknown watch ID) is silently skipped ---

func TestOrphanPricePoint_Ignored(t *testing.T) {
	// GIVEN: a history point whose WatchID has no corresponding watch
	orphan := watch.PricePoint{
		WatchID:   "nonexistent-watch",
		Price:     100.0,
		Currency:  "EUR",
		Timestamp: now,
	}

	// WHEN
	g := travelgraph.Build(nil, []watch.PricePoint{orphan}, nil, nil)
	nudges := travelgraph.Nudges(g, now)

	// THEN: no panic, no nudges
	if len(nudges) != 0 {
		t.Errorf("expected 0 nudges for orphan point, got %d", len(nudges))
	}
}

// --- historic-low: fewer than 3 history points → no nudge (guard branch) ---

func TestHistoricLow_TooFewPoints_NoNudge(t *testing.T) {
	w := watch.Watch{
		ID: "w-few", Origin: "OSL", Destination: "MAD",
		BelowPrice: 0, LastPrice: 120.0, Currency: "EUR",
	}
	history := []watch.PricePoint{
		pt("w-few", 200.0, "EUR", 48),
		pt("w-few", 120.0, "EUR", 1),
	}
	g := travelgraph.Build([]watch.Watch{w}, history, nil, nil)
	nudges := filterKind(travelgraph.Nudges(g, now), travelgraph.KindHistoricLow)
	if len(nudges) != 0 {
		t.Errorf("expected 0 historic-low nudges with only 2 points, got %d", len(nudges))
	}
}

// --- helpers ---

func containsSource(sources []travelgraph.SourceRef, id string) bool {
	for _, s := range sources {
		if s.ID == id {
			return true
		}
	}
	return false
}

func filterKind(nudges []travelgraph.Nudge, kind travelgraph.NudgeKind) []travelgraph.Nudge {
	var out []travelgraph.Nudge
	for _, n := range nudges {
		if n.Kind == kind {
			out = append(out, n)
		}
	}
	return out
}

// Compile-time check: Nudge, NudgeKind, Build, Nudges are exported.
var _ = fmt.Sprintf("%T", travelgraph.Nudge{})

// ptRoute builds a route-keyed PricePoint (MIK-6229 ad-hoc corpus).
func ptRoute(routeKey string, price float64, currency string, hoursAgo float64) watch.PricePoint {
	return watch.PricePoint{
		RouteKey:  routeKey,
		Price:     price,
		Currency:  currency,
		Timestamp: now.Add(-time.Duration(hoursAgo * float64(time.Hour))),
	}
}

// historyRouteKeyed builds n route-keyed points for a given route key, spread
// around basePrice with ±spread, oldest first.
func historyRouteKeyed(routeKey, currency string, n int, basePrice, spread float64) []watch.PricePoint {
	pts := make([]watch.PricePoint, n)
	for i := range pts {
		delta := spread * float64(i-n/2) / float64(n)
		pts[i] = watch.PricePoint{
			RouteKey:  routeKey,
			Price:     basePrice + delta,
			Currency:  currency,
			Timestamp: now.Add(-time.Duration((n - i) * int(time.Hour))),
		}
	}
	return pts
}

// --- MIK-6229 / MIK-6233: route-keyed corpus drives historic-low nudges ---

// TestRouteKeyedCorpus_FiresHistoricLowWithNoWatch verifies that >= 10
// route-keyed observations with the current price well below the median
// produce a KindHistoricLow nudge even when NO watch exists for that route.
// This is the core contract of MIK-6229/MIK-6233: the ad-hoc corpus must
// actually drive nudges.
func TestRouteKeyedCorpus_FiresHistoricLowWithNoWatch(t *testing.T) {
	// GIVEN: 10 route-keyed history points at ~500 EUR median, plus a current
	// point at 390 EUR (22% below median → fareintel "buy" + "high" confidence).
	rk := "FLIGHT|AMS|VLC|2026-07-15"
	history := historyRouteKeyed(rk, "EUR", 10, 500.0, 60.0)
	history = append(history, ptRoute(rk, 390.0, "EUR", 0.5))

	// WHEN: no watches at all — the route corpus is the only input
	g := travelgraph.Build(nil, history, nil, nil)
	nudges := travelgraph.Nudges(g, now)

	// THEN: exactly one KindHistoricLow nudge citing the route key "AMS-VLC"
	lowNudges := filterKind(nudges, travelgraph.KindHistoricLow)
	if len(lowNudges) == 0 {
		t.Fatalf("expected a KindHistoricLow nudge from route-keyed corpus, got %d nudge(s): %v", len(nudges), nudges)
	}
	n := lowNudges[0]
	const expectedRouteKey = "AMS-VLC"
	if !containsSource(n.Sources, expectedRouteKey) {
		t.Errorf("expected Sources to contain %q, got %v", expectedRouteKey, n.Sources)
	}
}

// TestRouteKeyedCorpus_MalformedKey_Skipped ensures that a route-keyed point
// with a malformed key (fewer than 3 pipe-separated segments) does not panic
// and is silently skipped.
func TestRouteKeyedCorpus_MalformedKey_Skipped(t *testing.T) {
	history := []watch.PricePoint{
		{RouteKey: "BADKEY", Price: 100.0, Currency: "EUR", Timestamp: now},
		{RouteKey: "FLIGHT|AMS", Price: 200.0, Currency: "EUR", Timestamp: now},
		{RouteKey: "", Price: 0, Currency: "EUR", Timestamp: now},
	}
	g := travelgraph.Build(nil, history, nil, nil)
	nudges := travelgraph.Nudges(g, now)
	if len(nudges) != 0 {
		t.Errorf("expected 0 nudges for malformed route keys, got %d: %v", len(nudges), nudges)
	}
}

// TestRouteKeyedCorpus_HotelKey_Skipped verifies that hotel-shaped route keys
// (non-FLIGHT kind) are silently skipped without panic.
func TestRouteKeyedCorpus_HotelKey_Skipped(t *testing.T) {
	// Hotel keys have a different shape; they must not be misrouted as flights.
	history := []watch.PricePoint{
		{RouteKey: "HOTEL|AMS|MARRIOTT|2026-07-15", Price: 250.0, Currency: "EUR", Timestamp: now},
	}
	g := travelgraph.Build(nil, history, nil, nil)
	nudges := travelgraph.Nudges(g, now)
	if len(nudges) != 0 {
		t.Errorf("expected 0 nudges for hotel-kind route key, got %d: %v", len(nudges), nudges)
	}
}

// TestRouteKeyedCorpus_MergesWithWatchHistory verifies that route-keyed points
// and watch-keyed points for the same route are merged into a single history
// bucket. The combined corpus should reach the confidence threshold that
// neither source alone might satisfy.
func TestRouteKeyedCorpus_MergesWithWatchHistory(t *testing.T) {
	// GIVEN: 5 watch-keyed + 5 route-keyed points on HEL-NYC, totalling 10
	// (confidence="high" threshold); current price 22% below median.
	w := watch.Watch{
		ID:          "w-merge",
		Origin:      "HEL",
		Destination: "NYC",
		BelowPrice:  0,
		LastPrice:   400.0,
		Currency:    "EUR",
	}
	rk := "FLIGHT|HEL|NYC|2026-08-01"
	watchPts := historyWithSpread("w-merge", "EUR", 5, 520.0, 40.0)
	routePts := historyRouteKeyed(rk, "EUR", 5, 520.0, 40.0)
	currentPt := ptRoute(rk, 400.0, "EUR", 0.5)
	history := append(append(watchPts, routePts...), currentPt)

	// WHEN
	g := travelgraph.Build([]watch.Watch{w}, history, nil, nil)
	nudges := travelgraph.Nudges(g, now)

	// THEN: merged corpus reaches high-confidence → historic-low nudge fires
	lowNudges := filterKind(nudges, travelgraph.KindHistoricLow)
	if len(lowNudges) == 0 {
		t.Fatalf("expected KindHistoricLow from merged watch+route corpus, got %d nudge(s): %v", len(nudges), nudges)
	}
}

// TestNoGroundedTrigger_RouteKeyed_ZeroNudges confirms the anti-speculation
// guarantee holds for route-keyed points: flat history (price near median)
// must not emit a nudge.
func TestNoGroundedTrigger_RouteKeyed_ZeroNudges(t *testing.T) {
	// GIVEN: 10 route-keyed points all at ~300 EUR — no material dip
	rk := "FLIGHT|FRA|LHR|2026-09-10"
	history := historyRouteKeyed(rk, "EUR", 10, 300.0, 5.0) // tiny spread, price near median
	// Current price at median — not a "buy"
	history = append(history, ptRoute(rk, 300.0, "EUR", 0.5))

	g := travelgraph.Build(nil, history, nil, nil)
	nudges := travelgraph.Nudges(g, now)

	if len(nudges) != 0 {
		t.Errorf("expected 0 nudges (flat route-keyed history), got %d: %v", len(nudges), nudges)
	}
}
