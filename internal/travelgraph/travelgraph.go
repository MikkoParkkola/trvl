// Package travelgraph joins watches, price history, trips, and preferences into
// a personal travel graph and emits GROUNDED proactive nudges.
//
// A nudge is grounded when it derives from a real data record — a watch whose
// LastPrice has crossed below its BelowPrice threshold, or a route whose current
// price is at or near a multi-month low confirmed by fareintel. Nudges are NEVER
// speculative: if no grounded trigger exists, no nudge is emitted.
//
// All core functions are pure (no file I/O, no network) so they are trivially
// unit-testable with constructed data.
package travelgraph

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/fareintel"
	"github.com/MikkoParkkola/trvl/internal/preferences"
	"github.com/MikkoParkkola/trvl/internal/trips"
	"github.com/MikkoParkkola/trvl/internal/watch"
)

// routeKey returns a canonical "ORIGIN-DESTINATION" key (upper-case, trimmed).
func routeKey(origin, destination string) string {
	return strings.ToUpper(strings.TrimSpace(origin)) + "-" +
		strings.ToUpper(strings.TrimSpace(destination))
}

// NudgeKind classifies the trigger that produced a nudge.
type NudgeKind string

const (
	// KindBelowThreshold fires when a watched route's LastPrice has crossed
	// below its configured BelowPrice target.
	KindBelowThreshold NudgeKind = "below_threshold"

	// KindHistoricLow fires when fareintel rates the current price as "buy"
	// with high confidence — meaning it is materially below the observed median
	// and backed by sufficient observations.
	KindHistoricLow NudgeKind = "historic_low"
)

// SourceKind classifies the kind of record a SourceRef points at.
type SourceKind string

const (
	// SourceWatch references a watch.Watch by its ID.
	SourceWatch SourceKind = "watch"
	// SourceRoute references a route bucket by its canonical
	// "ORIGIN-DESTINATION" key.
	SourceRoute SourceKind = "route"
)

// SourceRef is a reference back to the concrete data record that grounds a
// nudge. Kind distinguishes the record type; ID is its identifier (a watch ID
// or a route key), so a caller can always trace a nudge to its origin record.
type SourceRef struct {
	Kind SourceKind
	ID   string
}

// Nudge is a single proactive message grounded in one source record at minimum.
//
// Every Nudge carries at least one SourceRef so the caller can trace it back to
// the underlying watch or route that triggered it. Nudges with an empty Sources
// slice are not produced by this package.
type Nudge struct {
	Kind    NudgeKind
	Message string
	// Sources holds the source-record references that ground this nudge.
	// len(Sources) >= 1 is an invariant for all emitted nudges.
	Sources []SourceRef
}

// routeHistory groups price points by route key (for multi-watch aggregation).
type routeHistory struct {
	points  []watch.PricePoint
	watches []watch.Watch // watches that cover this route
}

// Graph is the indexed travel graph built from the user's data.
// Use [Build] to construct it; treat fields as read-only after construction.
type Graph struct {
	// byRoute maps "ORIGIN-DESTINATION" to the aggregated history and watches.
	byRoute map[string]*routeHistory
	// prefs holds user preferences. They gate and rank emitted nudges:
	// ExcludedDestinations hard-drops unwanted routes, and home airports +
	// AirportAffinity reorder the surviving nudges by relevance (see Nudges).
	prefs *preferences.Preferences
	// trips holds planned / booked trips indexed by ID.
	trips map[string]trips.Trip
}

