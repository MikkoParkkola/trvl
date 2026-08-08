package watch

import (
	"fmt"
	"testing"
	"time"
)

// TRVL.WATCH.EVICT.1 through TRVL.WATCH.EVICT.5.
//
// #511 describes the hazard against a per-watch cap (maxObservationsPerWatch /
// maxWatchObservations). Those constants do not exist on this branch and
// watch-keyed points are explicitly exempt from eviction (pruneGlobalRouteLocked
// skips any point with WatchID set), so the live instance of the bug is the
// route-keyed corpus, which had exactly the same shape: one global cap, evicted
// oldest-first across every key. This test constructs that condition — well over
// 50 distinct keys, one busy key outproducing the rest — and asserts the quiet
// keys survive it.
//
// Against the old oldest-first pruner every quiet route is written before the
// busy route's burst, so all of their points fall inside the evicted prefix and
// each quiet route is reduced to zero retained points.
func TestGlobalEvictionKeepsQuietRoutesAlive(t *testing.T) {
	const (
		quietRoutes    = 60
		quietPoints    = 3
		busyPoints     = 400
		saturatedLimit = 300
	)

	restore := maxRouteObservations
	maxRouteObservations = saturatedLimit
	t.Cleanup(func() { maxRouteObservations = restore })

	s := NewStore(t.TempDir())

	// Alternating prices keep every write outside the near-duplicate throttle, so
	// each RecordObservation call really lands a point. The per-index offset makes
	// every price in a series distinct, so "kept the newest N" and "kept the
	// oldest N" are distinguishable — with a repeating series they are not.
	price := func(i int) float64 {
		return 100 + 400*float64(i%2) + float64(i)*0.001
	}

	// recorded[key] is every price written for that key, in order.
	recorded := make(map[string][]float64, quietRoutes+1)

	// Quiet routes first: they are the oldest points in the corpus, which is what
	// made them the first casualties under oldest-first eviction.
	quietKeys := make([]string, 0, quietRoutes)
	for r := 0; r < quietRoutes; r++ {
		key := RouteKey("flight", "AMS", fmt.Sprintf("Q%02d", r), "2026-07-15")
		quietKeys = append(quietKeys, key)
		for i := 0; i < quietPoints; i++ {
			if err := s.RecordObservation(key, price(i), "EUR"); err != nil {
				t.Fatalf("record quiet observation: %v", err)
			}
			recorded[key] = append(recorded[key], price(i))
		}
	}

	busyKey := RouteKey("flight", "HEL", "BCN", "2026-07-15")
	for i := 0; i < busyPoints; i++ {
		if err := s.RecordObservation(busyKey, price(i), "EUR"); err != nil {
			t.Fatalf("record busy observation: %v", err)
		}
		recorded[busyKey] = append(recorded[busyKey], price(i))
	}

	// EVICT.5 — the global cap still binds.
	total := 0
	for _, p := range s.AllHistory() {
		if p.RouteKey != "" && p.WatchID == "" {
			total++
		}
	}
	if total > saturatedLimit {
		t.Fatalf("retained route points = %d, exceeds global cap %d", total, saturatedLimit)
	}
	if total < saturatedLimit/2 {
		t.Fatalf("retained route points = %d, cap %d: eviction is throwing away budget", total, saturatedLimit)
	}

	// EVICT.1 / EVICT.2 — every quiet route keeps its points; the busy route
	// cannot drive any of them to zero.
	for _, key := range quietKeys {
		got := s.RouteHistory(key)
		if len(got) == 0 {
			t.Fatalf("quiet route %s lost its entire history to global eviction", key)
		}
		if len(got) != quietPoints {
			t.Fatalf("quiet route %s retained %d points, want %d: pressure must fall on the largest contributor",
				key, len(got), quietPoints)
		}
	}

	// The busy route is the one that pays, and it is not wiped out either.
	busy := s.RouteHistory(busyKey)
	if len(busy) == 0 {
		t.Fatal("busy route retained nothing")
	}
	if len(busy) >= busyPoints {
		t.Fatalf("busy route retained %d of %d points: the largest contributor was not trimmed", len(busy), busyPoints)
	}

	// EVICT.4 — what survives is the newest run of each series, not an arbitrary
	// subset of it: sparklines and drop detection read the tail. Every price is
	// distinct, so retaining the oldest points instead of the newest fails here.
	for key, want := range recorded {
		got := s.RouteHistory(key)
		if len(got) == 0 {
			t.Fatalf("route %s retained nothing", key)
		}
		if len(got) > len(want) {
			t.Fatalf("route %s retained %d points, more than the %d written", key, len(got), len(want))
		}
		tail := want[len(want)-len(got):]
		for i := range got {
			if got[i].Price != tail[i] {
				t.Fatalf("route %s retained price[%d] = %v, want %v: eviction kept the wrong end of the series",
					key, i, got[i].Price, tail[i])
			}
		}
	}
}

// The degradation branch: more distinct routes than the whole budget. Fairness
// is unachievable (not even one point each fits), so the pruner keeps the newest
// point of the most recently active routes and still respects the cap.
func TestGlobalEvictionWithMoreRoutesThanBudget(t *testing.T) {
	const limit = 10

	restore := maxRouteObservations
	maxRouteObservations = limit
	t.Cleanup(func() { maxRouteObservations = restore })

	s := NewStore(t.TempDir())
	for r := 0; r < limit*3; r++ {
		key := RouteKey("flight", "AMS", fmt.Sprintf("R%02d", r), "2026-07-15")
		if err := s.RecordObservation(key, 100, "EUR"); err != nil {
			t.Fatalf("record: %v", err)
		}
	}

	total := 0
	for _, p := range s.AllHistory() {
		if p.RouteKey != "" && p.WatchID == "" {
			total++
		}
	}
	if total != limit {
		t.Fatalf("retained = %d, want exactly the cap %d", total, limit)
	}

	// The most recently written routes are the ones that survive.
	for r := limit * 3 * 2 / 3; r < limit*3; r++ {
		key := RouteKey("flight", "AMS", fmt.Sprintf("R%02d", r), "2026-07-15")
		if len(s.RouteHistory(key)) != 1 {
			t.Fatalf("recently active route %s was evicted", key)
		}
	}
}

