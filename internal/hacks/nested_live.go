package hacks

// nested_live.go adds a live-pricing wrapper over the pure nested round-trip
// ranker (nested_rt.go), shared by the MCP tool (optimize_nested_rt) and the
// CLI (trvl nested). Pricing fans out across the flight providers in parallel.
//
// Tracking: MIK-3076.

import (
	"context"
	"sync"

	"github.com/MikkoParkkola/trvl/internal/flights"
)

// LegPricer returns the cheapest price for a search; returnDate empty = one-way.
// Injected so callers (and tests) can supply a deterministic offline pricer.
type LegPricer func(ctx context.Context, origin, dest, date, returnDate string) float64

// DefaultLegPricer prices via the real flight providers (cheapest comparable).
func DefaultLegPricer(ctx context.Context, origin, dest, date, returnDate string) float64 {
	res, err := flights.SearchFlights(ctx, origin, dest, date, flights.SearchOptions{ReturnDate: returnDate})
	if err != nil || res == nil || len(res.Flights) == 0 {
		return 0
	}
	best := 0.0
	for _, f := range res.Flights {
		if p := f.PriceForRanking(); p > 0 && (best == 0 || p < best) {
			best = p
		}
	}
	return best
}

// priceWindow fans out the four quotes a VisitWindow needs, in parallel.
func priceWindow(ctx context.Context, a, b, departDate, returnDate string, price LegPricer) VisitWindow {
	w := VisitWindow{DepartDate: departDate, ReturnDate: returnDate}
	var wg sync.WaitGroup
	wg.Add(4)
	go func() { defer wg.Done(); w.RoundTripFromA = price(ctx, a, b, departDate, returnDate) }()
	go func() { defer wg.Done(); w.LegFromAToB = price(ctx, a, b, departDate, "") }()
	go func() { defer wg.Done(); w.LegFromBToA = price(ctx, b, a, returnDate, "") }()
	go func() { defer wg.Done(); w.RoundTripFromB = price(ctx, b, a, departDate, returnDate) }()
	wg.Wait()
	return w
}

// PlanNestedRT prices two visit windows between cities a and b (in parallel) and
// returns the top-k pairings ranked cheapest-first vs the naive two-RT baseline.
func PlanNestedRT(ctx context.Context, a, b, w1Depart, w1Return, w2Depart, w2Return string, price LegPricer, k int) []PairingResult {
	var w1, w2 VisitWindow
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); w1 = priceWindow(ctx, a, b, w1Depart, w1Return, price) }()
	go func() { defer wg.Done(); w2 = priceWindow(ctx, a, b, w2Depart, w2Return, price) }()
	wg.Wait()
	return EnumerateAndRank(w1, w2, k)
}