// Build constructs a Graph from the supplied data. None of the inputs are
// required to be non-nil; missing slices are silently treated as empty.
//
//   - ws: active watches from watch.Store.List()
//   - history: all price points — pass watch.Store.AllHistory() so that both
//     watch-keyed and route-keyed (MIK-6229 ad-hoc corpus) points are included.
//   - prefs: user preferences (pass nil to use defaults)
//   - ts: planned or booked trips
//
// Route-keyed points (RouteKey != "") are bucketed by parsing the key
// ("FLIGHT|ORIG|DEST|date") to extract origin+destination. Non-flight keys and
// malformed keys are silently skipped. Watch-keyed points continue to map via
// the watch index as before, so both sources merge into the same per-route
// history that historicLowNudge evaluates.
func Build(
	ws []watch.Watch,
	history []watch.PricePoint,
	prefs *preferences.Preferences,
	ts []trips.Trip,
) *Graph {
	if prefs == nil {
		prefs = preferences.Default()
	}

	g := &Graph{
		byRoute: make(map[string]*routeHistory),
		prefs:   prefs,
		trips:   make(map[string]trips.Trip, len(ts)),
	}

	// Index trips by ID.
	for _, t := range ts {
		g.trips[t.ID] = t
	}

	// Index watches by route key, ensuring each route entry exists.
	for _, w := range ws {
		key := routeKey(w.Origin, w.Destination)
		rh := g.routeFor(key)
		rh.watches = append(rh.watches, w)
	}

	// Build watch-ID → route-key lookup for watch-keyed history points.
	watchToRoute := make(map[string]string, len(ws))
	for _, w := range ws {
		watchToRoute[w.ID] = routeKey(w.Origin, w.Destination)
	}

	// Index price history. Two sources are handled:
	//   1. Route-keyed points (RouteKey != "") from the ad-hoc search corpus
	//      (MIK-6229). Parse origin+destination from the key and bucket directly.
	//   2. Watch-keyed points (WatchID set). Map to the route via watchToRoute.
	for _, p := range history {
		if p.RouteKey != "" {
			if key, ok := parseFlightRouteKey(p.RouteKey); ok {
				rh := g.routeFor(key)
				rh.points = append(rh.points, p)
			}
			// Malformed or non-flight keys are silently skipped.
			continue
		}
		key, ok := watchToRoute[p.WatchID]
		if !ok {
			continue // point belongs to a deleted or unknown watch; skip
		}
		rh := g.routeFor(key)
		rh.points = append(rh.points, p)
	}

	return g
}

// parseFlightRouteKey extracts the canonical "ORIGIN-DESTINATION" graph key
// from a watch.RouteKey string (format "KIND|ORIGIN|DESTINATION|DATE").
// Returns ("", false) for malformed keys or non-flight kinds so that
// hotel-shaped keys are silently skipped rather than misrouted.
//
// Only "FLIGHT" kind is handled for now; hotel keys use a different shape and
// will gain their own bucketing path when hotel nudges are added.
func parseFlightRouteKey(rk string) (string, bool) {
	parts := strings.SplitN(rk, "|", 4)
	if len(parts) < 3 {
		return "", false
	}
	if strings.ToUpper(parts[0]) != "FLIGHT" {
		return "", false
	}
	origin := strings.ToUpper(strings.TrimSpace(parts[1]))
	dest := strings.ToUpper(strings.TrimSpace(parts[2]))
	if origin == "" || dest == "" {
		return "", false
	}
	return routeKey(origin, dest), true
}

// routeFor returns the routeHistory for a key, creating it on first access.
func (g *Graph) routeFor(key string) *routeHistory {
	rh, ok := g.byRoute[key]
	if !ok {
		rh = &routeHistory{}
		g.byRoute[key] = rh
	}
	return rh
}

// RouteEntry is the read-only view of a single route bucket: the watches
// covering the route and the price points observed for it. Each element retains
// its source-record reference (watch.Watch / watch.PricePoint) so callers can
// trace any joined entry back to its origin.
type RouteEntry struct {
	Watches []watch.Watch
	Points  []watch.PricePoint
}

// ByRoute returns the RouteEntry indexed under a canonical "ORIGIN-DESTINATION"
// route key (e.g. "HEL-NYC"), and whether that route exists in the graph. The
// key is normalised (upper-cased, trimmed) to match how Build indexes routes.
func (g *Graph) ByRoute(key string) (RouteEntry, bool) {
	rh, ok := g.byRoute[strings.ToUpper(strings.TrimSpace(key))]
	if !ok {
		return RouteEntry{}, false
	}
	return RouteEntry{Watches: rh.watches, Points: rh.points}, true
}

