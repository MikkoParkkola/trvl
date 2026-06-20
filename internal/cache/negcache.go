package cache

import (
	"strings"
	"sync"
	"time"
)

// defaultNegMaxEntries bounds the negative cache so a long-running process that
// probes many routes cannot grow it without limit.
const defaultNegMaxEntries = 4000

// NegativeCache records (provider, origin, destination, date-class) tuples that
// a provider has *definitively* reported as having no service, so the same
// provider is not re-queried for the same route within a TTL window.
//
// Conservative by construction: callers must only Mark a CLEAN no-result — a
// provider that ran successfully and returned zero results. Errors, timeouts,
// rate-limits, and filtered-empty results must NOT be marked: those should
// retry on the next search. A negative entry is a small, honest "we checked
// recently and there was genuinely nothing", not "the request failed".
//
// Safe for concurrent use. The clock is injectable for deterministic tests.
type NegativeCache struct {
	mu         sync.RWMutex
	entries    map[string]time.Time // key -> expiry
	ttl        time.Duration
	maxEntries int
	now        func() time.Time
}

// NewNegativeCache creates a negative cache with the given TTL and the default
// entry cap, using the wall clock.
func NewNegativeCache(ttl time.Duration) *NegativeCache {
	return NewNegativeCacheWithClock(ttl, time.Now)
}

// NewNegativeCacheWithClock is like NewNegativeCache but takes an explicit clock
// function, which lets tests advance time deterministically without sleeping.
// A nil clock falls back to time.Now.
func NewNegativeCacheWithClock(ttl time.Duration, now func() time.Time) *NegativeCache {
	if now == nil {
		now = time.Now
	}
	if ttl <= 0 {
		ttl = time.Minute
	}
	return &NegativeCache{
		entries:    make(map[string]time.Time),
		ttl:        ttl,
		maxEntries: defaultNegMaxEntries,
		now:        now,
	}
}

// DateClass buckets a YYYY-MM-DD date into a coarse year-month class. A route is
// served or not at roughly seasonal granularity, so bucketing by month keeps the
// negative cache compact while still respecting summer-only / winter-only routes
// (a no-service entry for 2026-07 never suppresses a 2026-08 query). Malformed
// or empty dates fall back to the raw string so callers never key on "".
func DateClass(date string) string {
	d := strings.TrimSpace(date)
	if len(d) >= 7 && d[4] == '-' {
		return d[:7] // "YYYY-MM"
	}
	return d
}

// NegativeKey builds the canonical negative-cache key from a provider id, an
// origin, a destination, and a date. The date is reduced to its DateClass.
func NegativeKey(provider, origin, destination, date string) string {
	return strings.ToLower(strings.TrimSpace(provider)) + "|" +
		strings.ToUpper(strings.TrimSpace(origin)) + "|" +
		strings.ToUpper(strings.TrimSpace(destination)) + "|" +
		DateClass(date)
}

// Seen reports whether key has a live (unexpired) negative entry. Expired
// entries are treated as absent and lazily removed.
func (n *NegativeCache) Seen(key string) bool {
	n.mu.RLock()
	expiry, ok := n.entries[key]
	n.mu.RUnlock()
	if !ok {
		return false
	}
	if !n.now().Before(expiry) {
		n.mu.Lock()
		// Re-check under the write lock: a concurrent Mark may have refreshed it.
		if exp, ok := n.entries[key]; ok && !n.now().Before(exp) {
			delete(n.entries, key)
		}
		n.mu.Unlock()
		return false
	}
	return true
}

// Mark records key as a negative result, valid for the cache TTL from now.
// When the cache is at capacity, one expired entry (or, failing that, an
// arbitrary entry) is evicted first so the map stays bounded.
func (n *NegativeCache) Mark(key string) {
	now := n.now()
	n.mu.Lock()
	if len(n.entries) >= n.maxEntries {
		n.evictLocked(now)
	}
	n.entries[key] = now.Add(n.ttl)
	n.mu.Unlock()
}

// evictLocked removes one entry to make room. It prefers an already-expired
// entry; if none are expired it drops an arbitrary one (map iteration order).
// Caller must hold n.mu.
func (n *NegativeCache) evictLocked(now time.Time) {
	var anyKey string
	for k, exp := range n.entries {
		anyKey = k
		if !now.Before(exp) {
			delete(n.entries, k)
			return
		}
	}
	if anyKey != "" {
		delete(n.entries, anyKey)
	}
}

// Len returns the number of entries (including expired ones not yet reaped).
func (n *NegativeCache) Len() int {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return len(n.entries)
}
