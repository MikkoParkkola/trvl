package watch

import (
	"math"
	"sort"
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
func (s *Store) RecordObservation(routeKey string, price float64, currency string) error {
	if routeKey == "" || price <= 0 {
		return nil
	}
	return s.withTxn(func() error {
		cur := strings.ToUpper(strings.TrimSpace(currency))
		if last, ok := s.lastObservationLocked(routeKey, cur); ok && last.Price > 0 {
			if time.Since(last.Timestamp) < observationThrottle &&
				math.Abs(price-last.Price)/last.Price <= observationEpsilonPct {
				// Redundant near-duplicate. errTxnNoop unwinds without writing:
				// saving here would republish this process's whole snapshot
				// over a concurrent writer's for an observation we decided not
				// to keep.
				return errTxnNoop
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

// pruneGlobalRouteLocked bounds the total number of ad-hoc route-keyed
// observations to maxRouteObservations. Watch-keyed points (WatchID set) are
// never touched.
//
// Eviction is fair, not oldest-first (#511). Oldest-first across the whole
// corpus lets one busy route's recent points push a quiet route's entire history
// past the eviction boundary: the quiet route loses everything while never
// exceeding its own per-route cap, and the loss is silent and permanent.
//
// The policy is water-filling. Find the largest per-route quota q for which
// sum over routes of min(len(route), q) fits the global cap, then keep the
// newest q points of every route. Routes below quota are untouched, so pressure
// falls entirely on the largest contributors (TRVL.WATCH.EVICT.2), every route
// keeps a floor of q (EVICT.1), what survives is always the newest points —
// the ones sparklines and drop detection read (EVICT.4) — and the retained
// total never exceeds the cap (EVICT.5).
//
// Degradation when routes outnumber the cap: q would be 0 and fairness cannot
// be satisfied, since not even one point per route fits. In that case the
// most-recently-active maxRouteObservations routes keep their newest point each
// and the rest are dropped, which still bounds the file and still favours the
// newest data. maxRouteObservations is 20000, so this is a theoretical branch,
// not an operational one.
func (s *Store) pruneGlobalRouteLocked() {
	// Cheap counting pass first. Compaction runs on every observation, so the
	// overwhelming majority of calls are under-cap no-ops; grouping by route
	// before knowing that costs a map build (~5x the whole pass, measured by
	// BenchmarkPruneAtCap*) for nothing.
	total := 0
	for _, p := range s.history {
		if p.RouteKey != "" && p.WatchID == "" {
			total++
		}
	}
	if total <= maxRouteObservations {
		return
	}

	// Over cap: index every route-keyed point by route, in insertion
	// (chronological) order.
	order := make([]string, 0, 8)
	byRoute := make(map[string][]int)
	for i, p := range s.history {
		if p.RouteKey == "" || p.WatchID != "" {
			continue
		}
		if _, seen := byRoute[p.RouteKey]; !seen {
			order = append(order, p.RouteKey)
		}
		byRoute[p.RouteKey] = append(byRoute[p.RouteKey], i)
	}

	keep := make(map[int]bool, maxRouteObservations)
	if len(byRoute) > maxRouteObservations {
		// More routes than budget: keep the newest point of the most recently
		// active routes until the budget is spent.
		type lastPoint struct {
			idx int
			ts  time.Time
		}
		newest := make([]lastPoint, 0, len(byRoute))
		for _, key := range order {
			idxs := byRoute[key]
			last := idxs[len(idxs)-1]
			newest = append(newest, lastPoint{idx: last, ts: s.history[last].Timestamp})
		}
		sort.SliceStable(newest, func(a, b int) bool { return newest[a].ts.After(newest[b].ts) })
		for _, lp := range newest[:maxRouteObservations] {
			keep[lp.idx] = true
		}
	} else {
		quota := routeQuota(byRoute, maxRouteObservations)
		for _, idxs := range byRoute {
			tail := idxs
			if len(tail) > quota {
				tail = tail[len(tail)-quota:] // newest quota points
			}
			for _, i := range tail {
				keep[i] = true
			}
		}
	}

	kept := s.history[:0:0]
	for i, p := range s.history {
		if p.RouteKey == "" || p.WatchID != "" || keep[i] {
			kept = append(kept, p)
		}
	}
	s.history = kept
}

// routeQuota returns the largest per-route retention quota whose water-filled
// total fits cap. Callers guarantee len(byRoute) <= cap, so the result is >= 1.
func routeQuota(byRoute map[string][]int, limit int) int {
	longest := 0
	for _, idxs := range byRoute {
		if len(idxs) > longest {
			longest = len(idxs)
		}
	}
	fits := func(q int) bool {
		sum := 0
		for _, idxs := range byRoute {
			if len(idxs) < q {
				sum += len(idxs)
			} else {
				sum += q
			}
			if sum > limit {
				return false
			}
		}
		return true
	}
	// Binary search the largest q in [1, longest] that fits.
	lo, hi, best := 1, longest, 1
	for lo <= hi {
		mid := lo + (hi-lo)/2
		if fits(mid) {
			best = mid
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	return best
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