// TRVL.RETENTION.1 -- correcting a one-point overflow must spend the whole
// retention budget. Uneven route sizes are deliberate: a single water-fill
// quota used to retain only 8 of these 11 points against a cap of 10.
func TestGlobalEvictionOnePointOverflowEvictsOne(t *testing.T) {
	const limit = 10
	restore := maxRouteObservations
	maxRouteObservations = limit
	t.Cleanup(func() { maxRouteObservations = restore })

	s := NewStore(t.TempDir())
	base := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 7; i++ {
		s.history = append(s.history, PricePoint{
			RouteKey: "flight|AMS|BUSY|2026-09-01", Price: float64(i), Currency: "EUR", Timestamp: base.Add(time.Duration(i) * time.Minute),
		})
	}
	for i := 0; i < 4; i++ {
		s.history = append(s.history, PricePoint{
			RouteKey: "flight|AMS|QUIET|2026-09-01", Price: float64(100 + i), Currency: "EUR", Timestamp: base.Add(time.Duration(10+i) * time.Minute),
		})
	}

	s.pruneGlobalRouteLocked()
	if got := len(s.history); got != limit {
		t.Fatalf("one-point overflow retained %d points, want exactly cap %d", got, limit)
	}
	busy := s.RouteHistory("flight|AMS|BUSY|2026-09-01")
	quiet := s.RouteHistory("flight|AMS|QUIET|2026-09-01")
	if len(busy) != 6 || len(quiet) != 4 {
		t.Fatalf("retained busy=%d quiet=%d, want 6 and 4", len(busy), len(quiet))
	}
	if busy[0].Price != 1 || busy[len(busy)-1].Price != 6 {
		t.Fatalf("busy route kept the wrong tail: prices %v..%v", busy[0].Price, busy[len(busy)-1].Price)
	}
}

// Watch-keyed history is never touched by route-corpus eviction, even when the
// route corpus is saturated. This is the invariant that keeps #511's fix from
// becoming #511's cause.
func TestGlobalEvictionNeverTouchesWatchHistory(t *testing.T) {
	const limit = 20

	restore := maxRouteObservations
	maxRouteObservations = limit
	t.Cleanup(func() { maxRouteObservations = restore })

	s := NewStore(t.TempDir())
	for i := 0; i < 5; i++ {
		if err := s.RecordPrice("watch-1", float64(100+i), "EUR"); err != nil {
			t.Fatalf("record price: %v", err)
		}
	}
	for r := 0; r < 4; r++ {
		key := RouteKey("flight", "AMS", fmt.Sprintf("R%d", r), "2026-07-15")
		for i := 0; i < 30; i++ {
			p := 100.0
			if i%2 == 1 {
				p = 500
			}
			if err := s.RecordObservation(key, p, "EUR"); err != nil {
				t.Fatalf("record observation: %v", err)
			}
		}
	}

	if got := len(s.History("watch-1")); got != 5 {
		t.Fatalf("watch-keyed history = %d, want 5 (must never be evicted)", got)
	}
}

// #511's fail-fast asks for a measurement before adopting a fair policy: fair
// eviction groups the corpus by route and binary-searches a quota where the old
// policy was a single pass. These benchmarks compare the two at the production
// cap, in the two states a real store is in — exactly at the cap (the common
// per-write case, since compaction runs on every observation) and one point
// over it (the case that actually evicts).
//
// prunePrefixBaseline is the pre-fix oldest-first pruner, kept only as that
// baseline. It is not reachable from production code.
func prunePrefixBaseline(s *Store) {
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

// saturatedCorpus builds n route-keyed points spread over the given number of
// distinct route keys.
func saturatedCorpus(n, routes int) []PricePoint {
	base := time.Now().Add(-time.Duration(n) * time.Second)
	out := make([]PricePoint, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, PricePoint{
			RouteKey:  RouteKey("flight", "AMS", fmt.Sprintf("R%04d", i%routes), "2026-07-15"),
			Price:     100 + float64(i%400),
			Currency:  "EUR",
			Timestamp: base.Add(time.Duration(i) * time.Second),
		})
	}
	return out
}

func benchPrune(b *testing.B, extra int, prune func(*Store)) {
	corpus := saturatedCorpus(maxRouteObservations+extra, 200)
	s := NewStore(b.TempDir())
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		s.history = append(s.history[:0:0], corpus...)
		b.StartTimer()
		prune(s)
	}
}

func BenchmarkPruneAtCapFair(b *testing.B)     { benchPrune(b, 0, (*Store).pruneGlobalRouteLocked) }
func BenchmarkPruneAtCapPrefix(b *testing.B)   { benchPrune(b, 0, prunePrefixBaseline) }
func BenchmarkPruneOverCapFair(b *testing.B)   { benchPrune(b, 1, (*Store).pruneGlobalRouteLocked) }
func BenchmarkPruneOverCapPrefix(b *testing.B) { benchPrune(b, 1, prunePrefixBaseline) }
