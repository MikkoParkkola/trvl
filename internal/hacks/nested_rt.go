package hacks

// MIK-3076: nested / overlapping round-trip ticket combinator.
//
// For multi-visit scenarios where a traveller wants to be in destination
// B twice within a window (the HEL+AMS monthly commute case), the naive
// strategy is two separate round-trips:
//
//   trip 1: A1 -> B1 -> A1   (e.g. AMS -> HEL Mon..Fri week-1)
//   trip 2: A2 -> B2 -> A2   (e.g. AMS -> HEL Mon..Fri week-3)
//
// Airlines often price return-fares cheaper than one-ways, so reshuffling
// the four legs into two *overlapping* round-trips wins:
//
//   trip X: A1 -> B2 -> A1   (long stay)
//   trip Y: B1 -> A2 -> B1   (anchored at the destination)
//
// Both produce the same ground itinerary (presence in B from B1..A1 and
// B2..A2) but the second pairing typically saves 20-40%. Existing
// detector handles same-route back-to-back; this one generalises across
// two arbitrary A<->B visit windows so the optimizer can offer it on
// any 2-visit query.
//
// This file ships the pure ranker only: it consumes a slice of priced
// PairingOffers and returns the top-K cheapest with savings vs naive
// baseline. The live-pricing layer + MCP tool / CLI wiring will follow
// in a separate change so the math here can be unit-tested without any
// network dependency.

import (
	"fmt"
	"sort"
)

// VisitWindow describes one of the two visit intents. Direction is
// always "outbound to B then back to A"; the combinator decides how to
// shuffle the legs.
type VisitWindow struct {
	// DepartDate is the desired outbound date in ISO 8601 form.
	DepartDate string
	// ReturnDate is the desired homebound date in ISO 8601 form.
	ReturnDate string
	// LegFromAToB is the priced one-way A->B leg for this window.
	LegFromAToB float64
	// LegFromBToA is the priced one-way B->A leg for this window.
	LegFromBToA float64
	// RoundTripFromA is the priced round-trip A->B->A for this window
	// (cheaper than two one-ways for many carriers — used as the
	// per-window naive baseline).
	RoundTripFromA float64
	// RoundTripFromB is the priced round-trip B->A->B anchored on the
	// destination side. Required for the nested option that keeps the
	// inner stay continuous in B.
	RoundTripFromB float64
}

// PairingKind labels how the combinator reshuffled the four legs.
type PairingKind string

const (
	// PairingNaive is the user-facing baseline: two separate round-trips
	// rooted on side A. Always present so the savings comparison is
	// honest (zero savings vs itself).
	PairingNaive PairingKind = "naive_two_rt"
	// PairingNestedFromA reshuffles into a long A->B->A surrounding a
	// shorter B->A->B sequence — both rooted on A.
	PairingNestedFromA PairingKind = "nested_from_a"
	// PairingNestedFromB anchors the inner trip on side B (B->A->B)
	// while the outer trip stays on side A. This is the canonical
	// 'overlapping round-trip' pairing.
	PairingNestedFromB PairingKind = "nested_from_b"
	// PairingTwoOneWays is the explicit four-one-way bound, useful when
	// neither carrier publishes round-trip discounts and the consumer
	// wants the assignment regardless.
	PairingTwoOneWays PairingKind = "two_one_ways"
)

// PairingResult is one ranked itinerary candidate produced by the
// combinator. Cost is the total trip price in the input currency
// (caller responsible for normalisation); SavingsEUR is the absolute
// gap vs the naive baseline so callers can render "you save €X".
type PairingResult struct {
	Kind         PairingKind
	Cost         float64
	SavingsEUR   float64
	SavingsPct   float64
	Reason       string
	LegBreakdown map[string]float64
}

// EnumeratePairings constructs every pairing the combinator knows about
// from two visit windows. Pure function — it does not rank or trim;
// callers feed the result into Rank for the top-K view.
//
// Returns nil when either window is missing required prices for *any*
// pairing variant. Callers may opt to call individual builders below
// when they know which prices are available.
func EnumeratePairings(w1, w2 VisitWindow) []PairingResult {
	if !visitPriced(w1) || !visitPriced(w2) {
		return nil
	}
	out := make([]PairingResult, 0, 4)
	out = append(out, naivePairing(w1, w2))
	out = append(out, twoOneWaysPairing(w1, w2))
	if w1.RoundTripFromA > 0 && w2.RoundTripFromA > 0 {
		out = append(out, nestedFromAPairing(w1, w2))
	}
	if w1.RoundTripFromA > 0 && w2.RoundTripFromB > 0 {
		out = append(out, nestedFromBPairing(w1, w2))
	}
	return out
}

