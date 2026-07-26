package hacks

import (
	"context"
	"runtime"
	"testing"
	"time"
)

// TestDetectAll_DeadlineDoesNotLeakGoroutines pins the half of the deadline fix
// that is otherwise only a claim in a comment.
//
// Returning early on ctx.Done() leaves detector goroutines still running. That is
// only safe because the results channel is buffered to the detector count, so a
// straggler completes its send and exits rather than blocking forever on a
// channel nobody is reading. Shrink that buffer and every abandoned detector
// becomes a permanently parked goroutine — in a long-lived MCP server, one per
// timed-out search.
//
// The assertion is deliberately loose on count and strict on direction: what
// matters is that the number settles back toward the baseline rather than
// climbing by the size of the detector roster.
func TestDetectAll_DeadlineDoesNotLeakGoroutines(t *testing.T) {
	in := DetectorInput{Origin: "HEL", Destination: "BCN", Date: "2026-09-01"}

	// Let anything from earlier tests wind down before taking the baseline.
	time.Sleep(200 * time.Millisecond)
	runtime.GC()
	baseline := runtime.NumGoroutine()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	_ = DetectAll(ctx, in)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("DetectAll took %v with a 20ms deadline; it must not wait on detectors that have stopped mattering", elapsed)
	}

	// Give the abandoned detectors time to finish and exit. They cannot be
	// interrupted, so this waits for them rather than asserting immediately.
	deadline := time.Now().Add(90 * time.Second)
	var final int
	for time.Now().Before(deadline) {
		runtime.GC()
		final = runtime.NumGoroutine()
		if final <= baseline+5 {
			return // settled
		}
		time.Sleep(500 * time.Millisecond)
	}

	t.Fatalf("goroutines did not settle after an abandoned DetectAll: baseline %d, still %d. "+
		"Abandoned detectors must be able to complete their send and exit; a channel too small to hold "+
		"every result would park them forever", baseline, final)
}
