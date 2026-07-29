package watch

import (
	"context"
	"sync"
	"testing"
	"time"
)

// countingChecker509 records how many provider calls were issued and for which
// polled targets, so tests can assert the observation count directly rather
// than inferring it from the number of watches.
type countingChecker509 struct {
	mu     sync.Mutex
	calls  int
	perKey map[string]int
	price  float64
}

func newCountingChecker509(price float64) *countingChecker509 {
	return &countingChecker509{perKey: make(map[string]int), price: price}
}

func (c *countingChecker509) CheckPrice(_ context.Context, w Watch) (float64, string, string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	c.perKey[w.pollKey()]++
	return c.price, "EUR", "", nil
}

func (c *countingChecker509) total() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func routeWatch509(below float64) Watch {
	return Watch{Type: "flight", Origin: "AMS", Destination: "VLC", BelowPrice: below, Currency: "EUR"}
}

// MULTIPRICE.1 + MULTIPRICE.4: two thresholds on one route coexist as separate
// intents, while repeating an identical request does not accumulate.
func TestAddSeparatesThresholdsAndCollapsesRepeats(t *testing.T) {
	s := NewStore(t.TempDir())

	id1, err := s.Add(routeWatch509(200))
	if err != nil {
		t.Fatalf("add first threshold: %v", err)
	}
	id2, err := s.Add(routeWatch509(120))
	if err != nil {
		t.Fatalf("add second threshold: %v", err)
	}

	if id1 == id2 {
		t.Fatalf("MULTIPRICE.1: distinct thresholds collapsed into one watch %s", id1)
	}
	if got := len(s.List()); got != 2 {
		t.Fatalf("MULTIPRICE.1: want 2 watches for 2 thresholds, got %d", got)
	}

	// Same route, same threshold, asked for again: must not create a record.
	repeat, err := s.Add(routeWatch509(200))
	if err != nil {
		t.Fatalf("repeat add: %v", err)
	}
	if repeat != id1 {
		t.Fatalf("MULTIPRICE.4: repeat returned new id %s, want existing %s", repeat, id1)
	}
	if got := len(s.List()); got != 2 {
		t.Fatalf("MULTIPRICE.4: identical repeat accumulated, want 2 watches, got %d", got)
	}

	// The dedupe must survive a reload: it is committed state, not a
	// process-local snapshot.
	fresh := NewStore(s.dir)
	if err := fresh.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := len(fresh.List()); got != 2 {
		t.Fatalf("MULTIPRICE.4: reload sees %d watches, want 2", got)
	}
}

// MULTIPRICE.2: a route shared by several thresholds is polled once per round.
func TestCheckAllBoundedPollsEachTargetOnce(t *testing.T) {
	s := NewStore(t.TempDir())
	for _, below := range []float64{200, 150, 120} {
		if _, err := s.Add(routeWatch509(below)); err != nil {
			t.Fatalf("add %v: %v", below, err)
		}
	}
	// A genuinely different target, to prove the cache keys on the target and
	// does not simply collapse everything to one call.
	other := routeWatch509(300)
	other.Destination = "BCN"
	if _, err := s.Add(other); err != nil {
		t.Fatalf("add second route: %v", err)
	}

	checker := newCountingChecker509(150)
	results := CheckAllBounded(context.Background(), s, checker, nil, BoundedOptions{})

	if len(results) != 4 {
		t.Fatalf("want 4 results (one per watch), got %d", len(results))
	}
	for i, r := range results {
		if r.Error != nil {
			t.Fatalf("result %d errored: %v", i, r.Error)
		}
	}
	if got := checker.total(); got != 2 {
		t.Fatalf("MULTIPRICE.2: want 1 provider call per distinct target (2), got %d", got)
	}
}

// MULTIPRICE.3: each threshold is evaluated independently against the shared
// observation, with its own alert/dedup state on its own record.
func TestThresholdsAlertIndependently(t *testing.T) {
	s := NewStore(t.TempDir())
	idHigh, err := s.Add(routeWatch509(200)) // 150 <= 200 -> should fire
	if err != nil {
		t.Fatalf("add high threshold: %v", err)
	}
	idLow, err := s.Add(routeWatch509(120)) // 150 > 120 -> must not fire
	if err != nil {
		t.Fatalf("add low threshold: %v", err)
	}

	results := CheckAllBounded(context.Background(), s, newCountingChecker509(150), nil, BoundedOptions{})

	byID := make(map[string]CheckResult, len(results))
	for _, r := range results {
		byID[r.Watch.ID] = r
	}
	if !byID[idHigh].BelowGoal {
		t.Fatalf("MULTIPRICE.3: watch %s (target 200) did not report below-goal at price 150", idHigh)
	}
	if byID[idLow].BelowGoal {
		t.Fatalf("MULTIPRICE.3: watch %s (target 120) reported below-goal at price 150", idLow)
	}

	// Each record keeps its own state, so one threshold firing cannot consume
	// the other's dedup window.
	for _, id := range []string{idHigh, idLow} {
		w, ok := s.Get(id)
		if !ok {
			t.Fatalf("watch %s vanished", id)
		}
		if w.LastPrice != 150 {
			t.Fatalf("MULTIPRICE.3: watch %s recorded LastPrice %v, want 150", id, w.LastPrice)
		}
		hist := s.History(id)
		if len(hist) != 1 {
			t.Fatalf("MULTIPRICE.3: watch %s has %d history points, want its own 1", id, len(hist))
		}
	}
}

// MULTIPRICE.5: a pre-existing single-threshold watch keeps its history,
// lowest price and creation date when the same request arrives again.
func TestAddRepeatPreservesHistoryAndCreation(t *testing.T) {
	s := NewStore(t.TempDir())
	id, err := s.Add(routeWatch509(200))
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := s.RecordPrice(id, 180, "EUR"); err != nil {
		t.Fatalf("record price: %v", err)
	}
	if _, err := s.Mutate(id, func(w *Watch) { w.LowestPrice = 180; w.LastPrice = 180 }); err != nil {
		t.Fatalf("mutate: %v", err)
	}
	before, ok := s.Get(id)
	if !ok {
		t.Fatalf("watch %s missing", id)
	}

	time.Sleep(2 * time.Millisecond)
	if _, err := s.Add(routeWatch509(200)); err != nil {
		t.Fatalf("repeat add: %v", err)
	}

	after, ok := s.Get(id)
	if !ok {
		t.Fatalf("MULTIPRICE.5: watch %s lost after repeat add", id)
	}
	if got := len(s.List()); got != 1 {
		t.Fatalf("MULTIPRICE.5: repeat add left %d watches, want the original 1", got)
	}
	if !after.CreatedAt.Equal(before.CreatedAt) {
		t.Fatalf("MULTIPRICE.5: creation date changed %v -> %v", before.CreatedAt, after.CreatedAt)
	}
	if after.LowestPrice != 180 {
		t.Fatalf("MULTIPRICE.5: lowest price reset to %v, want 180", after.LowestPrice)
	}
	hist := s.History(id)
	if len(hist) != 1 || hist[0].Price != 180 {
		t.Fatalf("MULTIPRICE.5: price history lost, got %#v", hist)
	}
}