// Rank sorts the supplied pairings by Cost ascending and returns the
// top K (clipped to len when K is larger). Savings vs baseline are
// recomputed against the cheapest naive pairing in the input so the
// figures stay consistent if the caller pre-filtered the slice.
func Rank(pairings []PairingResult, k int) []PairingResult {
	if len(pairings) == 0 {
		return nil
	}
	sorted := make([]PairingResult, len(pairings))
	copy(sorted, pairings)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Cost < sorted[j].Cost
	})

	baseline := naiveBaseline(pairings)
	for i := range sorted {
		recomputeSavings(&sorted[i], baseline)
	}

	if k <= 0 || k > len(sorted) {
		k = len(sorted)
	}
	return sorted[:k]
}

// EnumerateAndRank is the convenience wrapper most callers want: build
// all pairings from the two windows then take the top K. Returns nil
// when either window is unpriced. Top K is clipped to the candidate
// count, so passing K=99 is safe.
func EnumerateAndRank(w1, w2 VisitWindow, k int) []PairingResult {
	return Rank(EnumeratePairings(w1, w2), k)
}

func visitPriced(w VisitWindow) bool {
	if w.DepartDate == "" || w.ReturnDate == "" {
		return false
	}
	if w.LegFromAToB <= 0 || w.LegFromBToA <= 0 {
		return false
	}
	return true
}

func naivePairing(w1, w2 VisitWindow) PairingResult {
	a := preferRoundTrip(w1.RoundTripFromA, w1.LegFromAToB+w1.LegFromBToA)
	b := preferRoundTrip(w2.RoundTripFromA, w2.LegFromAToB+w2.LegFromBToA)
	return PairingResult{
		Kind:   PairingNaive,
		Cost:   a + b,
		Reason: "two independent round-trips rooted on the home side; the do-nothing baseline",
		LegBreakdown: map[string]float64{
			"trip1_rt_from_a": a,
			"trip2_rt_from_a": b,
		},
	}
}

func twoOneWaysPairing(w1, w2 VisitWindow) PairingResult {
	cost := w1.LegFromAToB + w1.LegFromBToA + w2.LegFromAToB + w2.LegFromBToA
	return PairingResult{
		Kind:   PairingTwoOneWays,
		Cost:   cost,
		Reason: "four explicit one-ways; useful when the carrier does not discount round-trips",
		LegBreakdown: map[string]float64{
			"w1_a_to_b": w1.LegFromAToB,
			"w1_b_to_a": w1.LegFromBToA,
			"w2_a_to_b": w2.LegFromAToB,
			"w2_b_to_a": w2.LegFromBToA,
		},
	}
}

// nestedFromAPairing keeps both round-trips rooted on side A but
// shuffles the dates so the long-stay surrounds the short-stay. Net
// cost is identical to two RoundTripFromA quotes, but exposed so
// callers can show "we considered this and rejected it" when relevant.
func nestedFromAPairing(w1, w2 VisitWindow) PairingResult {
	cost := w1.RoundTripFromA + w2.RoundTripFromA
	return PairingResult{
		Kind:   PairingNestedFromA,
		Cost:   cost,
		Reason: "two A-rooted round-trips, dates shuffled; same cost as naive but kept for transparency",
		LegBreakdown: map[string]float64{
			"long_rt_from_a":  w1.RoundTripFromA,
			"short_rt_from_a": w2.RoundTripFromA,
		},
	}
}

// nestedFromBPairing is the canonical overlapping round-trip: outer
// trip is A->B->A spanning both visits; inner trip is B->A->B booked
// on a B-rooted itinerary. Dates work because the user sleeps in B
// between w1.DepartDate and w2.ReturnDate.
func nestedFromBPairing(w1, w2 VisitWindow) PairingResult {
	cost := w1.RoundTripFromA + w2.RoundTripFromB
	return PairingResult{
		Kind:   PairingNestedFromB,
		Cost:   cost,
		Reason: "outer A->B->A wraps an inner B->A->B; classic overlapping round-trip",
		LegBreakdown: map[string]float64{
			"outer_rt_from_a": w1.RoundTripFromA,
			"inner_rt_from_b": w2.RoundTripFromB,
		},
	}
}

func preferRoundTrip(rt, twoOneWays float64) float64 {
	if rt <= 0 {
		return twoOneWays
	}
	if twoOneWays <= 0 {
		return rt
	}
	if rt < twoOneWays {
		return rt
	}
	return twoOneWays
}

func naiveBaseline(p []PairingResult) float64 {
	best := 0.0
	for _, c := range p {
		if c.Kind == PairingNaive {
			if best == 0 || c.Cost < best {
				best = c.Cost
			}
		}
	}
	return best
}

func recomputeSavings(p *PairingResult, baseline float64) {
	if baseline <= 0 {
		return
	}
	gap := baseline - p.Cost
	if gap < 0 {
		gap = 0
	}
	p.SavingsEUR = gap
	p.SavingsPct = (gap / baseline) * 100
	if gap > 0 {
		p.Reason = fmt.Sprintf("%s — saves %.2f (%.1f%%) vs naive baseline", p.Reason, gap, p.SavingsPct)
	}
}
