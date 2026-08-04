package watch

import (
	"context"
	"sync"
	"testing"
	"time"
)

// Watch identity is TARGET-level: one watch per route, and re-watching a route
// with a different price target ADJUSTS that watch rather than adding a second
// one (operator decision, 2026-08-02).
//
// These tests previously encoded the opposite rule -- #509's threshold-aware
// identity, under which "alert me at 200" and "alert me at 120" coexisted as
// two records. They are rewritten, not deleted: the duplicate-accumulation
// behaviour #509 was really protecting (a repeated request must not pile up
// records, and a re-watch must not destroy accumulated history) still matters
// and is still asserted here. What changed is the answer to "is a new price a
// new intent, or a correction of the old one?" -- it is now a correction.
//
// The cost is deliberate and accepted: there is no way to hold two price
// targets on one route.

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

// WATCHID.1 + WATCHID.2: a second price target on a watched route adjusts that
// watch, and an identical repeat changes nothing. Neither accumulates records.
func TestAddAdjustsThresholdAndCollapsesRepeats(t *testing.T) {
	s := NewStore(t.TempDir())

	id1, created, err := s.Add(routeWatch509(200))
	if err != nil {
		t.Fatalf("add first threshold: %v", err)
	}
	if !created {
		t.Fatalf("WATCHID.1: first add reported created=false")
	}

	id2, created, err := s.Add(routeWatch509(120))
	if err != nil {
		t.Fatalf("add second threshold: %v", err)
	}
	if created {
		t.Errorf("WATCHID.1: adjusting the price reported created=true; it updates the existing watch")
	}
	if id2 != id1 {
		t.Fatalf("WATCHID.1: a new price target forked watch %s from %s; it must adjust the existing one", id2, id1)
	}
	if got := len(s.List()); got != 1 {
		t.Fatalf("WATCHID.1: want 1 watch for one route, got %d", got)
	}
	if w, _ := s.Get(id1); w.BelowPrice != 120 {
		t.Errorf("WATCHID.1: target price = %v, want the re-watched 120", w.BelowPrice)
	}

	// Same route, same threshold, asked for again: must not create a record.
	repeat, created, err := s.Add(routeWatch509(120))
	if err != nil {
		t.Fatalf("repeat add: %v", err)
	}
	if created {
		t.Errorf("WATCHID.2: identical repeat reported created=true")
	}
	if repeat != id1 {
		t.Fatalf("WATCHID.2: repeat returned new id %s, want existing %s", repeat, id1)
	}
	if got := len(s.List()); got != 1 {
		t.Fatalf("WATCHID.2: identical repeat accumulated, want 1 watch, got %d", got)
	}

	// The dedupe must survive a reload: it is committed state, not a
	// process-local snapshot.
	fresh := NewStore(s.dir)
	if err := fresh.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := len(fresh.List()); got != 1 {
		t.Fatalf("WATCHID.2: reload sees %d watches, want 1", got)
	}
}

// WATCHID.3: distinct targets are polled once each per round, and pollcache
// collapses concurrent checks of the SAME target onto one provider call.
func TestCheckAllBoundedPollsEachTargetOnce(t *testing.T) {
	s := NewStore(t.TempDir())
	if _, _, err := s.Add(routeWatch509(200)); err != nil {
		t.Fatalf("add first route: %v", err)
	}
	// A genuinely different target, to prove polling keys on the target and
	// does not simply collapse everything to one call.
	other := routeWatch509(300)
	other.Destination = "BCN"
	if _, _, err := s.Add(other); err != nil {
		t.Fatalf("add second route: %v", err)
	}

	checker := newCountingChecker509(150)
	results := CheckAllBounded(context.Background(), s, checker, nil, BoundedOptions{})

	if len(results) != 2 {
		t.Fatalf("want 2 results (one per watch), got %d", len(results))
	}
	for i, r := range results {
		if r.Error != nil {
			t.Fatalf("result %d errored: %v", i, r.Error)
		}
	}
	if got := checker.total(); got != 2 {
		t.Fatalf("WATCHID.3: want 1 provider call per distinct target (2), got %d", got)
	}
}

// WATCHID.4: the adjusted threshold is the one that decides below-goal. The
// superseded value must not keep firing.
func TestAdjustedThresholdIsTheOneThatFires(t *testing.T) {
	s := NewStore(t.TempDir())
	id, _, err := s.Add(routeWatch509(200)) // 150 <= 200 would fire
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	// Owner changes their mind: only below 120 is interesting now.
	if _, _, err := s.Add(routeWatch509(120)); err != nil {
		t.Fatalf("adjust: %v", err)
	}

	results := CheckAllBounded(context.Background(), s, newCountingChecker509(150), nil, BoundedOptions{})
	if len(results) != 1 {
		t.Fatalf("want 1 result for 1 watch, got %d", len(results))
	}
	if results[0].BelowGoal {
		t.Errorf("WATCHID.4: below-goal fired at 150 against the SUPERSEDED target 200; " +
			"the watch was adjusted to 120")
	}

	w, ok := s.Get(id)
	if !ok {
		t.Fatalf("watch %s vanished", id)
	}
	if w.LastPrice != 150 {
		t.Errorf("WATCHID.4: recorded LastPrice %v, want 150", w.LastPrice)
	}
	if hist := s.History(id); len(hist) != 1 {
		t.Errorf("WATCHID.4: %d history points, want 1", len(hist))
	}
}

// WATCHID.5: adjusting the price keeps the history, lowest price and creation
// date. This is the property that makes a long-running watch worth anything,
// and it is why a re-watch must not be implemented as delete-and-recreate.
func TestAddRepeatPreservesHistoryAndCreation(t *testing.T) {
	s := NewStore(t.TempDir())
	id, _, err := s.Add(routeWatch509(200))
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
	// Adjusted, not merely repeated: the accumulated state must survive a
	// genuine change of mind about the price, not only an identical request.
	if _, _, err := s.Add(routeWatch509(150)); err != nil {
		t.Fatalf("adjust: %v", err)
	}

	after, ok := s.Get(id)
	if !ok {
		t.Fatalf("WATCHID.5: watch %s lost after adjusting the price", id)
	}
	if got := len(s.List()); got != 1 {
		t.Fatalf("WATCHID.5: adjusting left %d watches, want the original 1", got)
	}
	if !after.CreatedAt.Equal(before.CreatedAt) {
		t.Fatalf("WATCHID.5: creation date changed %v -> %v", before.CreatedAt, after.CreatedAt)
	}
	if after.LowestPrice != 180 {
		t.Fatalf("WATCHID.5: lowest price reset to %v, want 180", after.LowestPrice)
	}
	if after.BelowPrice != 150 {
		t.Fatalf("WATCHID.5: target price = %v, want the adjusted 150", after.BelowPrice)
	}
	if got := len(s.History(id)); got != 1 {
		t.Fatalf("WATCHID.5: history reset to %d points, want the accumulated 1", got)
	}
}
