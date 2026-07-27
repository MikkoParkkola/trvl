package hacks

import (
	"context"
	"runtime"
	"slices"
	"testing"
	"time"
)

// withDetectors swaps the detector roster for the duration of a test so
// DetectAll's own control flow can be tested without touching live providers.
func withDetectors(t *testing.T, fns ...detectFn) {
	t.Helper()
	prev := currentDetectorRoster()
	setDetectorRoster(func() []detectFn { return fns })
	t.Cleanup(func() { setDetectorRoster(prev) })
}

// TestDetectAll_ReturnsAtDeadlineWithPartialFlag covers the whole contract
// deterministically: results that arrived are kept, the caller is not made to
// wait for the ones that did not, and it is told the sweep was cut short.
//
// Reporting a truncated list as complete is the failure this guards. An agent
// reading "count: 3" with no partial marker presents three hacks as the answer
// when there were more.
func TestDetectAll_ReturnsAtDeadlineWithPartialFlag(t *testing.T) {
	fast := func(context.Context, DetectorInput) []Hack {
		return []Hack{{Type: "fast", Savings: 10, Currency: "EUR"}}
	}
	slow := func(ctx context.Context, _ DetectorInput) []Hack {
		select {
		case <-time.After(30 * time.Second):
		case <-ctx.Done():
		}
		return []Hack{{Type: "slow", Savings: 20, Currency: "EUR"}}
	}
	withDetectors(t, fast, slow)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	start := time.Now()
	found, complete := DetectAll(ctx, DetectorInput{Origin: "HEL", Destination: "BCN", Date: "2026-09-01"})
	elapsed := time.Since(start)

	if elapsed > 3*time.Second {
		t.Fatalf("DetectAll took %v with a 150ms deadline; it must not wait on detectors that stopped mattering", elapsed)
	}
	if complete {
		t.Fatal("complete was true after the deadline cut the sweep short; a caller cannot then tell a partial list from the full answer")
	}
	if len(found) == 0 {
		t.Fatal("expected the detector that finished in time to be kept; partial results beat an empty answer")
	}
}

// TestDetectAll_CompleteWhenEveryDetectorFinishes is the other half: an
// uninterrupted sweep must report complete, or callers would warn about
// truncation on every ordinary search.
func TestDetectAll_CompleteWhenEveryDetectorFinishes(t *testing.T) {
	one := func(context.Context, DetectorInput) []Hack {
		return []Hack{{Type: "one", Savings: 10, Currency: "EUR"}}
	}
	withDetectors(t, one, one)

	found, complete := DetectAll(context.Background(), DetectorInput{Origin: "HEL", Destination: "BCN", Date: "2026-09-01"})

	if !complete {
		t.Fatal("complete was false although every detector returned")
	}
	if len(found) == 0 {
		t.Fatal("expected results from a completed sweep")
	}
}

// TestDetectAll_AbandonedDetectorsDoNotBlockForever pins the invariant the early
// return depends on: the results channel is buffered to the detector count, so a
// straggler completes its send and exits rather than parking on a channel nobody
// reads. Shrink that buffer and every abandoned detector becomes a permanently
// parked goroutine — in a long-lived MCP server, one per timed-out search.
//
// The observation has to be the goroutine count, not the detector's own return.
// A detector finishing is not the thing at risk; what parks is the goroutine
// wrapping it, on the send after the detector has already returned. An earlier
// version of this test signalled from inside the detector and therefore passed
// even with the buffer shrunk to 1.
//
// The injected roster keeps it deterministic and quick: no live providers, and
// the detectors return in well under a second.
func TestDetectAll_AbandonedDetectorsDoNotBlockForever(t *testing.T) {
	const n = 12
	fns := make([]detectFn, 0, n)
	for range n {
		fns = append(fns, func(context.Context, DetectorInput) []Hack {
			time.Sleep(300 * time.Millisecond) // outlive the deadline below
			return []Hack{{Type: "late", Savings: 5, Currency: "EUR"}}
		})
	}
	withDetectors(t, fns...)

	runtime.GC()
	baseline := runtime.NumGoroutine()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, complete := DetectAll(ctx, DetectorInput{Origin: "HEL", Destination: "BCN", Date: "2026-09-01"})
	if complete {
		t.Fatal("expected the sweep to be reported partial")
	}

	// The abandoned goroutines cannot be interrupted, so wait for them to drain.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		runtime.GC()
		if runtime.NumGoroutine() <= baseline+2 {
			return // they completed their sends and exited
		}
		time.Sleep(100 * time.Millisecond)
	}

	t.Fatalf("goroutines did not drain after an abandoned sweep: baseline %d, still %d. "+
		"Each is parked on a send nobody will receive, so every timed-out search leaks one per detector",
		baseline, runtime.NumGoroutine())
}

