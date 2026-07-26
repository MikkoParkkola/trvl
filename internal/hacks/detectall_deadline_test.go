package hacks

import (
	"context"
	"runtime"
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
