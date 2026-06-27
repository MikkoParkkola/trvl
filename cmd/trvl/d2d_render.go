package main

import (
	"fmt"
	"io"

	"github.com/MikkoParkkola/trvl/internal/d2d"
	"github.com/MikkoParkkola/trvl/internal/multimodal"
)

// printDoorToDoor renders the MIK-6231 honest door-to-door total for one
// multimodal itinerary: a confirmed total that EXCLUDES indicative legs, the
// indicative portion surfaced separately, and a confidence band rather than a
// false-precise single figure.
func printDoorToDoor(w io.Writer, it multimodal.Itinerary) {
	total := d2d.Compute(itineraryToLegs(it))
	if total.Counts.Confirmed == 0 && total.Counts.Indicative == 0 && total.Counts.Unverified == 0 {
		return
	}
	_, _ = fmt.Fprintf(w, "     Door-to-door: %s %.2f confirmed", total.Currency, total.ConfirmedTotal)
	if sum, n := legSum(total.IndicativeLegs); n > 0 {
		_, _ = fmt.Fprintf(w, " + %s %.2f indicative across %d leg(s) — verify before booking", total.Currency, sum, n)
	}
	if sum, n := legSum(total.UnverifiedLegs); n > 0 {
		_, _ = fmt.Fprintf(w, " + %s %.2f unverified across %d leg(s)", total.Currency, sum, n)
	}
	if total.MixedCurrency {
		_, _ = fmt.Fprintf(w, " (mixed currencies: %d leg(s) excluded)", len(total.ExcludedCurrencyLegs))
	}
	_, _ = fmt.Fprintf(w, " · band %s [%.0f–%.0f]\n", total.Band, total.BandLow, total.BandHigh)
}

func itineraryToLegs(it multimodal.Itinerary) []d2d.Leg {
	legs := make([]d2d.Leg, len(it.Legs))
	for i, l := range it.Legs {
		v := d2d.Confirmed
		if l.Estimated {
			v = d2d.Indicative
		}
		legs[i] = d2d.Leg{
			Mode:         l.Mode,
			From:         l.From,
			To:           l.To,
			Price:        l.Price,
			Currency:     l.Currency,
			Source:       l.Provider,
			Verification: v,
		}
	}
	return legs
}

func legSum(legs []d2d.Leg) (float64, int) {
	var sum float64
	for _, l := range legs {
		sum += l.Price
	}
	return sum, len(legs)
}