// TestDetectAll_BoundedWithoutACallerDeadline is the availability guard.
//
// The per-detector timeout only reaches the detector; it never stopped the
// collector waiting. A caller that sets no deadline of its own — an MCP request
// that supplies none — plus one detector that ignores cancellation left DetectAll
// blocked forever. Cancellation being cooperative means such a detector cannot be
// stopped; it does not mean the response has to wait for it.
//
// This also exercises the completeness accounting on a path with no cancellation
// at all, which the previous version of this test could not: context expiry alone
// forced complete=false through the ctx.Done() case, so the test passed even with
// the delivered-result accounting removed.
func TestDetectAll_BoundedWithoutACallerDeadline(t *testing.T) {
	prev := currentSweepTimeout()
	setSweepTimeout(300 * time.Millisecond)
	t.Cleanup(func() { setSweepTimeout(prev) })

	quick := func(context.Context, DetectorInput) []Hack {
		return []Hack{{Type: "quick", Savings: 10, Currency: "EUR"}}
	}
	// Ignores its context entirely, exactly like a detector stuck in
	// non-context-aware work.
	stuck := func(context.Context, DetectorInput) []Hack {
		time.Sleep(30 * time.Second)
		return nil
	}
	withDetectors(t, quick, stuck)

	start := time.Now()
	found, complete := DetectAll(context.Background(), DetectorInput{Origin: "HEL", Destination: "BCN", Date: "2026-09-01"})
	elapsed := time.Since(start)

	if elapsed > 5*time.Second {
		t.Fatalf("DetectAll blocked for %v with no caller deadline; an uncooperative detector must not hold the response open", elapsed)
	}
	if complete {
		t.Fatal("reported complete although one detector never delivered; completeness must count results, not dispatches")
	}
	if len(found) == 0 {
		t.Fatal("expected the detector that did finish to be kept")
	}
}

// TestDetectAll_DetectorTimeoutCountsAsIncomplete closes the gap where a detector
// cut off by its own deadline still made the sweep look finished.
//
// A timed-out detector returns whatever it had and sends it, so counting
// deliveries reported complete=true while one detector had in fact been cut off
// mid-work. The CLI and MCP then suppressed their partial warnings, and a caller
// with no deadline of its own — the common case — was told a truncated sweep was
// the whole answer.
func TestDetectAll_DetectorTimeoutCountsAsIncomplete(t *testing.T) {
	prevDet := currentDetectorTimeout()
	setDetectorTimeout(100 * time.Millisecond)
	t.Cleanup(func() { setDetectorTimeout(prevDet) })

	quick := func(context.Context, DetectorInput) []Hack {
		return []Hack{{Type: "quick", Savings: 10, Currency: "EUR"}}
	}
	// Cooperative, but slower than its own allowance: it observes the deadline
	// and returns, which is exactly the case that used to look complete.
	slow := func(ctx context.Context, _ DetectorInput) []Hack {
		<-ctx.Done()
		return nil
	}
	withDetectors(t, quick, slow)

	// No caller deadline at all: the only thing cutting anything short is the
	// per-detector allowance.
	found, complete := DetectAll(context.Background(), DetectorInput{Origin: "HEL", Destination: "BCN", Date: "2026-09-01"})

	if complete {
		t.Fatal("reported complete although a detector was cut off by its own deadline; delivery is not the same as finishing")
	}
	if len(found) == 0 {
		t.Fatal("expected the detector that finished in time to be kept")
	}
}

// TestDetectAll_CompleteWhenNothingIsCutShort keeps the flag from becoming noise:
// a sweep where every detector finishes inside its allowance must report complete.
func TestDetectAll_CompleteWhenNothingIsCutShort(t *testing.T) {
	quick := func(context.Context, DetectorInput) []Hack {
		return []Hack{{Type: "quick", Savings: 10, Currency: "EUR"}}
	}
	withDetectors(t, quick, quick, quick)

	_, complete := DetectAll(context.Background(), DetectorInput{Origin: "HEL", Destination: "BCN", Date: "2026-09-01"})

	if !complete {
		t.Fatal("a sweep where every detector finished must report complete, or the warning appears on every ordinary search")
	}
}

// TestDrainBuffered_KeepsWhatWasAlreadyDeliveredAndCountsIt covers a defect the
// adversarial review found. Both of DetectAll's bail-out paths used to return
// without looking at the channel, and a select with more than one ready case
// picks at random, so a result delivered at the same moment as the deadline was
// thrown away. That makes the answer wrong rather than merely incomplete, and
// separating those two is the entire purpose of the partial flag.
//
// This tests the mechanism directly rather than trying to lose a race on
// purpose. Two earlier attempts drove the whole sweep and asserted on what came
// back; both passed against a mutant with the drain removed, because the
// collector consumed everything long before cancellation landed. A test that
// cannot fail is worse than no test, so the function was hoisted out to be
// called on a channel whose contents are known.
func TestDrainBuffered_KeepsWhatWasAlreadyDeliveredAndCountsIt(t *testing.T) {
	ch := make(chan detectorResult, 8)
	ch <- detectorResult{hacks: []Hack{{Type: "a"}, {Type: "b"}}}
	ch <- detectorResult{hacks: []Hack{{Type: "c"}}, cutShort: true}
	ch <- detectorResult{hacks: []Hack{{Type: "d"}}}

	received := 5 // a non-zero start proves it adds rather than assigns
	anyCutShort := false

	got := drainBuffered(ch, &received, &anyCutShort)

	var types []string
	for _, h := range got {
		types = append(types, h.Type)
	}
	if want := []string{"a", "b", "c", "d"}; !slices.Equal(types, want) {
		t.Errorf("drained hacks = %v, want %v", types, want)
	}
	if received != 8 {
		t.Errorf("received = %d, want 8: every drained result must count, or the sweep can call itself complete on results it never accounted for", received)
	}
	if !anyCutShort {
		t.Error("a drained result carrying cutShort must mark the sweep cut short, otherwise a truncated detector's output is reported as a full one")
	}
}

