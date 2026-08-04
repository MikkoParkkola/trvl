package watch

import (
	"context"
	"sync"
)

// roundCache is a PriceChecker decorator that collapses duplicate provider
// calls within a single check round.
//
// Several checks may legitimately hit one polled target in a round: the same
// route re-checked from different entry points, or the parallel fan-out racing
// on one watch. Without this, each meant its own provider round trip, so the
// store's growth multiplied outbound traffic.
//
// (This once also covered several watches sharing a target, which #509's
// threshold-aware identity made possible. That identity was reversed on
// 2026-08-02 -- one watch per target now -- so single-flight is what remains
// load-bearing here, not the multi-watch collapse.)
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
