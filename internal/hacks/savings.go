package hacks

import (
	"context"
	"math"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// DetectFunc is the detector seam used by the auto-compose savings engine.
// Production passes nil (BestSaving falls back to DetectAll); tests inject a
// deterministic detector so the savings-surfacing behaviour can be proven
// offline without any network fan-out.
type DetectFunc func(ctx context.Context, in DetectorInput) []Hack

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
	if in.NaivePrice <= 0 || !in.valid() {
		return nil
	}

	found := detect(ctx, in)

	var best *Hack
	for i := range found {
		h := &found[i]
		// Only a hack with a real, strictly-positive saving represents a lower
		// price. A saving at or above the whole naive fare cannot be real, so we
		// drop it rather than surface a fabricated "free or negative" price.
		if h.Savings <= 0 || h.Savings >= in.NaivePrice {
			continue
		}
		if best == nil || h.Savings > best.Savings {
			best = h
		}
	}
	if best == nil {
		return nil
	}

	currency := best.Currency
	if currency == "" {
		currency = in.currency()
	}
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
