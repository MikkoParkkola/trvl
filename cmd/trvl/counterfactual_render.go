package main

import (
	"fmt"
	"io"
	"time"

	"github.com/MikkoParkkola/trvl/internal/counterfactual"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// printSavings renders counterfactual savings in three honest buckets:
//   - Tier 0 (call-free, not probe-derived): genuine by-products of data already
//     in hand — "no extra searches".
//   - Tier 1 (call-free but probe-derived): pre-computed by the background watch
//     monitor — free to read now, but they DID cost provider calls earlier, so
//     they are labelled as such rather than hidden among the true by-products.
//   - Tier 2 (not call-free): a deeper search that issued extra provider calls
//     just now.
//
// Each line carries an "as of" age when its data is not from this moment
// (TRVL.CF.5 honesty). The point is to never imply a fan-out that did not happen
// nor hide one that did — including one that happened in the background.
func printSavings(w io.Writer, savings []counterfactual.Saving, now time.Time) {
	if len(savings) == 0 {
		return
	}
	var free, precomputed, probed []counterfactual.Saving
	for _, s := range savings {
		switch {
		case s.CallFree && s.Kind == counterfactual.KindProbe:
			precomputed = append(precomputed, s)
		case s.CallFree:
			free = append(free, s)
		default:
			probed = append(probed, s)
		}
	}
	printSavingsSection(w, "\nSavings you could capture (no extra searches):", free, now)
	printSavingsSection(w, "\nPre-computed by your watch monitor (no extra searches now):", precomputed, now)
	printSavingsSection(w, "\nFound by a deeper search (extra provider calls):", probed, now)
}

func printSavingsSection(w io.Writer, header string, savings []counterfactual.Saving, now time.Time) {
	if len(savings) == 0 {
		return
	}
	fmt.Fprintln(w, header)
	for _, s := range savings {
		fmt.Fprintf(w, "  • %s%s\n", s.Description, asOfSuffix(s.AsOf, now))
	}
}

// asOfSuffix returns a human "(as of N ago)" marker when the data is at least an
// hour old, so a persisted-grid saving is never silently presented as live.
func asOfSuffix(asOf, now time.Time) string {
	if asOf.IsZero() {
		return ""
	}
	age := now.Sub(asOf)
	if age < time.Hour {
		return ""
	}
	switch {
	case age < 24*time.Hour:
		return fmt.Sprintf(" (as of %dh ago)", int(age.Hours()))
	default:
		return fmt.Sprintf(" (as of %dd ago)", int(age.Hours())/24)
	}
}

// cheapestFlightPrice returns the lowest positive fare in a result set, or 0.
func cheapestFlightPrice(flights []models.FlightResult) float64 {
	var best float64
	for _, f := range flights {
		if f.Price > 0 && (best == 0 || f.Price < best) {
			best = f.Price
		}
	}
	return best
}
