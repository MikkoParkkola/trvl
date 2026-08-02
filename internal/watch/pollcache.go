package watch

import (
	"context"
	"sync"
)

// roundCache is a PriceChecker decorator that collapses duplicate provider
// calls within a single check round.
//
// Several watches may legitimately share one polled target — the point of
// #509 is that two price thresholds on AMS→VLC are two intents but one
// search. Without this, N duplicate watches meant N provider round trips per
// round, so the store's growth multiplied outbound traffic (MULTIPRICE.2).
//
// The cache lives for exactly one round: it is created inside the fan-out and
// dropped when the round returns, so it never serves a stale price to a later
// round. Deduplication is single-flight, not just memoisation — concurrent
// callers sharing a pollKey wait for the in-flight call instead of racing to
// issue their own, which matters for the parallel CheckAllBounded path.
type roundCache struct {
	inner PriceChecker

	mu      sync.Mutex
	entries map[string]*roundEntry
}

type roundEntry struct {
	done chan struct{} // closed once the fields below are final

	price        float64
	currency     string
	cheapestDate string
	err          error
}

func newRoundCache(inner PriceChecker) *roundCache {
	return &roundCache{inner: inner, entries: make(map[string]*roundEntry)}
}

// calls reports how many distinct provider calls this round has issued. Used
// by tests to assert the observation count directly rather than inferring it.
func (c *roundCache) calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

func (c *roundCache) CheckPrice(ctx context.Context, w Watch) (float64, string, string, error) {
	key := w.pollKey()

	c.mu.Lock()
	if e, ok := c.entries[key]; ok {
		c.mu.Unlock()
		// A follower must not outlive its own deadline waiting on the leader.
		select {
		case <-e.done:
			return e.price, e.currency, e.cheapestDate, e.err
		case <-ctx.Done():
			return 0, w.Currency, "", ctx.Err()
		}
	}
	e := &roundEntry{done: make(chan struct{})}
	c.entries[key] = e
	c.mu.Unlock()

	e.price, e.currency, e.cheapestDate, e.err = c.inner.CheckPrice(ctx, w)
	close(e.done)
	return e.price, e.currency, e.cheapestDate, e.err
}
