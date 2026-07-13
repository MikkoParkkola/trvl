package hacks

import (
	"context"
	"math"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// DetectFunc is the detector seam used by the auto-compose savings engine.
// Production passes nil (BestSaving falls back to DetectAll); tests inject a
// deterministic detector so the savings-surfacing behaviour can be proven
// offline without any network fan-out.
type DetectFunc func(ctx context.Context, in DetectorInput) []Hack

// isMoney reports whether v is a usable, finite, strictly-positive monetary
// amount. NaN and +Inf are rejected here so a poisoned detector value can never
// reach the JSON headline (encoding/json cannot marshal non-finite floats). The
// v > 0 test already excludes NaN and negatives; the IsInf test rules out +Inf.
func isMoney(v float64) bool {
	return v > 0 && !math.IsInf(v, 0) && !math.IsNaN(v)
}

// BestSaving runs the hack detectors against the same route/date/naive-price as
// a just-completed naive search and returns the single best money-saving option,
// or nil when no detector yields a genuinely cheaper result.
//
// It is the integration point that makes savings a default outcome of flight and
// ground searches rather than a separate opt-in command. Honest by construction:
//
//   - requires a positive NaivePrice baseline and a valid route;
//   - only considers hacks with Savings > 0 (a real, lower-priced option);
//   - rejects any hack claiming to save the entire fare or more (Savings >=
//     NaivePrice) as a fabricated / mis-priced result;
//   - preserves the winning detector's Risks verbatim (hidden-city / throwaway
//     caveats are never stripped).
//
// BestSaving respects ctx cancellation; callers should pass a timeout-scoped
// context so a slow detector never blocks the naive results.
func BestSaving(ctx context.Context, in DetectorInput, detect DetectFunc) *models.HackSaving {
	if detect == nil {
		detect = DetectAll
	}
	if !isMoney(in.NaivePrice) || !in.valid() {
		return nil
	}

	found := detect(ctx, in)

	// The naive baseline (in.NaivePrice) is denominated in the requested
	// currency. A hack's Savings is denominated in that hack's own Currency.
	// Comparing or subtracting the two only tells the truth when both are the
	// same currency, so we normalise to the requested currency and drop any
	// candidate that reports a different one. Detectors are expected to emit
	// their card in in.currency(); a candidate in a foreign currency signals a
	// detector that has not been migrated to convert honestly, and surfacing it
	// would mix currencies in the headline saving. Dropping it is the honest
	// failure mode. ponytail: guard, not converter — conversion belongs in each
	// detector, so the aggregator stays simple.
	target := strings.ToUpper(strings.TrimSpace(in.currency()))
	if target == "" {
		target = "EUR"
	}

	var best *Hack
	for i := range found {
		h := &found[i]
		hc := strings.ToUpper(strings.TrimSpace(h.Currency))
		if hc == "" {
			// A blank currency is only trustworthy while EUR is the migration
			// default. If the caller asked for anything else, an unlabelled EUR
			// constant would be silently relabelled (e.g. shown as GBP), so we
			// drop it rather than lie about the denomination.
			if target != "EUR" {
				continue
			}
			hc = target
		}
		if hc != target {
			continue // foreign-currency saving cannot be honestly compared to the baseline
		}
		// Only a hack with a real, strictly-positive, finite saving represents a
		// lower price. A saving at or above the whole naive fare cannot be real,
		// and a non-finite value would poison the JSON headline, so both are
		// dropped rather than surfaced as a fabricated "free or negative" price.
		if !isMoney(h.Savings) || h.Savings >= in.NaivePrice {
			continue
		}
		// Guard the rounding boundary: a saving that rounds the resulting price to
		// zero-or-below (e.g. 100 - 99.6) is not an honest lower price. Skip it so
		// a genuinely cheaper hack can still win.
		if roundSavings(in.NaivePrice-h.Savings) <= 0 {
			continue
		}
		if best == nil || h.Savings > best.Savings {
			best = h
		}
	}
	if best == nil {
		return nil
	}

	currency := target
	price := roundSavings(in.NaivePrice - best.Savings)
	pct := math.Round(best.Savings/in.NaivePrice*1000) / 10

	s := &models.HackSaving{
		Type:        best.Type,
		Title:       best.Title,
		Description: best.Description,
		Price:       price,
		NaivePrice:  in.NaivePrice,
		Savings:     best.Savings,
		SavingsPct:  pct,
		Currency:    currency,
		Risks:       best.Risks,
		Steps:       best.Steps,
		Citations:   best.Citations,
	}
	if len(best.ConcreteCandidates) > 0 {
		s.Candidates = append([]models.FlightResult(nil), best.ConcreteCandidates...)
	}
	return s
}
