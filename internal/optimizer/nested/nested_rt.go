// Package nested implements a combinatorial solver for nested / overlapping
// round-trip booking strategies.
//
// The key insight (from travel hacking): when visiting a destination twice on
// date windows A and B, buying two separate RTs is often not the cheapest
// option. Interleaving the date ranges — buying A(start)→B(end) RT and
// B(start)→A(end) RT — can be dramatically cheaper because airlines price
// longer date-range RTs lower.
package nested

import (
	"context"
	"fmt"
	"sort"
)

// Visit represents one trip to a destination within a date window.
// Dates are ISO 8601 format strings (year-month-day, e.g. "2026-04-23").
type Visit struct{ Start, End string }

// Leg represents one leg of a travel plan.
// ReturnDate empty means one-way.
type Leg struct{ Origin, Destination, Date, ReturnDate string }

// Plan is a set of legs that covers the visits, with total price.
type Plan struct {
	Name       string
	Legs       []Leg
	TotalPrice float64
	Currency   string
}

// SearchLegFunc prices a single leg via caller-injected provider.
type SearchLegFunc func(ctx context.Context, leg Leg) (price float64, currency string, err error)

// SolvePlans generates plan permutations (separate RTs, overlapping RTs, mixed OW), prices each,
// returns ranked list (cheapest first).
//
// Parameters:
//   - home: IATA code of the traveller's home airport.
//   - dest: IATA code of the destination airport.
//   - visits: ordered list of date windows at dest (must not be empty).
//   - returnHome: explicit date for the final leg home; if empty the last
//     visit's End date is used.
//   - search: caller-injected pricing function.
//
// Currency: all legs in a plan must return the same currency; mixed-currency
// plans are silently excluded. The first leg's currency is authoritative.
func SolvePlans(ctx context.Context, home string, dest string, visits []Visit, returnHome string, search SearchLegFunc) ([]Plan, error) {
	if len(visits) == 0 {
		return nil, fmt.Errorf("nested_rt: at least one visit required")
	}

	// Effective return date for the last visit.
	lastReturn := returnHome
	if lastReturn == "" {
		lastReturn = visits[len(visits)-1].End
	}

	var candidates []Plan

	// --- 1. Separate RTs (baseline): one RT per visit. ---
	{
		legs := make([]Leg, len(visits))
		for i, v := range visits {
			ret := v.End
			if i == len(visits)-1 && lastReturn != "" {
				ret = lastReturn
			}
			legs[i] = Leg{Origin: home, Destination: dest, Date: v.Start, ReturnDate: ret}
		}
		if p, err := pricePlan(ctx, "separate-rts", legs, search); err == nil {
			candidates = append(candidates, p)
		}
	}

	// --- 2. All one-way: home→dest and dest→home for every visit boundary. ---
	{
		var legs []Leg
		for i, v := range visits {
			legs = append(legs, Leg{Origin: home, Destination: dest, Date: v.Start})
			ret := v.End
			if i == len(visits)-1 && lastReturn != "" {
				ret = lastReturn
			}
			legs = append(legs, Leg{Origin: dest, Destination: home, Date: ret})
		}
		if p, err := pricePlan(ctx, "all-one-way", legs, search); err == nil {
			candidates = append(candidates, p)
		}
	}

	// --- 3. Overlapping RTs. ---
	// For N=2: outer RT (v[0].Start → v[1].End) + inner RT (v[1].Start → v[0].End).
	// For N>2: outer RT anchors v[0].Start → lastReturn; each subsequent visit
	// opens a new RT from v[i].Start → v[i-1].End, nesting inside the outer one.
	if len(visits) >= 2 {
		var legs []Leg
		// Outer leg: departs on first visit start, returns on last visit end.
		legs = append(legs, Leg{Origin: home, Destination: dest, Date: visits[0].Start, ReturnDate: lastReturn})
		// Inner legs: each visits[i] (i≥1) departs on its start, returns on visits[i-1].End.
		for i := 1; i < len(visits); i++ {
			legs = append(legs, Leg{Origin: home, Destination: dest, Date: visits[i].Start, ReturnDate: visits[i-1].End})
		}
		if p, err := pricePlan(ctx, "overlap-outer-inner", legs, search); err == nil {
			candidates = append(candidates, p)
		}
	}

	// Sort cheapest first.
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].TotalPrice < candidates[j].TotalPrice
	})

	return candidates, nil
}

// pricePlan prices all legs and returns a Plan. Returns error if context is
// cancelled or currencies are inconsistent.
func pricePlan(ctx context.Context, name string, legs []Leg, search SearchLegFunc) (Plan, error) {
	var total float64
	var currency string
	for _, leg := range legs {
		price, cur, err := search(ctx, leg)
		if err != nil {
			return Plan{}, fmt.Errorf("nested_rt: pricing leg %+v: %w", leg, err)
		}
		if currency == "" {
			currency = cur
		} else if cur != currency {
			return Plan{}, fmt.Errorf("nested_rt: mixed currencies %q vs %q in plan %q", currency, cur, name)
		}
		total += price
	}
	return Plan{Name: name, Legs: legs, TotalPrice: total, Currency: currency}, nil
}
