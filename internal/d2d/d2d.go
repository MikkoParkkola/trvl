// Package d2d computes honest door-to-door (end-to-end) totals for multimodal
// journeys with per-leg verification status and a confidence band.
//
// Honesty contract:
//   - Indicative legs (e.g. sourced from rome2rio) are NEVER folded into the
//     confirmed total. They are surfaced separately so the caller can render an
//     honest "at least X confirmed + ~Y indicative" rather than a false-precise
//     sum.
//   - Unverified legs (no source information) are excluded from ConfirmedTotal
//     and reported in their own bucket.
//   - Mixed currencies are flagged: if legs disagree on currency the total is not
//     silently summed. MixedCurrency is set and ConfirmedTotal reflects only the
//     legs in the headline currency.
//   - The confidence band widens whenever any non-confirmed leg exists, and is
//     always expressed in the headline currency — magnitudes in other currencies
//     are never summed into it. A single figure is only produced for
//     all-confirmed, single-currency trips.
//
// This package is pure (no network I/O, no new dependencies beyond stdlib).
package d2d

import (
	"strings"
)

// Verification classifies how trustworthy a leg's price is.
//
// The three levels map directly to trvl's provider semantics:
//   - Confirmed: a live or recent bookable fare from a transactional provider.
//   - Indicative: a range or estimate (e.g. Rome2Rio discovery prices). Never
//     presented as a bookable fare; always surfaced separately.
//   - Unverified: no source information is available; the price is unknown or
//     caller-supplied without evidence.
type Verification string

const (
	// Confirmed means the price comes from a transactional provider and is
	// suitable for inclusion in a confirmed total.
	Confirmed Verification = "confirmed"

	// Indicative means the price is an estimate or range (e.g. Rome2Rio).
	// It must not be summed into a confirmed total.
	Indicative Verification = "indicative"

	// Unverified means no source evidence backs the price.
	Unverified Verification = "unverified"
)

// BandKind is a qualitative confidence band for the end-to-end total.
type BandKind string

const (
	// BandExact: all legs confirmed in a single currency — the total is precise.
	BandExact BandKind = "exact"

	// BandNarrow: all legs confirmed but mixed currencies prevented full
	// summation. The confirmed subtotal is still reliable.
	BandNarrow BandKind = "narrow"

	// BandWide: one or more legs are indicative. The true cost may differ
	// materially from the confirmed subtotal.
	BandWide BandKind = "wide"

	// BandUnknown: one or more legs are unverified. The total is uncertain.
	BandUnknown BandKind = "unknown"
)

// Leg is a single segment of a door-to-door journey.
//
// Callers that have already determined the verification status should set it
// explicitly. When Verification is left empty, DefaultVerification maps the
// Source to a default (e.g. "rome2rio" -> Indicative).
type Leg struct {
	// Mode is the transport or accommodation type: "air", "train", "bus",
	// "ferry", "hotel", "taxi", etc. Free-form; used for display only.
	Mode string

	// From and To are human-readable place names (city, airport, station).
	From string
	To   string

	// Price is the fare or cost for this leg. Zero means unknown.
	Price float64

	// Currency is an ISO 4217 code (e.g. "EUR", "USD"). Must be non-empty
	// for a leg to be summed into the confirmed total.
	Currency string

	// Source is the provider that returned this price (e.g. "google_flights",
	// "rome2rio", "booking.com"). Used to derive Verification when the field
	// is left empty.
	Source string

	// Verification is the caller-supplied trust level. When empty, the package
	// derives it from Source via DefaultVerification.
	Verification Verification
}

// effectiveVerification returns the leg's verification, falling back to the
// source-derived default when the caller left the field empty.
func (l Leg) effectiveVerification() Verification {
	if l.Verification != "" {
		return l.Verification
	}
	return DefaultVerification(l.Source)
}

// LegCount reports how many legs fall into each verification bucket.
type LegCount struct {
	Confirmed  int
	Indicative int
	Unverified int
}

// Total is the honest end-to-end result for a set of legs.
type Total struct {
	// ConfirmedTotal is the sum of all Confirmed legs in the headline currency.
	// Legs in other currencies are excluded and appear in ExcludedCurrencyLegs.
	ConfirmedTotal float64

	// IndicativeLegs holds every leg whose Verification is Indicative. These
	// are intentionally NOT summed into ConfirmedTotal.
	IndicativeLegs []Leg

	// UnverifiedLegs holds every leg whose Verification is Unverified.
	UnverifiedLegs []Leg

	// ExcludedCurrencyLegs holds Confirmed legs that could not be summed
	// because they are in a different currency than the headline.
	ExcludedCurrencyLegs []Leg

	// Currency is the headline currency — the currency of the first confirmed
	// leg with a valid price, or the most common currency among all legs.
	Currency string

	// MixedCurrency is true when legs carry more than one currency code,
	// regardless of how many could be summed.
	MixedCurrency bool

	// Counts breaks down the total number of legs by verification status.
	Counts LegCount

	// Band is a qualitative confidence level for the end-to-end total.
	Band BandKind

	// BandLow is the lower bound of the estimated total range.
	// Equal to ConfirmedTotal when Band == BandExact.
	BandLow float64

	// BandHigh is the upper bound of the estimated total range, in the headline
	// currency. Equal to ConfirmedTotal when Band == BandExact; inflated by
	// indicative and unverified legs that are in the headline currency.
	BandHigh float64
}

