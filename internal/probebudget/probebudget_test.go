package probebudget

import (
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// TRVL.CF.2: the load-bearing safety test. A SATURATED probe lane must never
// delay or starve interactive acquisition.
func TestInteractiveNeverStarvedBySaturatedProbeLane(t *testing.T) {
	clk := &fakeClock{t: time.Unix(0, 0)}
	b := New(2, 0.0, clk.now) // capacity 2, no refill

	// Drain the probe lane completely.
	if !b.TryAcquireProbe() {
		t.Fatalf("first probe should succeed")
	}
	if !b.TryAcquireProbe() {
		t.Fatalf("second probe should succeed")
	}
	if b.TryAcquireProbe() {
		t.Fatalf("third probe must fail (lane exhausted)")
	}

	// Interactive must still succeed every time, lane fully drained.
	for i := 0; i < 1000; i++ {
		if !b.AcquireInteractive() {
			t.Fatalf("interactive acquisition #%d was denied — invariant violated", i)
		}
	}
}

func TestProbeRefill(t *testing.T) {
	clk := &fakeClock{t: time.Unix(0, 0)}
	b := New(2, 1.0, clk.now) // 1 token/sec, cap 2

	b.TryAcquireProbe()
	b.TryAcquireProbe()
	if b.TryAcquireProbe() {
		t.Fatalf("lane should be empty")
	}
	clk.advance(1500 * time.Millisecond) // +1.5 tokens
	if !b.TryAcquireProbe() {
		t.Fatalf("after refill a probe should be available")
	}
	// Capacity cap holds.
	clk.advance(10 * time.Second)
	if got := b.ProbeTokens(); got > 2 {
		t.Fatalf("tokens must not exceed capacity, got %v", got)
	}
}

func TestExhaustedMeansFallback(t *testing.T) {
	b := New(0, 0, nil) // no probe budget at all
	if b.TryAcquireProbe() {
		t.Fatalf("zero-capacity lane must always fail (forces cache fallback)")
	}
	if !b.AcquireInteractive() {
		t.Fatalf("interactive must still succeed with zero probe budget")
	}
}
