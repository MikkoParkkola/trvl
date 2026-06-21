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
	"strings"

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

// Nudge is a single proactive message grounded in one or more source records.
//
// Every Nudge carries at least one Source identifier so the caller can trace it
// back to the underlying watch, route key, or trip that triggered it. Nudges
// with an empty Sources slice are not produced by this package.
type Nudge struct {
	Kind    NudgeKind
	Message string
	// Sources holds the identifiers (watch ID, route key, trip ID) that ground
	// this nudge. len(Sources) >= 1 is an invariant for all emitted nudges.
	Sources []string
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
	// prefs holds user preferences for contextual filtering (currently stored for
	// future extension; not used in nudge logic itself).
	prefs *preferences.Preferences
	// trips holds planned / booked trips indexed by ID.
	trips map[string]trips.Trip
}

// Build constructs a Graph from the supplied data. None of the inputs are
// required to be non-nil; missing slices are silently treated as empty.
//
//   - ws: active watches from watch.Store.List()
//   - history: all price points, typically from watch.Store's full history
//   - prefs: user preferences (pass nil to use defaults)
//   - ts: planned or booked trips
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

	// Index price history. Points may carry a WatchID; map them to the route key
	// via the watch index we just built.
	watchToRoute := make(map[string]string, len(ws))
	for _, w := range ws {
		watchToRoute[w.ID] = routeKey(w.Origin, w.Destination)
	}

	for _, p := range history {
		key, ok := watchToRoute[p.WatchID]
		if !ok {
			continue // point belongs to a deleted or unknown watch; skip
		}
		rh := g.routeFor(key)
		rh.points = append(rh.points, p)
	}

	return g
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
func Nudges(g *Graph) []Nudge {
	var out []Nudge
	// Track which routes already produced a KindBelowThreshold nudge so the
	// historic-low check doesn't double-fire on the same route.
	belowRoutes := make(map[string]bool)

	for key, rh := range g.byRoute {
		belowNudges := belowThresholdNudges(rh)
		if len(belowNudges) > 0 {
			out = append(out, belowNudges...)
			belowRoutes[key] = true
		}
	}

	for key, rh := range g.byRoute {
		if belowRoutes[key] {
			continue // already nudged via threshold crossing
		}
		if n, ok := historicLowNudge(key, rh); ok {
			out = append(out, n)
		}
	}

	return out
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
			Sources: []string{w.ID},
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
// the most recent price point in history. Returns (Nudge, true) when a nudge
// fires, (Nudge{}, false) otherwise.
func historicLowNudge(key string, rh *routeHistory) (Nudge, bool) {
	if len(rh.points) < 3 {
		return Nudge{}, false
	}

	latest := latestPoint(rh.points)
	if latest.Price <= 0 {
		return Nudge{}, false
	}

	res := fareintel.Analyze(latest.Price, latest.Currency, rh.points)
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
		Sources: []string{key},
	}, true
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
