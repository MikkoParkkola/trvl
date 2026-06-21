// Package probebudget implements the two-lane provider-call budget that makes
// counterfactual fan-out (MIK-6234 Tier 1/2) safe: counterfactual probes draw
// from a separate, slow-refilling, best-effort bucket that is strictly lower
// priority than interactive traffic. A counterfactual probe can NEVER consume
// the budget a user's live query needs.
//
// The invariant this package guarantees: interactive acquisition is unbounded
// and unaffected by the probe lane; probe acquisition is bounded by its own
// bucket and, when exhausted, fails fast (TryAcquire returns false) rather than
// borrowing from or blocking interactive work. Budget exhausted means "serve
// cache / say not-yet-computed", never a silent fresh fan-out.
package probebudget

import (
	"sync"
	"time"
)

// Clock returns the current time; injectable for deterministic tests.
type Clock func() time.Time

// Budget is a two-lane call budget. The probe lane is a token bucket; the
// interactive lane is intentionally unmetered so it is never throttled by probe
// activity.
type Budget struct {
	mu           sync.Mutex
	tokens       float64
	capacity     float64
	refillPerSec float64
	last         time.Time
	now          Clock
}

// New creates a probe budget with the given bucket capacity and refill rate
// (tokens per second). It starts full. A nil clock uses time.Now.
func New(capacity, refillPerSec float64, clock Clock) *Budget {
	if clock == nil {
		clock = time.Now
	}
	if capacity < 0 {
		capacity = 0
	}
	if refillPerSec < 0 {
		refillPerSec = 0
	}
	return &Budget{
		tokens:       capacity,
		capacity:     capacity,
		refillPerSec: refillPerSec,
		last:         clock(),
		now:          clock,
	}
}

// TryAcquireProbe attempts to take one token from the probe lane. It returns
// true if a token was available (probe may proceed), false if the lane is
// exhausted (caller MUST fall back to cache, never a fresh fan-out). It never
// blocks.
func (b *Budget) TryAcquireProbe() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refill()
	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

// AcquireInteractive accounts for an interactive (user-facing) call. It always
// succeeds and is never throttled by the probe lane — that is the safety
// invariant. It exists so callers route every provider call through one place
// and so the two lanes are explicit at call sites.
func (b *Budget) AcquireInteractive() bool {
	return true
}

// ProbeTokens reports the currently available probe tokens (after refill).
func (b *Budget) ProbeTokens() float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refill()
	return b.tokens
}

func (b *Budget) refill() {
	now := b.now()
	elapsed := now.Sub(b.last).Seconds()
	if elapsed <= 0 {
		return
	}
	b.last = now
	b.tokens += elapsed * b.refillPerSec
	if b.tokens > b.capacity {
		b.tokens = b.capacity
	}
}
