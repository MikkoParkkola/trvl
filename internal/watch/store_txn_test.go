package watch

import (
	"context"
	"os"
	"testing"
	"time"
)

// parkingChecker blocks inside CheckPrice until release is closed, so a test can
// hold a scheduler round open in the exact window between "the round took a
// detached copy of the watch" and "the round persists it".
type parkingChecker struct {
	entered chan struct{}
	release chan struct{}
	price   float64
}

func (c *parkingChecker) CheckPrice(_ context.Context, _ Watch) (float64, string, string, error) {
	close(c.entered)
	<-c.release
	return c.price, "EUR", "", nil
}

// waitFor blocks until ch fires or the test times out.
func waitFor(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// TRVL.STORE.TXN.1 / TRVL.STORE.TXN.3 — two independent writers rooted at the
// same directory each add a different watch; neither may be lost.
//
// Two *Store values are the multi-process model: separate mutexes, separate
// in-memory snapshots, separate lock-file descriptors. flock is held by the open
// file description, so they exclude each other exactly as two processes do.
//
// The assertion is positioned inside the window, not after it. Writer A is
// parked mid-transaction by txnHook, writer B is started while A is still
// parked, and only then is A released. Both sabotage directions fail this test:
// remove the advisory lock and B's whole transaction lands inside A's window so
// A's save erases it; keep the lock but remove the in-transaction reload and B
// wakes up holding a snapshot that predates A's commit and erases A instead.
func TestConcurrentAddsFromSeparateStoresBothSurvive(t *testing.T) {
	if !lockSupported {
		t.Skip("no advisory-lock implementation on this platform")
	}
	dir := t.TempDir()

	a := NewStore(dir)
	b := NewStore(dir)
	// Both writers start from the same (empty) committed state, which is what
	// makes this a lost-update race rather than a sequence.
	if err := a.Load(); err != nil {
		t.Fatalf("load a: %v", err)
	}
	if err := b.Load(); err != nil {
		t.Fatalf("load b: %v", err)
	}

	aParked := make(chan struct{})
	releaseA := make(chan struct{})
	txnHook = func(s *Store, stage txnStage) {
		if s == a && stage == stageAfterReload {
			close(aParked)
			<-releaseA
		}
	}
	t.Cleanup(func() { txnHook = nil })

	aDone := make(chan error, 1)
	go func() {
		_, err := a.Add(Watch{Type: "flight", Origin: "AMS", Destination: "VLC", BelowPrice: 100, Currency: "EUR"})
		aDone <- err
	}()
	waitFor(t, aParked, "writer A to park mid-transaction")

	bDone := make(chan error, 1)
	bStarted := make(chan struct{})
	go func() {
		close(bStarted)
		_, err := b.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "EUR"})
		bDone <- err
	}()
	waitFor(t, bStarted, "writer B to start")
	// Give B time to get all the way into (and, when correct, blocked inside) its
	// own transaction while A is still parked. Without the lock this is enough
	// for B to complete and be overwritten.
	time.Sleep(100 * time.Millisecond)
	close(releaseA)

	if err := <-aDone; err != nil {
		t.Fatalf("writer A add: %v", err)
	}
	if err := <-bDone; err != nil {
		t.Fatalf("writer B add: %v", err)
	}

	verify := NewStore(dir)
	if err := verify.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	got := verify.List()
	if len(got) != 2 {
		t.Fatalf("committed watches = %d, want 2 (a concurrent add was lost): %+v", len(got), got)
	}
	seen := map[string]bool{}
	for _, w := range got {
		seen[w.Origin] = true
	}
	if !seen["AMS"] || !seen["HEL"] {
		t.Fatalf("both writers' watches must survive, got origins %v", seen)
	}
}

// TRVL.STORE.TXN.2 / TRVL.STORE.TXN.4 — a scheduler round that is already in
// flight must not revert a tool call that edits the same watch while the round
// is out at the provider.
//
// Entry is through CheckAll, the function the scheduler and the MCP check tool
// actually call, not through an internal helper. The round takes its detached
// copy of the watch, then parks inside the provider call; the "tool call" then
// raises BelowPrice through a second Store; then the round is released and
// persists. The round's own fields (LastPrice, LastCheck) must land AND the tool
// call's field must survive.
//
// TXN.4 rides along: the tool call completes while the round is blocked in the
// provider, which is only possible because no store lock is held across the
// network call. If a future change moved the lock outside the provider call this
// test deadlocks and fails on the timeout rather than passing quietly.
func TestCheckRoundDoesNotRevertConcurrentToolCall(t *testing.T) {
	dir := t.TempDir()

	seed := NewStore(dir)
	id, err := seed.Add(Watch{Type: "flight", Origin: "AMS", Destination: "VLC", BelowPrice: 500, Currency: "EUR", LastPrice: 480})
	if err != nil {
		t.Fatalf("seed add: %v", err)
	}

	round := NewStore(dir)
	if err := round.Load(); err != nil {
		t.Fatalf("load round store: %v", err)
	}

	checker := &parkingChecker{
		entered: make(chan struct{}),
		release: make(chan struct{}),
		price:   410,
	}

	roundDone := make(chan []CheckResult, 1)
	go func() { roundDone <- CheckAll(context.Background(), round, checker) }()

	// The round now holds a detached copy of the watch with BelowPrice 500.
	waitFor(t, checker.entered, "check round to reach the provider call")

	tool := NewStore(dir)
	if _, err := tool.Mutate(id, func(cur *Watch) { cur.BelowPrice = 999 }); err != nil {
		t.Fatalf("concurrent tool call: %v", err)
	}

	close(checker.release)
	results := <-roundDone
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if results[0].Error != nil {
		t.Fatalf("check error: %v", results[0].Error)
	}

	verify := NewStore(dir)
	if err := verify.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	got, ok := verify.Get(id)
	if !ok {
		t.Fatalf("watch %s vanished", id)
	}
	if got.BelowPrice != 999 {
		t.Fatalf("BelowPrice = %v, want 999: the check round reverted the concurrent tool call", got.BelowPrice)
	}
	if got.LastPrice != 410 {
		t.Fatalf("LastPrice = %v, want 410: the check round failed to persist its own result", got.LastPrice)
	}
	if got.LastCheck.IsZero() {
		t.Fatal("LastCheck not recorded by the check round")
	}
}

