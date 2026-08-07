package watch

import (
	"strconv"
	"testing"
)

// newCappedStore returns a loaded store whose per-watch retention cap is small,
// plus the cap in force.
//
// Every RecordPrice is a committed transaction, so a test that pushes the
// production cap of 1000 past its limit performs ~1500 disk-syncing writes. Four
// such tests were 54s of this package's 83s locally, and on 2026-08-06 the
// package hit Go's 600s per-package ceiling on a CI runner and failed a merge on
// a PR that had not touched it (trvl#585, run 31055857360).
//
// Driving a cap of 20 over its limit proves exactly what driving 1000 over its
// limit proves, for 2% of the writes. The rule this follows: invert the budget
// rather than generate the load. A test that races a real clock is measuring the
// runner, not the code.
//
// The override is the same one operators get (TRVL_WATCH_MAX_POINTS_PER_WATCH),
// so this exercises the shipping configuration path rather than a test-only back
// door.
//
// It returns the store already Loaded, and that is the whole reason it exists as
// a constructor rather than a bare setenv helper: retention is read ONLY by
// Load (store.go:104). withTxn reloads committed state through loadLocked,
// which does not re-read it. So a store built with NewStore and never loaded
// silently keeps the compiled 1000 default, the override appears to do nothing,
// and the test still passes -- it just writes 50x more than intended. Handing
// back a loaded store makes that mistake unavailable.
//
// t.Setenv is deliberate: it forbids t.Parallel in these tests, and they must
// not run in parallel anyway because they each drive a shared cap.
func newCappedStore(t *testing.T) (*Store, int) {
	t.Helper()
	const capacity = 20
	t.Setenv(EnvMaxPointsPerWatch, strconv.Itoa(capacity))
	s := NewStore(t.TempDir())
	if err := s.Load(); err != nil {
		t.Fatalf("load store with %s=%d: %v", EnvMaxPointsPerWatch, capacity, err)
	}
	return s, capacity
}

// The cap helper must actually lower the cap. Without this, a future change to
// where retention is read turns every test below into a slow no-op that still
// passes -- which is exactly the failure this helper was written to escape.
func TestNewCappedStoreReallyLowersTheCap(t *testing.T) {
	s, capacity := newCappedStore(t)
	if capacity >= maxObservationsPerWatch {
		t.Fatalf("test cap %d is not below the production cap %d", capacity, maxObservationsPerWatch)
	}
	if got := s.retentionOrDefault().MaxPointsPerWatch; got != capacity {
		t.Fatalf("store retains %d points per watch, want the injected %d -- the override did not "+
			"reach the store, so every test using this helper is writing 50x what it thinks", got, capacity)
	}
}
