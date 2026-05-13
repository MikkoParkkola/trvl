// Package railpass computes the break-even of a rail pass (Eurail /
// Interrail / Swiss Travel Pass / JR Pass) against the equivalent
// stack of point-to-point segment prices on the same itinerary. Given
// a slice of priced PointToPointSegment fixtures and a PassOption,
// EvaluatePass returns a recommendation with the gap, the activation
// breakeven count, and the cheaper choice.
//
// MIK-3086 (partial). Pure function — no I/O — so the math stays
// trivially auditable. Live segment pricing (DB Bahn / Trenitalia /
// Renfe / Eurostar) and the open-jaw composer ship as separate
// changes against the same surface.
package railpass

import (
	"fmt"
	"sort"
)

// PointToPointSegment is one priced rail leg the user would buy
// without a pass. Operator is informational; ReservationFee captures
// the supplement that some operators (Eurostar, Thello, Frecciarossa
// in 1st class) require even when travelling on a pass — a pass user
// pays the reservation fee on top of pass activation, so it must be
// carried into the pass cost too.
type PointToPointSegment struct {
	Operator       string
	Origin         string
	Destination    string
	Date           string  // ISO 8601
	Price          float64 // walk-up / flexible / refundable price in user currency
	ReservationFee float64 // mandatory supplement when travelling on a rail pass
	YouthEligible  bool    // operator honours the youth-pass age band on this leg
}

// PassOption describes one rail pass the user is evaluating. Cost is
// the activation price including any youth / senior discount the
// caller already applied. Days is the number of activation days the
// pass covers — Interrail global passes range 4 to 15 days within a
// 1- or 2-month window. Caller is responsible for matching segment
// dates to those activation days; this package assumes every supplied
// segment is in scope.
type PassOption struct {
	Name                 string  // e.g. "Interrail Global 7-in-1month"
	Cost                 float64 // pass activation price
	Days                 int     // number of travel days the pass covers
	ReservationsIncluded bool    // when true, segment reservation fees are included in Cost
}

// Recommendation is the verdict for one user-supplied scenario.
type Recommendation struct {
	PassName           string
	PointToPointTotal  float64
	PassTotalEffective float64 // pass cost + sum of mandatory reservation fees (if not included)
	Savings            float64 // p2p_total - pass_effective; negative when pass is more expensive
	BreakEvenSegments  int     // segments needed before the pass starts paying off, given current avg price
	SegmentsScored     int
	Verdict            Verdict
	Reason             string
}

// Verdict labels the high-level recommendation so callers can render
// distinct messages.
type Verdict string

const (
	// VerdictBuyPass — pass is cheaper given the supplied segments.
	VerdictBuyPass Verdict = "buy_pass"
	// VerdictSkipPass — point-to-point stack wins.
	VerdictSkipPass Verdict = "skip_pass"
	// VerdictMarginal — within 10%% — buy/skip is a coin-flip and the
	// caller should weight on flexibility / change-fee tolerance
	// rather than headline price.
	VerdictMarginal Verdict = "marginal"
)

// EvaluatePass scores `pass` against the supplied `segments` and
// returns a Recommendation. Segments with non-positive price are
// skipped silently. Returns a zero-value Recommendation when no
// usable segments remain so the caller never has to special-case
// "what if I have no priced legs".
func EvaluatePass(pass PassOption, segments []PointToPointSegment) Recommendation {
	scored := make([]PointToPointSegment, 0, len(segments))
	var p2pTotal, mandatoryFees float64
	for _, s := range segments {
		if s.Price <= 0 {
			continue
		}
		p2pTotal += s.Price
		if !pass.ReservationsIncluded {
			mandatoryFees += s.ReservationFee
		}
		scored = append(scored, s)
	}
	if len(scored) == 0 {
		return Recommendation{
			PassName: pass.Name,
			Reason:   "no priced segments supplied; cannot evaluate",
		}
	}
	passEffective := pass.Cost + mandatoryFees
	savings := p2pTotal - passEffective
	avgPrice := p2pTotal / float64(len(scored))
	breakEven := 0
	if avgPrice > 0 {
		raw := pass.Cost / avgPrice
		breakEven = int(raw)
		if raw-float64(breakEven) > 0 {
			breakEven++
		}
	}

	verdict, reason := classify(savings, p2pTotal, passEffective)

	return Recommendation{
		PassName:           pass.Name,
		PointToPointTotal:  p2pTotal,
		PassTotalEffective: passEffective,
		Savings:            savings,
		BreakEvenSegments:  breakEven,
		SegmentsScored:     len(scored),
		Verdict:            verdict,
		Reason:             reason,
	}
}

func classify(savings, p2p, pass float64) (Verdict, string) {
	if p2p == 0 {
		return VerdictSkipPass, "no point-to-point baseline; cannot recommend a pass"
	}
	gapPct := savings / p2p * 100
	switch {
	case savings >= 0 && gapPct >= 10:
		return VerdictBuyPass, fmt.Sprintf("pass cheaper by %.2f (%.1f%% of p2p); buy", savings, gapPct)
	case savings <= 0 && gapPct <= -10:
		return VerdictSkipPass, fmt.Sprintf("p2p cheaper by %.2f (%.1f%% under pass); skip", -savings, -gapPct)
	default:
		return VerdictMarginal, fmt.Sprintf("within 10%% (%.2f gap); decide on flexibility, not price", savings)
	}
}

// EvaluateAll scores every pass against the same segment set and
// sorts cheapest-effective-cost first. Useful for "which pass to
// buy" UIs that present a small grid of options. Empty input yields
// an empty slice rather than nil so range-loops are safe.
func EvaluateAll(passes []PassOption, segments []PointToPointSegment) []Recommendation {
	out := make([]Recommendation, 0, len(passes))
	for _, p := range passes {
		out = append(out, EvaluatePass(p, segments))
	}
	sort.SliceStable(out, func(i, j int) bool {
		// Pass options that fail to evaluate (zero pass total) sort
		// last so the actionable rows surface first.
		if out[i].PassTotalEffective == 0 {
			return false
		}
		if out[j].PassTotalEffective == 0 {
			return true
		}
		return out[i].PassTotalEffective < out[j].PassTotalEffective
	})
	return out
}
