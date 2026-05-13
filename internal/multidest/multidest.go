// Package multidest implements the two-stage 'screen then drill-down'
// strategy described in mental-model section 3 for trips spanning 3+
// cities: enumerate orderings, screen each on flight-only price, take
// the top K, then drill down with hotel pricing. Pure functions —
// caller supplies the priced flight + hotel fixtures (in production
// the existing flights.SearchFlights / hotels providers are expected
// to feed this), and the ranker returns top bundles ordered by total
// trip cost.
//
// MIK-3080 (partial). Live pricing wiring + the optimize_multi_city
// handler refactor compose on top of this in a follow-up so the math
// stays trivially testable.
package multidest

import (
	"sort"
	"strings"
)

// Leg is one priced flight hop in a candidate ordering. Origin and
// Destination are IATA codes; the caller supplies the slice in the
// order the traveller would fly.
type Leg struct {
	Origin      string
	Destination string
	Date        string
	Price       float64
	Carrier     string
}

// HotelCost is one priced hotel stay along the candidate ordering.
// City matches the Destination of the inbound Leg whose stay it
// represents; the ranker matches by case-insensitive City equality.
type HotelCost struct {
	City       string
	CheckIn    string
	CheckOut   string
	Provider   string
	TotalPrice float64
}

// Ordering is one candidate city sequence with its priced legs and
// the hotel costs at each non-final city. The flight-only screen
// uses the sum of leg prices; the drill-down adds the hotel total.
type Ordering struct {
	Cities []string // human-readable ordered list, e.g. ["AMS", "ROM", "BCN"]
	Legs   []Leg
	Hotels []HotelCost
}

// Bundle is the final ranked output: one ordering with its computed
// totals and a reasoning string the UI can render.
type Bundle struct {
	Cities      []string
	FlightTotal float64
	HotelTotal  float64
	GrandTotal  float64
	Legs        []Leg
	Hotels      []HotelCost
	Reason      string
	Rank        int // 1-based position in the returned slice
}

// ScreenOptions tune the two-stage cut.
type ScreenOptions struct {
	// TopKAfterScreen is how many cheapest orderings (by flight-only
	// total) survive into the drill-down stage. Defaults to 3 when
	// zero — matches the AC's 'top 3 then refine'.
	TopKAfterScreen int
	// TopKFinal clips the drill-down output. Defaults to 3 when zero.
	TopKFinal int
}

// Screen ranks orderings by flight-only total ascending and returns
// the top K. Pure function. Orderings missing legs are skipped so the
// caller does not have to pre-clean.
func Screen(orderings []Ordering, opt ScreenOptions) []Ordering {
	if len(orderings) == 0 {
		return nil
	}
	if opt.TopKAfterScreen == 0 {
		opt.TopKAfterScreen = 3
	}
	usable := make([]Ordering, 0, len(orderings))
	for _, o := range orderings {
		if len(o.Legs) == 0 {
			continue
		}
		usable = append(usable, o)
	}
	if len(usable) == 0 {
		return nil
	}
	sort.SliceStable(usable, func(i, j int) bool {
		return flightTotal(usable[i]) < flightTotal(usable[j])
	})
	if opt.TopKAfterScreen < len(usable) {
		usable = usable[:opt.TopKAfterScreen]
	}
	return usable
}

// DrillDown takes screened orderings, adds hotel costs, sorts by
// grand total, and returns the top-K bundles. Each bundle records
// flight + hotel split so the UI can show the user where the savings
// land. Reason is generated last so it can reference the rank.
func DrillDown(screened []Ordering, opt ScreenOptions) []Bundle {
	if len(screened) == 0 {
		return nil
	}
	if opt.TopKFinal == 0 {
		opt.TopKFinal = 3
	}
	bundles := make([]Bundle, 0, len(screened))
	for _, o := range screened {
		f := flightTotal(o)
		h := hotelTotal(o)
		bundles = append(bundles, Bundle{
			Cities:      append([]string(nil), o.Cities...),
			FlightTotal: f,
			HotelTotal:  h,
			GrandTotal:  f + h,
			Legs:        o.Legs,
			Hotels:      o.Hotels,
		})
	}
	sort.SliceStable(bundles, func(i, j int) bool {
		return bundles[i].GrandTotal < bundles[j].GrandTotal
	})
	if opt.TopKFinal < len(bundles) {
		bundles = bundles[:opt.TopKFinal]
	}
	for i := range bundles {
		bundles[i].Rank = i + 1
		bundles[i].Reason = describeBundle(bundles[i], bundles[0].GrandTotal)
	}
	return bundles
}

// ScreenAndDrillDown is the convenience composition most callers
// want: screen the full enumeration then drill the survivors with
// hotel prices. Returns nil when the input is empty.
func ScreenAndDrillDown(orderings []Ordering, opt ScreenOptions) []Bundle {
	return DrillDown(Screen(orderings, opt), opt)
}

func flightTotal(o Ordering) float64 {
	var sum float64
	for _, l := range o.Legs {
		sum += l.Price
	}
	return sum
}

func hotelTotal(o Ordering) float64 {
	var sum float64
	for _, h := range o.Hotels {
		sum += h.TotalPrice
	}
	return sum
}

func describeBundle(b Bundle, cheapest float64) string {
	cities := strings.Join(b.Cities, " -> ")
	if cities == "" {
		cities = "ordering"
	}
	if b.GrandTotal <= cheapest {
		return cities + " — cheapest bundle in the screened set"
	}
	gap := b.GrandTotal - cheapest
	gapPct := gap / cheapest * 100
	return cities + " — " + formatGap(gap, gapPct) + " more than the cheapest"
}

func formatGap(gap, gapPct float64) string {
	whole := int(gap + 0.5)
	out := intToString(whole) + " ("
	pctWhole := int(gapPct + 0.5)
	out += intToString(pctWhole) + "%)"
	return out
}

func intToString(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	buf := make([]byte, 0, 8)
	for i > 0 {
		buf = append([]byte{byte('0' + i%10)}, buf...)
		i /= 10
	}
	if neg {
		return "-" + string(buf)
	}
	return string(buf)
}