// Nudges evaluates the graph and returns all grounded nudges. It emits at most
// one nudge per watch (below-threshold check) and at most one nudge per route
// (historic-low check). If there are no grounded triggers, the slice is empty.
//
// Trigger rules:
//
//  1. A watch whose LastPrice > 0 and LastPrice <= BelowPrice fires
//     KindBelowThreshold. Source: watch.ID.
//
//  2. A route with >= 3 price-history points whose fareintel verdict is "buy"
//     and confidence is "high" fires KindHistoricLow. Source: route key.
//     This check is skipped if the route already fired a KindBelowThreshold to
//     avoid duplicate nudges for the same route.
//
// Profile awareness (uses g.prefs):
//
//   - Routes whose destination is in ExcludedDestinations are gated out: the
//     user never wants those destinations, so a "buy" signal for them is noise.
//   - Surviving nudges are ordered by profile relevance — routes departing from
//     a home (or nearby) airport rank first, then by destination AirportAffinity,
//     so the most personally relevant nudges surface at the top of the list.
//
// With nil/empty preferences the gate is a no-op and ordering is stable by
// route key, so the no-profile baseline is unchanged.
//
// now is the evaluation reference time. Price points dated after now are not yet
// observed and therefore cannot ground a nudge, so they are excluded from the
// historic-low corpus. Pass time.Now() in production.
func Nudges(g *Graph, now time.Time) []Nudge {
	var scored []scoredNudge
	// Track which routes already produced a KindBelowThreshold nudge so the
	// historic-low check doesn't double-fire on the same route.
	belowRoutes := make(map[string]bool)

	for key, rh := range g.byRoute {
		if g.isExcludedRoute(key) {
			continue
		}
		belowNudges := belowThresholdNudges(rh)
		if len(belowNudges) == 0 {
			continue
		}
		for _, n := range belowNudges {
			scored = append(scored, scoredNudge{nudge: n, key: key, rank: g.routeRank(key)})
		}
		belowRoutes[key] = true
	}

	for key, rh := range g.byRoute {
		if belowRoutes[key] || g.isExcludedRoute(key) {
			continue // already nudged via threshold crossing, or gated out
		}
		if n, ok := historicLowNudge(key, rh, now); ok {
			scored = append(scored, scoredNudge{nudge: n, key: key, rank: g.routeRank(key)})
		}
	}

	return orderByRelevance(scored)
}

// scoredNudge couples an emitted nudge with the route key it came from and the
// profile-relevance rank used to order the final slice. Higher rank sorts first.
type scoredNudge struct {
	nudge Nudge
	key   string
	rank  int
}

// orderByRelevance returns the nudges sorted by descending profile rank, with
// route key as a deterministic tiebreaker. When every rank is equal (the
// no-profile case) the result is simply route-key ordered — stable and
// reproducible, unlike the bare map iteration it replaces.
func orderByRelevance(scored []scoredNudge) []Nudge {
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].rank != scored[j].rank {
			return scored[i].rank > scored[j].rank
		}
		return scored[i].key < scored[j].key
	})
	out := make([]Nudge, 0, len(scored))
	for _, s := range scored {
		out = append(out, s.nudge)
	}
	return out
}

// splitRouteKey splits a canonical "ORIGIN-DESTINATION" key back into its two
// airport codes. Returns ("", "", false) for keys that don't contain exactly
// one separator (defensive — all keys built by routeKey are well-formed).
func splitRouteKey(key string) (origin, dest string, ok bool) {
	o, d, found := strings.Cut(key, "-")
	if !found || o == "" || d == "" {
		return "", "", false
	}
	return o, d, true
}

// isExcludedRoute reports whether the route's destination is hard-excluded by
// the user's ExcludedDestinations preference. Matching is case-insensitive on
// the destination airport code. Returns false when prefs are nil/empty.
func (g *Graph) isExcludedRoute(key string) bool {
	if g.prefs == nil || len(g.prefs.ExcludedDestinations) == 0 {
		return false
	}
	_, dest, ok := splitRouteKey(key)
	if !ok {
		return false
	}
	for _, ex := range g.prefs.ExcludedDestinations {
		if strings.EqualFold(strings.TrimSpace(ex), dest) {
			return true
		}
	}
	return false
}

// homeRankBonus is added to a route's rank when it departs from one of the
// user's home or nearby airports. It dominates the affinity component (which is
// at most affinityRankScale) so any home-origin route always outranks a
// non-home route, regardless of destination affinity.
const homeRankBonus = 1000

// affinityRankScale maps a destination's AirportAffinity score in [0,1] onto an
// integer rank contribution in [0,100], keeping ranks comparable as ints.
const affinityRankScale = 100

