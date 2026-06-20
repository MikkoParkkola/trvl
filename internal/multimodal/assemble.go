package multimodal

import (
	"sort"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// estimatePlaceholder is the leg returned when a real provider could not price a
// leg. It carries no price yet (Price 0); assembleItinerary fills an indicative
// figure from the route's Rome2Rio range and keeps Estimated=true.
func estimatePlaceholder(spec LegSpec) PricedLeg {
	return PricedLeg{
		Mode:      spec.Mode,
		From:      spec.From,
		To:        spec.To,
		Estimated: true,
		Provider:  "rome2rio (estimate)",
	}
}

// legSpecsForRoute turns a discovered route into the ordered list of legs to
// price. Per-leg endpoints come from the GroundLeg stops when present; the first
// leg's origin defaults to the overall from and the last leg's destination to
// the overall to, so a chain whose intermediate hubs are unknown still prices
// its outer legs and estimates the middle.
func legSpecsForRoute(route models.GroundRoute, from, to, date string) []LegSpec {
	if len(route.Legs) == 0 {
		// Single-mode route with no leg breakdown: one leg end-to-end.
		mode := route.Type
		if mode == "" {
			mode = "mixed"
		}
		return []LegSpec{{Mode: mode, From: from, To: to, Date: date}}
	}

	specs := make([]LegSpec, len(route.Legs))
	for i, leg := range route.Legs {
		f := leg.Departure.City
		t := leg.Arrival.City
		if i == 0 && f == "" {
			f = from
		}
		if i == len(route.Legs)-1 && t == "" {
			t = to
		}
		specs[i] = LegSpec{Mode: leg.Type, From: f, To: t, Date: date}
	}
	return specs
}

// assembleItinerary sums priced legs into one end-to-end itinerary. It is pure
// (no I/O) so the composition, estimate-fallback and labelling logic is unit
// tested deterministically.
//
// Currency discipline (mirrors flights round-trip composition): legs are summed
// only in a single currency. The headline currency is the first real priced
// leg's currency, falling back to the route's indicative currency. A real leg in
// a different currency cannot be summed honestly, so it is demoted to an
// estimate with a warning rather than fabricating a converted total.
//
// Estimate fallback: any leg that could not be priced (or was demoted) shares
// the route's indicative remainder — max(0, indicativeLow - realSum) split
// across the unpriced legs. The whole itinerary is then flagged Estimated.
//
// Returns ok=false when the route yields neither a real nor an indicative total
// (nothing honest to show).
func assembleItinerary(route models.GroundRoute, from, to, date string, legs []PricedLeg) (Itinerary, bool) {
	currency := headlineCurrency(legs, route.Currency)

	realSum := 0.0
	unpriced := 0
	for i := range legs {
		l := &legs[i]
		if !l.Estimated && l.Price > 0 && l.Currency == currency {
			realSum += l.Price
			continue
		}
		if !l.Estimated && l.Price > 0 && l.Currency != currency {
			// Real fare in a non-matching currency: cannot be summed honestly.
			l.Estimated = true
			l.Detail = strings.TrimSpace(l.Detail + " (priced in " + l.Currency + "; not summed)")
		}
		l.Estimated = true
		unpriced++
	}

	// Distribute the indicative remainder across unpriced legs.
	if unpriced > 0 {
		remainder := route.Price - realSum // route.Price is the indicative low
		if remainder < 0 {
			remainder = 0
		}
		per := 0.0
		if remainder > 0 {
			per = round2(remainder / float64(unpriced))
		}
		for i := range legs {
			if legs[i].Estimated {
				legs[i].Price = per
				if legs[i].Currency == "" {
					legs[i].Currency = currency
				}
				if legs[i].Provider == "" {
					legs[i].Provider = "rome2rio (estimate)"
				}
			}
		}
	}

	total := 0.0
	estimated := false
	durSum := 0
	durKnown := true
	var warnings, risks []string
	for i := range legs {
		l := legs[i]
		total += l.Price
		if l.Estimated {
			estimated = true
		}
		if l.DurationMin > 0 {
			durSum += l.DurationMin
		} else {
			durKnown = false
		}
	}
	total = round2(total)

	// Nothing honest to show: no real legs and no indicative figure.
	if total <= 0 {
		return Itinerary{}, false
	}

	if estimated {
		warnings = append(warnings, "total includes an estimate: one or more legs use Rome2Rio's indicative price, not a confirmed fare — verify before booking")
	}
	if route.Type != "" {
		// Preserve discovery risk caveats if the route carried any amenity/notes.
		risks = append(risks, route.Amenities...)
	}

	duration := route.Duration
	if durKnown && durSum > 0 {
		duration = durSum
	}

	transfers := 0
	if len(legs) > 1 {
		transfers = len(legs) - 1
	}

	return Itinerary{
		From:        from,
		To:          to,
		Date:        date,
		Legs:        legs,
		TotalPrice:  total,
		Currency:    currency,
		DurationMin: duration,
		Transfers:   transfers,
		ModeChain:   modeChain(legs),
		Estimated:   estimated,
		Source:      "rome2rio",
		BookingURL:  route.BookingURL,
		Warnings:    warnings,
		Risks:       risks,
	}, true
}

// headlineCurrency picks the currency in which legs are summed: the first real
// priced leg's currency, else the route's indicative currency, else the first
// leg that has any currency, else EUR.
func headlineCurrency(legs []PricedLeg, routeCurrency string) string {
	for _, l := range legs {
		if !l.Estimated && l.Price > 0 && l.Currency != "" {
			return l.Currency
		}
	}
	if routeCurrency != "" {
		return routeCurrency
	}
	for _, l := range legs {
		if l.Currency != "" {
			return l.Currency
		}
	}
	return "EUR"
}

// modeChain renders the ordered mode sequence, e.g. "ferry → fly".
func modeChain(legs []PricedLeg) string {
	parts := make([]string, 0, len(legs))
	for _, l := range legs {
		m := l.Mode
		if m == "" {
			m = "mixed"
		}
		if len(parts) == 0 || parts[len(parts)-1] != m {
			parts = append(parts, m)
		}
	}
	return strings.Join(parts, " → ")
}

// rankItineraries sorts cheapest true-total first. Fully-priced itineraries beat
// estimated ones on a tie (a confirmed total is worth more than an indicative
// one), and shorter duration breaks remaining ties.
func rankItineraries(its []Itinerary) {
	sort.SliceStable(its, func(i, j int) bool {
		a, b := its[i], its[j]
		if a.TotalPrice != b.TotalPrice {
			return a.TotalPrice < b.TotalPrice
		}
		if a.Estimated != b.Estimated {
			return !a.Estimated // non-estimated first
		}
		return a.DurationMin < b.DurationMin
	})
}

func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}