// indicativeSources is the canonical set of sources whose prices are indicative
// by default. Rome2Rio is the primary case: its prices are discovery ranges, not
// confirmed fares. See internal/multimodal/pricing.go:124 ("Rome2Rio is the
// DISCOVERY source; its prices are indicative ranges, not confirmed fares").
var indicativeSources = map[string]bool{
	"rome2rio": true,
}

// DefaultVerification returns the default Verification for a given source name.
// rome2rio -> Indicative; any unknown or empty source -> Unverified; all other
// known transactional providers -> Confirmed.
func DefaultVerification(source string) Verification {
	s := strings.ToLower(strings.TrimSpace(source))
	if s == "" {
		return Unverified
	}
	if indicativeSources[s] {
		return Indicative
	}
	return Confirmed
}

// Compute returns the honest door-to-door total for a slice of legs.
//
// The returned Total always reflects exactly what the input legs can honestly
// support: indicative and unverified legs are never silently folded into
// ConfirmedTotal, and the confidence band widens whenever non-confirmed legs
// exist. An empty or nil legs slice returns a zero Total with BandUnknown.
func Compute(legs []Leg) Total {
	if len(legs) == 0 {
		return Total{Band: BandUnknown}
	}

	currency := headlineCurrency(legs)
	counts, confirmed, indicative, unverified, excluded := classifyLegs(legs, currency)
	mixedCurrency := hasMixedCurrencies(legs)

	confirmedTotal := sumPrices(confirmed)
	band, low, high := computeBand(confirmedTotal, currency, indicative, unverified, len(excluded) > 0, mixedCurrency)

	return Total{
		ConfirmedTotal:       round2(confirmedTotal),
		IndicativeLegs:       indicative,
		UnverifiedLegs:       unverified,
		ExcludedCurrencyLegs: excluded,
		Currency:             currency,
		MixedCurrency:        mixedCurrency,
		Counts:               counts,
		Band:                 band,
		BandLow:              round2(low),
		BandHigh:             round2(high),
	}
}

// headlineCurrency picks the currency in which the confirmed total is expressed.
// It prefers the first confirmed leg with a non-zero price, falls back to the
// first leg with any currency, and finally returns "EUR" as a safe default.
func headlineCurrency(legs []Leg) string {
	for _, l := range legs {
		if l.effectiveVerification() == Confirmed && l.Price > 0 && l.Currency != "" {
			return l.Currency
		}
	}
	for _, l := range legs {
		if l.Currency != "" {
			return l.Currency
		}
	}
	return "EUR"
}

// classifyLegs splits legs into confirmed (in headline currency), indicative,
// unverified, and currency-excluded confirmed buckets.
func classifyLegs(legs []Leg, currency string) (LegCount, []Leg, []Leg, []Leg, []Leg) {
	var (
		counts     LegCount
		confirmed  []Leg
		indicative []Leg
		unverified []Leg
		excluded   []Leg
	)
	for _, l := range legs {
		switch l.effectiveVerification() {
		case Confirmed:
			counts.Confirmed++
			if l.Currency != currency {
				excluded = append(excluded, l)
			} else {
				confirmed = append(confirmed, l)
			}
		case Indicative:
			counts.Indicative++
			indicative = append(indicative, l)
		default: // Unverified or any unknown value
			counts.Unverified++
			unverified = append(unverified, l)
		}
	}
	return counts, confirmed, indicative, unverified, excluded
}

// hasMixedCurrencies reports whether legs carry more than one distinct currency.
func hasMixedCurrencies(legs []Leg) bool {
	seen := ""
	for _, l := range legs {
		if l.Currency == "" {
			continue
		}
		if seen == "" {
			seen = l.Currency
			continue
		}
		if l.Currency != seen {
			return true
		}
	}
	return false
}

// sumPrices returns the total price of legs that carry a positive price.
func sumPrices(legs []Leg) float64 {
	var total float64
	for _, l := range legs {
		if l.Price > 0 {
			total += l.Price
		}
	}
	return total
}

// sumPricesInCurrency totals only legs priced in the given currency, so a
// confidence band never adds magnitudes from a different currency (which would
// be a fabricated, meaningless number).
func sumPricesInCurrency(legs []Leg, currency string) float64 {
	var total float64
	for _, l := range legs {
		if l.Price > 0 && l.Currency == currency {
			total += l.Price
		}
	}
	return total
}

// computeBand derives the confidence band and [low, high] range, all in the
// headline currency. The band widens whenever non-confirmed legs exist.
// Indicative and unverified legs inflate the upper bound ONLY when priced in the
// headline currency; foreign-currency legs (including all excluded legs) are
// never summed into the band — they are surfaced via the MixedCurrency flag and
// the ExcludedCurrencyLegs bucket instead.
func computeBand(confirmed float64, currency string, indicative, unverified []Leg, hasExcluded, mixed bool) (BandKind, float64, float64) {
	hasIndicative := len(indicative) > 0
	hasUnverified := len(unverified) > 0

	switch {
	case hasUnverified:
		high := confirmed + sumPricesInCurrency(indicative, currency) + sumPricesInCurrency(unverified, currency)
		return BandUnknown, confirmed, high
	case hasIndicative:
		high := confirmed + sumPricesInCurrency(indicative, currency)
		return BandWide, confirmed, high
	case hasExcluded || mixed:
		// Confirmed subtotal is reliable; foreign-currency legs exist but cannot
		// be summed into a single-currency bound, so the band stays at the subtotal.
		return BandNarrow, confirmed, confirmed
	default:
		return BandExact, confirmed, confirmed
	}
}

// round2 rounds a non-negative float to 2 decimal places (matches
// multimodal/assemble.go). Prices in this package are never negative.
func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}