// routeRank scores a route for ordering: a large bonus when its origin is a
// home/nearby airport, plus a destination-affinity component. Higher is more
// relevant. Returns 0 for nil prefs so the no-profile case orders by key alone.
func (g *Graph) routeRank(key string) int {
	if g.prefs == nil {
		return 0
	}
	origin, dest, ok := splitRouteKey(key)
	if !ok {
		return 0
	}

	rank := 0
	if g.isHomeOrigin(origin) {
		rank += homeRankBonus
	}
	if aff, found := g.prefs.AirportAffinity[strings.ToUpper(dest)]; found {
		rank += int(aff * affinityRankScale)
	}
	return rank
}

// isHomeOrigin reports whether origin is one of the user's home airports or a
// configured nearby alternative. Matching is case-insensitive.
func (g *Graph) isHomeOrigin(origin string) bool {
	for _, home := range g.prefs.ExpandHomeOrigins() {
		if strings.EqualFold(home, origin) {
			return true
		}
	}
	return false
}

// belowThresholdNudges emits one KindBelowThreshold nudge per watch that has
// crossed its price target. A watch qualifies when:
//   - LastPrice > 0 (an observation exists)
//   - BelowPrice > 0 (a threshold is configured)
//   - LastPrice <= BelowPrice
func belowThresholdNudges(rh *routeHistory) []Nudge {
	var out []Nudge
	for _, w := range rh.watches {
		if !isThresholdCrossed(w) {
			continue
		}
		out = append(out, Nudge{
			Kind: KindBelowThreshold,
			Message: fmt.Sprintf(
				"%s→%s dropped to %.0f %s — below your %.0f %s target",
				w.Origin, w.Destination,
				w.LastPrice, w.Currency,
				w.BelowPrice, w.Currency,
			),
			Sources: []SourceRef{{Kind: SourceWatch, ID: w.ID}},
		})
	}
	return out
}

// isThresholdCrossed returns true when the watch has a valid observation that
// meets or beats its configured price target.
func isThresholdCrossed(w watch.Watch) bool {
	return w.LastPrice > 0 && w.BelowPrice > 0 && w.LastPrice <= w.BelowPrice
}

// historicLowNudge returns a KindHistoricLow nudge for a route when fareintel
// rates it as a "buy" with "high" confidence. The current price is derived from
// the most recent price point in history. Points dated after now are excluded
// (not yet observed). Returns (Nudge, true) when a nudge fires, (Nudge{}, false)
// otherwise.
func historicLowNudge(key string, rh *routeHistory, now time.Time) (Nudge, bool) {
	points := observedPoints(rh.points, now)
	if len(points) < 3 {
		return Nudge{}, false
	}

	latest := latestPoint(points)
	if latest.Price <= 0 {
		return Nudge{}, false
	}

	res := fareintel.Analyze(latest.Price, latest.Currency, points)
	if res.Verdict != fareintel.VerdictBuy || res.Confidence != "high" {
		return Nudge{}, false
	}

	return Nudge{
		Kind: KindHistoricLow,
		Message: fmt.Sprintf(
			"%s is at a historic low (%.0f %s, %.1f%% below median of %.0f %s) — strong buy signal",
			key,
			latest.Price, latest.Currency,
			-res.PercentVsMedian, res.MedianPrice, latest.Currency,
		),
		Sources: []SourceRef{{Kind: SourceRoute, ID: key}},
	}, true
}

// observedPoints returns the price points that are at or before now (already
// observed). A point dated after now is in the future and cannot ground a
// nudge. When now is the zero time, all points are treated as observed so
// callers that do not care about temporal filtering keep the full corpus.
func observedPoints(points []watch.PricePoint, now time.Time) []watch.PricePoint {
	if now.IsZero() {
		return points
	}
	out := make([]watch.PricePoint, 0, len(points))
	for _, p := range points {
		if !p.Timestamp.After(now) {
			out = append(out, p)
		}
	}
	return out
}

// latestPoint returns the price point with the most recent Timestamp.
// Callers must ensure len(points) >= 1.
func latestPoint(points []watch.PricePoint) watch.PricePoint {
	latest := points[0]
	for _, p := range points[1:] {
		if p.Timestamp.After(latest.Timestamp) {
			latest = p
		}
	}
	return latest
}
