// Package pricesignal computes a price-position signal: where a current price
// sits within a route's own local price history, and a buy/wait verdict.
//
// It is deliberately dependency-free (operates on plain float64 prices) so it
// can be reused by the CLI, the MCP layer, the counterfactual engine, and the
// travel graph without import cycles.
//
// The cardinal rule is honesty under sparse data: below a confidence floor the
// signal reports Unknown and never asserts a trend. A wrong "wait" that costs a
// booking burns the trust that makes the history corpus valuable, so the floor
// is mandatory, not advisory.
package pricesignal

import "sort"

// DefaultConfidenceFloor is the minimum number of historical observations
// required before a band or verdict is emitted. Below this, Compute returns a
// BandUnknown / VerdictUnknown position with Confident=false.
const DefaultConfidenceFloor = 10

// Band describes where a current price sits relative to a route's history,
// using terciles of the historical distribution.
type Band string

const (
	// BandLow means the current price is in the cheapest third of history.
	BandLow Band = "low"
	// BandTypical means the current price is in the middle third.
	BandTypical Band = "typical"
	// BandHigh means the current price is in the most expensive third.
	BandHigh Band = "high"
	// BandUnknown means there is not enough history to judge.
	BandUnknown Band = "unknown"
)

// Verdict is a buy/wait recommendation derived from the band.
type Verdict string

const (
	// VerdictBuy recommends booking now (price is in the cheap third).
	VerdictBuy Verdict = "buy"
	// VerdictWait suggests waiting (price is in the expensive third).
	VerdictWait Verdict = "wait"
	// VerdictNeutral means no strong signal either way (typical price).
	VerdictNeutral Verdict = "neutral"
	// VerdictUnknown means there is not enough history to judge.
	VerdictUnknown Verdict = "unknown"
)

// Position is the computed price-position signal for a current price against a
// route's historical observations.
type Position struct {
	Band         Band    `json:"band"`
	Verdict      Verdict `json:"verdict"`
	Current      float64 `json:"current"`
	Low          float64 `json:"low,omitempty"`           // historical minimum
	High         float64 `json:"high,omitempty"`          // historical maximum
	Median       float64 `json:"median,omitempty"`        // historical median
	VsMedianPct  float64 `json:"vs_median_pct,omitempty"` // (current-median)/median*100; negative = below median
	Observations int     `json:"observations"`            // number of historical points used
	// Confident reports whether Observations met the confidence floor. When
	// false, Band and Verdict are Unknown and Low/High/Median are still filled
	// for transparency but carry no recommendation.
	Confident bool `json:"confident"`
}

// Compute classifies current against the historical prices using the given
// confidence floor. A floor <= 0 falls back to DefaultConfidenceFloor.
//
// history may be in any order and may contain non-positive values, which are
// ignored (a 0/negative price is never a real fare). current must be > 0 to be
// classified; a non-positive current yields an Unknown position.
func Compute(history []float64, current float64, floor int) Position {
	if floor <= 0 {
		floor = DefaultConfidenceFloor
	}

	// Filter to valid positive observations.
	prices := make([]float64, 0, len(history))
	for _, p := range history {
		if p > 0 {
			prices = append(prices, p)
		}
	}
	sort.Float64s(prices)

	pos := Position{
		Current:      current,
		Observations: len(prices),
		Band:         BandUnknown,
		Verdict:      VerdictUnknown,
	}

	if len(prices) == 0 || current <= 0 {
		return pos
	}

	pos.Low = prices[0]
	pos.High = prices[len(prices)-1]
	pos.Median = median(prices)
	if pos.Median > 0 {
		pos.VsMedianPct = (current - pos.Median) / pos.Median * 100
	}

	// Below the confidence floor: report numbers, withhold the verdict.
	if len(prices) < floor {
		return pos
	}
	pos.Confident = true

	loCut := quantile(prices, 1.0/3.0)
	hiCut := quantile(prices, 2.0/3.0)
	switch {
	case current <= loCut:
		pos.Band = BandLow
		pos.Verdict = VerdictBuy
	case current >= hiCut:
		pos.Band = BandHigh
		pos.Verdict = VerdictWait
	default:
		pos.Band = BandTypical
		pos.Verdict = VerdictNeutral
	}
	return pos
}

// median returns the median of a sorted, non-empty slice.
func median(sorted []float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

// quantile returns the value at fraction q (0..1) of a sorted, non-empty slice
// using nearest-rank interpolation.
func quantile(sorted []float64, q float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n == 1 {
		return sorted[0]
	}
	pos := q * float64(n-1)
	lo := int(pos)
	if lo >= n-1 {
		return sorted[n-1]
	}
	frac := pos - float64(lo)
	return sorted[lo]*(1-frac) + sorted[lo+1]*frac
}
