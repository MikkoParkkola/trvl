package trip

import (
	"context"
	"fmt"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// Landed-cost completion for MIK-6530 (PLANCOMP.1 / PLANCOMP.2).
//
// Two costs a traveller cannot anticipate from a flight+hotel quote — the
// airport<->city transfer and the local tourist/city tax — are folded into the
// grand total so it is a genuine no-surprise figure. Both come from small
// bundled static tables (v1, no API key, no network). The fail-fast contract
// from the ticket: when a city is not in the table we return a TYPED UNKNOWN
// (zero amount, known=false) rather than fabricate a number. Callers surface
// the unknown via the *Estimated flags on PlanSummary.
//
// Amounts are stored in EUR and converted to the summary currency by the
// caller, matching how meals/flights/hotels are normalised.

// transferCostEUR is the per-person round-trip cost of getting between the
// airport and the city centre on standard public transport, in EUR. Round trip
// because a traveller pays both ways. Curated from published 2026 fares.
var transferCostEUR = map[string]float64{
	"barcelona": 11.40, // 2x Aerobús / metro segment
	"madrid":    10.00, // 2x metro + airport supplement
	"paris":     22.60, // 2x RoissyBus ~11.30
	"amsterdam": 13.60, // 2x Schiphol train ~6.80
	"rome":      32.00, // 2x Leonardo Express ~16.00
	"london":    27.60, // 2x Elizabeth line / Heathrow
	"lisbon":    4.00,  // 2x metro ~2.00
	"berlin":    8.00,  // 2x ABC ticket ~4.00
	"prague":    5.00,  // 2x bus+metro ~2.50
	"vienna":    9.00,  // 2x City Airport Train alt / S-Bahn
}

// cityTaxPerNightEUR is the per-person, per-night tourist/city tax in EUR.
// Percentage-based regimes (Amsterdam, Berlin) use a conservative flat
// approximation so the total never over-states the surprise.
var cityTaxPerNightEUR = map[string]float64{
	"barcelona": 4.00,
	"paris":     5.20,
	"rome":      6.00,
	"venice":    5.00,
	"amsterdam": 3.50,
	"lisbon":    2.00,
	"berlin":    3.00,
	"prague":    2.00,
	"vienna":    3.20,
}

// cityTaxNightCap reflects that most EU city taxes stop charging after a fixed
// number of consecutive nights.
// ponytail: flat 7-night cap; refine per-city if a real itinerary needs it.
const cityTaxNightCap = 7

// matchCity resolves a destination (IATA code or free text) to a static-table
// key, or returns "" when no curated entry matches. It checks the resolved
// canonical name and the raw input, matching a table key as a substring so
// "Barcelona, Spain" still resolves to "barcelona".
func matchCity(dest string) string {
	candidates := []string{
		strings.ToLower(models.ResolveLocationName(dest)),
		strings.ToLower(strings.TrimSpace(dest)),
	}
	for _, c := range candidates {
		if _, ok := transferCostEUR[c]; ok {
			return c
		}
		if _, ok := cityTaxPerNightEUR[c]; ok {
			return c
		}
		for key := range transferCostEUR {
			if strings.Contains(c, key) {
				return key
			}
		}
		for key := range cityTaxPerNightEUR {
			if strings.Contains(c, key) {
				return key
			}
		}
	}
	return ""
}

// transferCost returns the total airport<->city transfer cost in EUR for the
// whole party, and whether a curated figure was found. Unknown city -> (0,
// false): a typed unknown, never a fabricated number.
func transferCost(dest string, guests int) (float64, bool) {
	if guests <= 0 {
		return 0, false
	}
	per, ok := transferCostEUR[matchCity(dest)]
	if !ok {
		return 0, false
	}
	return per * float64(guests), true
}

// cityTax returns the total tourist/city tax in EUR for the whole party over
// the stay (capped at cityTaxNightCap nights), and whether a curated figure was
// found. Unknown city -> (0, false).
func cityTax(dest string, guests, nights int) (float64, bool) {
	if guests <= 0 || nights <= 0 {
		return 0, false
	}
	per, ok := cityTaxPerNightEUR[matchCity(dest)]
	if !ok {
		return 0, false
	}
	taxed := nights
	if taxed > cityTaxNightCap {
		taxed = cityTaxNightCap
	}
	return per * float64(guests) * float64(taxed), true
}

// transferCostConverted / cityTaxConverted are the caller-facing wrappers that
// convert the EUR table figure into the summary currency.
func transferCostConverted(ctx context.Context, dest string, guests int, cur string) (float64, bool) {
	eur, known := transferCost(dest, guests)
	if !known {
		return 0, false
	}
	return convertedPlanAmount(ctx, eur, "EUR", cur), true
}

func cityTaxConverted(ctx context.Context, dest string, guests, nights int, cur string) (float64, bool) {
	eur, known := cityTax(dest, guests, nights)
	if !known {
		return 0, false
	}
	return convertedPlanAmount(ctx, eur, "EUR", cur), true
}

// budgetVerdict implements PLANCOMP.2: when a budget is set and the cheapest
// grand total exceeds it, return the explicit "no package fits" message
// carrying the cheapest total and the overage. A non-positive budget means "no
// budget set" -> never over budget. Pure and deterministic for testing.
func budgetVerdict(grandTotal, budget float64, cur string) (over bool, overage float64, msg string) {
	if budget <= 0 || grandTotal <= budget {
		return false, 0, ""
	}
	overage = grandTotal - budget
	msg = fmt.Sprintf("no package fits — cheapest is %s%.0f (%s%.0f over budget)", cur, grandTotal, cur, overage)
	return true, overage, msg
}
