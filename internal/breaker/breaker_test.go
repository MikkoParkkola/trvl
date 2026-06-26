package breaker

import (
	"sync"
	"testing"
	"time"
)

// fixedClock lets a test drive the breaker's notion of "now".
type fixedClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fixedClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fixedClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newTestBreaker(threshold int, cooldown time.Duration) (*Breaker, *fixedClock) {
	clk := &fixedClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	b := NewWithConfig(threshold, cooldown)
	b.now = clk.now
	return b, clk
}

func TestAllowsUntilThreshold(t *testing.T) {
	b, _ := newTestBreaker(3, time.Minute)
	for i := 0; i < 2; i++ {
		b.RecordFailure("google")
		if !b.Allow("google") {
			t.Fatalf("breaker tripped after %d failures, want open only at threshold 3", i+1)
		}
	}
	b.RecordFailure("google") // 3rd → trips
	if b.Allow("google") {
		t.Fatal("breaker should be tripped after 3 consecutive failures within cooldown")
	}
}

func TestCooldownAllowsHalfOpenProbe(t *testing.T) {
	b, clk := newTestBreaker(2, time.Minute)
	b.RecordFailure("booking")
	b.RecordFailure("booking") // tripped
	if b.Allow("booking") {
		t.Fatal("breaker should be open immediately after tripping")
	}
	clk.advance(59 * time.Second)
	if b.Allow("booking") {
		t.Fatal("breaker should stay open before cooldown elapses")
	}
	clk.advance(time.Second) // now exactly at cooldown
	if !b.Allow("booking") {
		t.Fatal("breaker should allow a half-open probe once cooldown elapses")
	}
}

func TestSuccessClosesBreaker(t *testing.T) {
	b, clk := newTestBreaker(2, time.Minute)
	b.RecordFailure("agoda")
	b.RecordFailure("agoda") // tripped
	clk.advance(time.Minute) // half-open
	b.RecordSuccess("agoda") // probe succeeds → closed
	if !b.Allow("agoda") {
		t.Fatal("breaker should be closed after a successful probe")
	}
	// A fresh failure must not instantly re-trip a closed breaker.
	b.RecordFailure("agoda")
	if !b.Allow("agoda") {
		t.Fatal("single failure after recovery should not trip a threshold-2 breaker")
	}
}

func TestFailedProbeReArmsCooldown(t *testing.T) {
	b, clk := newTestBreaker(2, time.Minute)
	b.RecordFailure("trivago")
	b.RecordFailure("trivago") // tripped
	clk.advance(time.Minute)   // half-open
	if !b.Allow("trivago") {
		t.Fatal("expected half-open probe")
	}
	b.RecordFailure("trivago") // probe fails → re-arm cooldown
	if b.Allow("trivago") {
		t.Fatal("failed probe should re-arm the cooldown and re-open the breaker")
	}
	clk.advance(time.Minute)
	if !b.Allow("trivago") {
		t.Fatal("breaker should allow another probe after the re-armed cooldown elapses")
	}
}

func TestKeysAreIndependent(t *testing.T) {
	b, _ := newTestBreaker(1, time.Minute)
	b.RecordFailure("google") // trips google only
	if b.Allow("google") {
		t.Fatal("google should be tripped")
	}
	if !b.Allow("booking") {
		t.Fatal("booking must be unaffected by google's failures")
	}
}

func TestConcurrentAccessIsRaceFree(t *testing.T) {
	b := New()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.RecordFailure("p")
			_ = b.Allow("p")
			b.RecordSuccess("p")
		}()
	}
	wg.Wait()
}