// TestDrainBuffered_DoesNotWaitForWhatHasNotArrived is the other half of the
// contract. Draining must never block: anything not yet buffered is what makes
// the sweep partial, and waiting for it is what the caller's deadline forbids.
// Without the default case this test hangs rather than fails, so it carries its
// own timeout.
func TestDrainBuffered_DoesNotWaitForWhatHasNotArrived(t *testing.T) {
	ch := make(chan detectorResult, 4) // open, one buffered, three slots free
	ch <- detectorResult{hacks: []Hack{{Type: "arrived"}}}

	received := 0
	anyCutShort := false

	done := make(chan []Hack, 1)
	go func() { done <- drainBuffered(ch, &received, &anyCutShort) }()

	select {
	case got := <-done:
		if len(got) != 1 || got[0].Type != "arrived" {
			t.Fatalf("drained %v, want exactly the one buffered hack", got)
		}
		if received != 1 {
			t.Errorf("received = %d, want 1", received)
		}
		if anyCutShort {
			t.Error("nothing was cut short")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("drainBuffered blocked waiting on an open channel with nothing buffered")
	}
}

// TestCollectSweep_BailOutKeepsAlreadyDeliveredResults covers the two bail-out
// call sites, which is the coverage the earlier attempts at this failed to get.
//
// Both cases are made ready at the same instant on purpose: ch is pre-filled, and
// done is already closed. Go picks at random between them, so without the drain a
// run that takes the bail-out first loses every buffered result. Repetition turns
// that coin flip into a certainty. With the drain the outcome does not depend on
// which case wins, which is the property being asserted.
func TestCollectSweep_BailOutKeepsAlreadyDeliveredResults(t *testing.T) {
	const iterations = 200

	for _, tc := range []struct {
		name string
		bail func() (<-chan struct{}, <-chan time.Time)
	}{
		{
			name: "caller deadline",
			bail: func() (<-chan struct{}, <-chan time.Time) {
				done := make(chan struct{})
				close(done)
				return done, nil // a nil timer channel never fires
			},
		},
		{
			name: "sweep bound",
			bail: func() (<-chan struct{}, <-chan time.Time) {
				fired := make(chan time.Time, 1)
				fired <- time.Time{}
				return nil, fired // a nil done channel never fires
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for i := 0; i < iterations; i++ {
				ch := make(chan detectorResult, 4)
				ch <- detectorResult{hacks: []Hack{{Type: "first", Savings: 10, Currency: "EUR"}}}
				ch <- detectorResult{hacks: []Hack{{Type: "second", Savings: 20, Currency: "EUR"}}}
				done, sweep := tc.bail()

				got, complete := collectSweep(ch, done, sweep, 4, 4)

				if complete {
					t.Fatalf("iteration %d: a sweep that bailed out reported complete", i)
				}
				seen := map[string]bool{}
				for _, h := range got {
					seen[h.Type] = true
				}
				for _, want := range []string{"first", "second"} {
					if !seen[want] {
						t.Fatalf("iteration %d: %q was already delivered when the bail-out fired and was discarded (got %d hacks)", i, want, len(got))
					}
				}
			}
		})
	}
}

// TestCollectSweep_CompleteOnlyWhenEveryDetectorDelivered guards the accounting
// the bail-out paths hand back, so the drain cannot quietly turn a truncated
// sweep into a finished one.
func TestCollectSweep_CompleteOnlyWhenEveryDetectorDelivered(t *testing.T) {
	newFull := func(cutShort bool) <-chan detectorResult {
		ch := make(chan detectorResult, 2)
		ch <- detectorResult{hacks: []Hack{{Type: "a"}}}
		ch <- detectorResult{hacks: []Hack{{Type: "b"}}, cutShort: cutShort}
		close(ch)
		return ch
	}

	if _, complete := collectSweep(newFull(false), nil, nil, 2, 2); !complete {
		t.Error("every detector was dispatched and delivered, so the sweep is complete")
	}
	if _, complete := collectSweep(newFull(true), nil, nil, 2, 2); complete {
		t.Error("a detector cut off by its own allowance means the sweep is not complete")
	}
	if _, complete := collectSweep(newFull(false), nil, nil, 2, 3); complete {
		t.Error("a detector that was never dispatched means the sweep is not complete")
	}
	if _, complete := collectSweep(newFull(false), nil, nil, 3, 3); complete {
		t.Error("a dispatched detector that never delivered means the sweep is not complete")
	}
}
