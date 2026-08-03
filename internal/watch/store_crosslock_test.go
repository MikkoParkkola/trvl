package watch

import (
	"sync"
	"testing"
)

// TestCrossProcessStoreDoesNotDropConcurrentWrites is the #553 regression
// test: two independent *Store instances (each with its own sync.Mutex,
// modeling the scheduler's Store and the MCP watch_price tool's Store, which
// are genuinely separate *Store values in production) pointed at the same
// directory must not silently clobber each other's Add. Before the #553 fix,
// Store.Add mutated whatever snapshot the process happened to be holding and
// rewrote both files wholesale with no cross-process coordination, so this
// test would lose watches under concurrent writers. Equivalent coverage to
// the deleted internal/watch/txn_test.go (e6ad617).
func TestCrossProcessStoreDoesNotDropConcurrentWrites(t *testing.T) {
	dir := t.TempDir()

	const n = 40
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Each goroutine uses its OWN *Store, modeling two separate
			// processes rather than two goroutines sharing one in-process
			// mutex.
			s := NewStore(dir)
			dest := [3]byte{byte('A' + i%26), byte('A' + (i/26)%26), byte('A' + (i/676)%26)}
			_, _, err := s.Add(Watch{
				Type:        "flight",
				Origin:      "HEL",
				Destination: string(dest[:]),
				BelowPrice:  100,
				Currency:    "EUR",
			})
			if err != nil {
				t.Errorf("Add from independent store %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	final := NewStore(dir)
	if err := final.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := len(final.List()); got != n {
		t.Fatalf("cross-process writes dropped: store holds %d watches, want %d", got, n)
	}
}

// TestCrossProcessStoreRecordPriceDoesNotLoseUpdates mirrors the same hazard
// for RecordPrice, the hottest RMW path (called once per watch per scheduler
// round): n independent stores each append one price point for the SAME
// watch ID; none of the n appends may be lost.
func TestCrossProcessStoreRecordPriceDoesNotLoseUpdates(t *testing.T) {
	dir := t.TempDir()
	seed := NewStore(dir)
	id, _, err := seed.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "EUR"})
	if err != nil {
		t.Fatalf("seed add: %v", err)
	}

	const n = 40
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := NewStore(dir)
			if err := s.RecordPrice(id, float64(100+i), "EUR"); err != nil {
				t.Errorf("RecordPrice from independent store %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	final := NewStore(dir)
	if err := final.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := len(final.History(id)); got != n {
		t.Fatalf("cross-process RecordPrice calls dropped: %d points recorded, want %d", got, n)
	}
}
