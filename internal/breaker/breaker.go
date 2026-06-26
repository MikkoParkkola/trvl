// Package breaker provides an in-memory circuit breaker for the hand-rolled
// scraper fan-outs (hotels, flights, ground). Those fan-outs bypass the
// registry-backed breaker in internal/providers/runtime_search.go, so a
// persistently-failing scraper (dead cookie, blocked endpoint, hard 429) gets
// retried on every search with zero back-off. This primitive gives them the
// same protection without the YAML provider registry's persistence layer, which
// the in-process scrapers don't have.
//
// Semantics mirror internal/providers exactly: trip after N consecutive
// failures, stay open for a fixed cooldown measured from the last failure, then
// allow a single half-open probe. A successful probe closes the breaker; a
// failed probe re-arms the cooldown.
package breaker

import (
	"sync"
	"time"
)

// Defaults mirror internal/providers/runtime_core.go circuitBreakerThreshold
// and circuitBreakerCooldown so scraper and registry breakers behave alike.
const (
	DefaultThreshold = 5
	DefaultCooldown  = 5 * time.Minute
)

type state struct {
	errorCount  int
	lastErrorAt time.Time
}

// Breaker is a per-key in-memory circuit breaker, safe for concurrent use by
// the goroutine fan-outs. Key is the provider name (e.g. "google", "booking").
type Breaker struct {
	threshold int
	cooldown  time.Duration
	now       func() time.Time // overridable in tests

	mu     sync.Mutex
	states map[string]*state
}

// New returns a breaker with the default threshold and cooldown.
func New() *Breaker { return NewWithConfig(DefaultThreshold, DefaultCooldown) }

// NewWithConfig returns a breaker with a custom threshold and cooldown.
// Non-positive values fall back to the defaults.
func NewWithConfig(threshold int, cooldown time.Duration) *Breaker {
	if threshold <= 0 {
		threshold = DefaultThreshold
	}
	if cooldown <= 0 {
		cooldown = DefaultCooldown
	}
	return &Breaker{
		threshold: threshold,
		cooldown:  cooldown,
		now:       time.Now,
		states:    make(map[string]*state),
	}
}

// Allow reports whether a call for key may proceed. It returns false only while
// the breaker is tripped (>= threshold consecutive failures) and still inside
// the cooldown window. Once cooldown elapses it allows a single half-open probe
// through; the next RecordFailure re-arms the cooldown and RecordSuccess closes
// the breaker.
func (b *Breaker) Allow(key string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.states[key]
	if s == nil || s.errorCount < b.threshold {
		return true
	}
	return b.now().Sub(s.lastErrorAt) >= b.cooldown
}

// Tripped reports whether key is currently open (tripped and within cooldown).
// It is the inverse of Allow.
func (b *Breaker) Tripped(key string) bool { return !b.Allow(key) }

// RecordSuccess closes the breaker for key, clearing its failure count.
func (b *Breaker) RecordSuccess(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if s := b.states[key]; s != nil {
		s.errorCount = 0
		s.lastErrorAt = time.Time{}
	}
}

// RecordFailure registers a consecutive failure for key, tripping the breaker
// once the count reaches the threshold and re-arming the cooldown window.
func (b *Breaker) RecordFailure(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.states[key]
	if s == nil {
		s = &state{}
		b.states[key] = s
	}
	s.errorCount++
	s.lastErrorAt = b.now()
}
