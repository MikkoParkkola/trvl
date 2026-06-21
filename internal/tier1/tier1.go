// Package tier1 runs the MIK-6234 scheduler-amortized counterfactual probe: at
// most one budget-gated fan-out per scheduler tick, rotated across active flight
// watches, with results cached for later call-free reads by a flight search.
//
// It is the glue that lets the watch daemon pre-compute cold-route
// counterfactuals (nearby-airport, split, hidden-city) on its idle cycles
// without ever fanning out at a user's query time. The detect function is
// injected so the package is testable without the network and so the watch
// package never depends on the hacks engine.
package tier1

import (
	"context"
	"time"

	"github.com/MikkoParkkola/trvl/internal/cfprobe"
	"github.com/MikkoParkkola/trvl/internal/hacks"
	"github.com/MikkoParkkola/trvl/internal/probecache"
	"github.com/MikkoParkkola/trvl/internal/watch"
)

// DetectFunc is the fan-out producer (hacks.DetectAll in production).
type DetectFunc func(ctx context.Context, in hacks.DetectorInput) []hacks.Hack

// ProbeOne runs at most one budget-gated probe for a single active flight watch,
// selected by tick rotation, and caches the savings for later call-free reads.
// It is best-effort: when the probe budget is exhausted it caches nothing and
// returns, so the daemon never fans out unbounded. Returns true when a probe ran.
func ProbeOne(ctx context.Context, active []watch.Watch, tick int, engine *cfprobe.Engine, cache *probecache.Store, now time.Time, detect DetectFunc) bool {
	if engine == nil || cache == nil || detect == nil {
		return false
	}
	candidates := flightCandidates(active)
	if len(candidates) == 0 {
		return false
	}
	w := candidates[((tick%len(candidates))+len(candidates))%len(candidates)]

	in := hacks.DetectorInput{
		Origin:      w.Origin,
		Destination: w.Destination,
		Date:        w.DepartDate,
		ReturnDate:  w.ReturnDate,
		Currency:    w.Currency,
		NaivePrice:  w.LastPrice,
		Passengers:  1,
	}
	savings, status := engine.Probe(now, func() []hacks.Hack { return detect(ctx, in) })
	if status != cfprobe.StatusRan {
		return false // budget exhausted: cache nothing, never a silent unbounded fan-out
	}
	// Served call-free from the cache at read time; stamp accordingly so the
	// flight renderer shows them under "no extra searches (as of N ago)".
	for i := range savings {
		savings[i].CallFree = true
		savings[i].AsOf = now
	}
	_ = cache.Put(probecache.RouteKey(w.Origin, w.Destination), savings, now)
	return true
}

// flightCandidates selects flight watches with enough information to probe.
func flightCandidates(active []watch.Watch) []watch.Watch {
	var out []watch.Watch
	for _, w := range active {
		if w.Type == "flight" && w.Origin != "" && w.Destination != "" && w.LastPrice > 0 {
			out = append(out, w)
		}
	}
	return out
}
