package watch

import (
	"context"
	"testing"
)

// sequencePriceChecker returns a scripted sequence of prices across successive
// CheckPrice calls, letting tests drive a watch through a price trajectory
// deterministically and offline.
type sequencePriceChecker struct {
	prices   []float64
	currency string
	idx      int
}

func (c *sequencePriceChecker) CheckPrice(_ context.Context, _ Watch) (float64, string, string, error) {
	p := c.prices[c.idx]
	if c.idx < len(c.prices)-1 {
		c.idx++
	}
	cur := c.currency
	if cur == "" {
		cur = "EUR"
	}
	return p, cur, "", nil
}

// checkSeq runs one bounded check round (no inter-check delay) and returns the
// single result for the lone watch in the store.
func checkSeq(t *testing.T, store *Store, checker PriceChecker) CheckResult {
	t.Helper()
	results := CheckAllBounded(context.Background(), store, checker, nil, BoundedOptions{})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	return results[0]
}

func newAlertWatchStore(t *testing.T, w Watch) *Store {
	t.Helper()
	store := NewStore(t.TempDir())
	if _, err := store.Add(w); err != nil {
		t.Fatalf("add watch: %v", err)
	}
	return store
}

// TestPriceDropAlert_FiresExactlyOnceBeyondThreshold drives a baseline capture,
// a qualifying drop (one alert), and a re-check at the same price (silent).
func TestPriceDropAlert_FiresExactlyOnceBeyondThreshold(t *testing.T) {
	store := newAlertWatchStore(t, Watch{
		Type: "flight", Origin: "HEL", Destination: "BCN",
		Currency: "EUR", AlertDropPct: 10,
	})
	checker := &sequencePriceChecker{prices: []float64{200, 180, 180}}

	// First check captures the baseline — no alert.
	if r := checkSeq(t, store, checker); r.PriceDropAlert {
		t.Fatalf("baseline capture must not alert")
	}

	// Second check: 10% drop — exactly one alert.
	r := checkSeq(t, store, checker)
	if !r.PriceDropAlert {
		t.Fatalf("expected a price-drop alert at 10%% drop")
	}
	if r.AlertBaseline != 200 {
		t.Fatalf("AlertBaseline = %v, want 200", r.AlertBaseline)
	}

	// Third check at the same price — must not re-alert (dedup).
	if r := checkSeq(t, store, checker); r.PriceDropAlert {
		t.Fatalf("same drop must not re-alert")
	}
}

// TestPriceDropAlert_BelowThresholdSilent verifies a sub-threshold drop is quiet.
func TestPriceDropAlert_BelowThresholdSilent(t *testing.T) {
	store := newAlertWatchStore(t, Watch{
		Type: "flight", Origin: "HEL", Destination: "OUL",
		Currency: "EUR", AlertDropPct: 10,
	})
	checker := &sequencePriceChecker{prices: []float64{200, 190}} // 5% drop

	_ = checkSeq(t, store, checker) // baseline
	if r := checkSeq(t, store, checker); r.PriceDropAlert {
		t.Fatalf("5%% drop must be silent under a 10%% threshold")
	}
}

// TestPriceDropAlert_RaisedPriceDoesNotAlert verifies a recovered/raised fare
// never alerts and re-arms the detector against the new peak.
func TestPriceDropAlert_RaisedPriceDoesNotAlert(t *testing.T) {
	store := newAlertWatchStore(t, Watch{
		Type: "flight", Origin: "HEL", Destination: "LHR",
		Currency: "EUR", AlertDropPct: 10,
	})
	checker := &sequencePriceChecker{prices: []float64{200, 250}} // rises

	_ = checkSeq(t, store, checker) // baseline 200
	if r := checkSeq(t, store, checker); r.PriceDropAlert {
		t.Fatalf("a rising price must not alert")
	}
	// Baseline should track up to the new peak.
	w, _ := store.Get(store.List()[0].ID)
	if w.BaselinePrice != 250 {
		t.Fatalf("baseline should rise to 250, got %v", w.BaselinePrice)
	}
}

// TestPriceDropAlert_BaselineSurvivesReload proves the baseline + dedup state is
// persisted to disk and a reloaded store does not re-alert for the same drop.
func TestPriceDropAlert_BaselineSurvivesReload(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	id, err := store.Add(Watch{
		Type: "flight", Origin: "HEL", Destination: "JFK",
		Currency: "EUR", AlertDropPct: 10,
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	checker := &sequencePriceChecker{prices: []float64{500, 400}} // baseline, then 20% drop
	_ = checkSeq(t, store, checker)                               // baseline 500
	r := checkSeq(t, store, checker)                              // 20% drop -> alert
	if !r.PriceDropAlert {
		t.Fatalf("expected alert on 20%% drop")
	}

	// Reload from disk into a fresh store.
	reloaded := NewStore(dir)
	if err := reloaded.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	w, ok := reloaded.Get(id)
	if !ok {
		t.Fatalf("watch missing after reload")
	}
	if w.BaselinePrice != 500 {
		t.Fatalf("persisted baseline = %v, want 500", w.BaselinePrice)
	}
	if w.LastAlertedPrice != 400 {
		t.Fatalf("persisted LastAlertedPrice = %v, want 400", w.LastAlertedPrice)
	}

	// Re-checking at the same 400 against the reloaded state must stay silent.
	sameChecker := &sequencePriceChecker{prices: []float64{400}}
	if r := checkSeq(t, reloaded, sameChecker); r.PriceDropAlert {
		t.Fatalf("reloaded state must not re-alert for the same drop")
	}
}