// TRVL.STORE.TXN.2 — the same hazard on the watch's own alert state, where the
// concurrent writer is a second check round rather than a tool call.
//
// BaselinePrice and LastAlertedPrice are running state, not values the round
// computes from scratch: pricealert.Evaluate reads them back in to dedup. A
// round that derives them from its pre-provider copy therefore reverts whatever
// a round that finished in the meantime recorded, re-arming the dedup window and
// alerting a second time for a single drop.
//
// Round A parks in the provider at 460 (an 8% dip, under the 10% threshold).
// Round B then completes at 400 and records the alert. When A resumes it must
// leave B's dedup marker alone.
func TestCheckRoundDoesNotRevertConcurrentAlertState(t *testing.T) {
	dir := t.TempDir()

	seed := NewStore(dir)
	id, err := seed.Add(Watch{
		Type: "flight", Origin: "AMS", Destination: "VLC", Currency: "EUR",
		BaselinePrice: 500, AlertDropPct: 10,
	})
	if err != nil {
		t.Fatalf("seed add: %v", err)
	}

	roundA := NewStore(dir)
	if err := roundA.Load(); err != nil {
		t.Fatalf("load round A: %v", err)
	}
	parked := &parkingChecker{
		entered: make(chan struct{}),
		release: make(chan struct{}),
		price:   460,
	}
	aDone := make(chan []CheckResult, 1)
	go func() { aDone <- CheckAll(context.Background(), roundA, parked) }()
	waitFor(t, parked.entered, "round A to reach the provider call")

	roundB := NewStore(dir)
	if err := roundB.Load(); err != nil {
		t.Fatalf("load round B: %v", err)
	}
	bResults := CheckAll(context.Background(), roundB, fixedPriceChecker{price: 400})
	if len(bResults) != 1 || bResults[0].Error != nil {
		t.Fatalf("round B: %+v", bResults)
	}
	if !bResults[0].PriceDropAlert {
		t.Fatal("round B did not alert on a 20% drop; the fixture is wrong")
	}

	close(parked.release)
	aResults := <-aDone
	if len(aResults) != 1 || aResults[0].Error != nil {
		t.Fatalf("round A: %+v", aResults)
	}
	if aResults[0].PriceDropAlert {
		t.Fatal("round A alerted on an 8% dip below a 10% threshold")
	}

	verify := NewStore(dir)
	if err := verify.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	got, ok := verify.Get(id)
	if !ok {
		t.Fatalf("watch %s vanished", id)
	}
	if got.LastAlertedPrice != 400 {
		t.Fatalf("LastAlertedPrice = %v, want 400: round A reverted the dedup state "+
			"recorded by a concurrent round, so the next check alerts twice for one drop",
			got.LastAlertedPrice)
	}
	if got.BaselinePrice != 500 {
		t.Fatalf("BaselinePrice = %v, want 500", got.BaselinePrice)
	}
}

// fixedPriceChecker always reports the same price.
type fixedPriceChecker struct{ price float64 }

func (c fixedPriceChecker) CheckPrice(_ context.Context, _ Watch) (float64, string, string, error) {
	return c.price, "EUR", "", nil
}

// TRVL.STORE.TXN.5 — a writer that dies holding the lock must not wedge the
// store. flock is owned by the open file description, so the kernel drops it
// when the descriptor is closed, including on abnormal termination. Closing the
// descriptor without unlocking is exactly what process death does to it, so this
// exercises the property we depend on without spawning a subprocess.
func TestAbandonedLockDoesNotWedgeStore(t *testing.T) {
	if !lockSupported {
		t.Skip("no advisory-lock implementation on this platform")
	}
	dir := t.TempDir()
	s := NewStore(dir)
	if err := s.ensureDir(); err != nil {
		t.Fatalf("ensure dir: %v", err)
	}

	held, err := acquireFileLock(s.lockPath())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if _, statErr := os.Stat(s.lockPath()); statErr != nil {
		t.Fatalf("lock file missing: %v", statErr)
	}
	// Simulate death: drop the descriptor, never unlock.
	if err := held.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := s.Add(Watch{Type: "flight", Origin: "AMS", Destination: "VLC", Currency: "EUR"})
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("add after abandoned lock: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("store wedged: a dead holder's lock was never released")
	}
}
