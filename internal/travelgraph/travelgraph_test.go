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
	nudges := travelgraph.Nudges(g)

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
	nudges := travelgraph.Nudges(g)

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
	nudges := travelgraph.Nudges(g)

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
	nudges := travelgraph.Nudges(g)

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
	nudges := travelgraph.Nudges(g)

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
	nudges := travelgraph.Nudges(g)

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
	nudges := travelgraph.Nudges(g)
	if len(nudges) != 0 {
		t.Errorf("expected 0 nudges on empty graph, got %d", len(nudges))
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
	nudges := filterKind(travelgraph.Nudges(g), travelgraph.KindBelowThreshold)

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
	nudges := travelgraph.Nudges(g)

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
	nudges := filterKind(travelgraph.Nudges(g), travelgraph.KindHistoricLow)
	if len(nudges) != 0 {
		t.Errorf("expected 0 historic-low nudges with only 2 points, got %d", len(nudges))
	}
}

// --- helpers ---

func containsSource(sources []string, id string) bool {
	for _, s := range sources {
		if s == id {
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
