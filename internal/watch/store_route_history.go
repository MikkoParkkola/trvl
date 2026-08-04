package watch

import (
	"math"
	"strings"
	"time"
)

// RecordObservation appends an ad-hoc (route-keyed) price observation and
// persists. This is the MIK-6229 enabler: every flight/hotel search can log its
// observed price so the history corpus compounds across all searched routes,
// not only watched ones. A non-positive price is ignored (never a real fare).
//
// Two scaling guards (added in the MIK-6229 improve pass) keep the corpus
// bounded and the per-search write cheap:
//   - Throttle: a near-identical observation for the same route+currency within
//     observationThrottle is skipped entirely (no write), so rapid repeat
//     searches of the same route do not each rewrite the history file.
//   - Cap: at most maxObservationsPerRoute points are retained per route key;
//     the oldest are pruned, bounding file growth to cap x number-of-routes.
//
// #553 review round 2: this used to take only s.mu and call saveLocked
// directly -- no acquireFileLock, no reload -- so it was the one production
// write path (every flight/hotel search, via pricefeed) still exhibiting the
// exact #553 cross-process clobber the rest of this file's withTxnLocked
// wiring was meant to close. Now runs inside withTxnLocked like every other
// mutator; the throttle check reads the freshly reloaded history so a
// near-duplicate decided elsewhere in the meantime is still caught, and
// skips the write via errTxnNoop instead of persisting a no-op.
func (s *Store) RecordObservation(routeKey string, price float64, currency string) error {
	if routeKey == "" || price <= 0 {
		return nil
	}
	cur := strings.ToUpper(strings.TrimSpace(currency))

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.withTxnLocked(func() error {
		if last, ok := s.lastObservationLocked(routeKey, cur); ok && last.Price > 0 {
			if time.Since(last.Timestamp) < observationThrottle &&
				math.Abs(price-last.Price)/last.Price <= observationEpsilonPct {
				return errTxnNoop // redundant near-duplicate; skip the write entirely
			}
		}

		s.history = append(s.history, PricePoint{
			RouteKey:  routeKey,
			Price:     price,
			Currency:  cur,
			Timestamp: time.Now(),
		})
		s.pruneRouteLocked(routeKey)
		s.pruneGlobalRouteLocked()
		return nil
	})
}

// pruneGlobalRouteLocked evicts the oldest ad-hoc route-keyed observations once
// their total exceeds maxRouteObservations, bounding the file regardless of how
// many distinct routes are searched. Watch-keyed points (WatchID set) are never
// touched. Caller holds s.mu.
func (s *Store) pruneGlobalRouteLocked() {
	var routeIdx []int
	for i, p := range s.history {
		if p.RouteKey != "" && p.WatchID == "" {
			routeIdx = append(routeIdx, i)
		}
	}
	if len(routeIdx) <= maxRouteObservations {
		return
	}
	drop := make(map[int]bool, len(routeIdx)-maxRouteObservations)
	for _, i := range routeIdx[:len(routeIdx)-maxRouteObservations] {
		drop[i] = true
	}
	kept := s.history[:0:0]
	for i, p := range s.history {
		if !drop[i] {
			kept = append(kept, p)
		}
	}
	s.history = kept
}

// lastObservationLocked returns the most recent price point for a route key,
// optionally filtered to a currency (empty currency matches any). Caller holds s.mu.
func (s *Store) lastObservationLocked(routeKey, currency string) (PricePoint, bool) {
	for i := len(s.history) - 1; i >= 0; i-- {
		p := s.history[i]
		if p.RouteKey != routeKey {
			continue
		}
		if currency != "" && strings.ToUpper(p.Currency) != currency {
			continue
		}
		return p, true
	}
	return PricePoint{}, false
}

// pruneRouteLocked drops the oldest observations for routeKey beyond the cap,
// preserving order. Caller holds s.mu.
func (s *Store) pruneRouteLocked(routeKey string) {
	var idx []int
	for i, p := range s.history {
		if p.RouteKey == routeKey {
			idx = append(idx, i)
		}
	}
	if len(idx) <= maxObservationsPerRoute {
		return
	}
	drop := make(map[int]bool, len(idx)-maxObservationsPerRoute)
	for _, i := range idx[:len(idx)-maxObservationsPerRoute] {
		drop[i] = true
	}
	kept := s.history[:0:0]
	for i, p := range s.history {
		if !drop[i] {
			kept = append(kept, p)
		}
	}
	s.history = kept
}

// AllHistory returns a snapshot of every price point in the store — both
// watch-keyed (WatchID set) and route-keyed (RouteKey set) — ordered by
// insertion time. The returned slice is a copy; mutations do not affect the
// store. Callers that need the full corpus for graph construction (e.g. the
// travelgraph nudge engine) should prefer this over per-watch History calls so
// that ad-hoc route observations (MIK-6229) are included alongside the
// watch-scoped history.
func (s *Store) AllHistory() []PricePoint {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]PricePoint, len(s.history))
	copy(out, s.history)
	return out
}

// RouteHistory returns all price points recorded for a given route key, ordered
// by insertion (chronological) time.
func (s *Store) RouteHistory(routeKey string) []PricePoint {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []PricePoint
	for _, p := range s.history {
		if p.RouteKey == routeKey {
			out = append(out, p)
		}
	}
	return out
}

// RoutePrices returns the price values for a route key, filtered to a currency
// so callers never mix currencies into a single price-position computation.
// An empty currency returns every recorded price for the key.
func (s *Store) RoutePrices(routeKey, currency string) []float64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	cur := strings.ToUpper(strings.TrimSpace(currency))
	var out []float64
	for _, p := range s.history {
		if p.RouteKey != routeKey {
			continue
		}
		if cur != "" && strings.ToUpper(p.Currency) != cur {
			continue
		}
		out = append(out, p.Price)
	}
	return out
}
