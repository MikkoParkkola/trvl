// Package dailyspend provides a coarse, offline per-destination estimate of a
// traveller's daily on-the-ground spend (meals, local transport, incidentals).
//
// trvl can price flights and hotels from live providers, but there is no live
// cost-of-living source under the project's "no new providers / no API key by
// default" lock. Without a meals figure the landed-cost total silently omits a
// real, unavoidable cost and under-quotes the trip. This package closes that
// gap honestly: a bundled static cost-tier index (no key, no network), with
// every estimate explicitly tagged so it is never presented as a live quote.
//
// The index is deliberately coarse — four cost tiers, a per-destination tier
// map, and a labelled fallback — rather than fabricated per-city precision.
package dailyspend

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed data.json
var rawData []byte

// Estimate is a per-person, per-day on-the-ground spend estimate.
type Estimate struct {
	// PerPersonPerDay is the estimated daily spend per traveller, in Currency.
	PerPersonPerDay float64
	Currency        string
	// Tier is the cost bucket used (budget|moderate|expensive|premium).
	Tier string
	// Fallback is true when the destination was not found and the default tier
	// was used. Callers should surface this so the figure is not mistaken for a
	// destination-specific value.
	Fallback bool
	// Source describes the basis of the estimate; surface it alongside the
	// figure so the user knows it is an estimate, not a live quote.
	Source string
}

// Total returns the estimated meals/local spend for the whole party over the
// stay: PerPersonPerDay * guests * nights. Non-positive guests or nights yield
// zero (a same-day or party-less plan has no daily spend to add).
func (e Estimate) Total(guests, nights int) float64 {
	if guests <= 0 || nights <= 0 {
		return 0
	}
	return e.PerPersonPerDay * float64(guests) * float64(nights)
}

type index struct {
	Currency    string             `json:"currency"`
	Source      string             `json:"source"`
	Tiers       map[string]float64 `json:"tiers"`
	DefaultTier string             `json:"default_tier"`
	Cities      map[string]string  `json:"cities"`
	Countries   map[string]string  `json:"countries"`
}

var idx = mustLoad()

func mustLoad() index {
	var i index
	if err := json.Unmarshal(rawData, &i); err != nil {
		// The dataset is embedded at build time; a parse failure is a build
		// bug, not a runtime condition. Fail loud so it is caught in tests.
		panic(fmt.Sprintf("dailyspend: embedded data.json is invalid: %v", err))
	}
	if _, ok := i.Tiers[i.DefaultTier]; !ok {
		panic(fmt.Sprintf("dailyspend: default_tier %q missing from tiers", i.DefaultTier))
	}
	return i
}

// Lookup returns the daily-spend estimate for a destination. The location may
// be a city name (preferred) or a country; matching is case-insensitive and
// ignores surrounding whitespace. Unknown destinations return the default tier
// with Fallback=true. The returned estimate always carries a Source string.
func Lookup(location string) Estimate {
	key := strings.ToLower(strings.TrimSpace(location))

	if tier, ok := idx.Cities[key]; ok {
		return idx.estimate(tier, false)
	}
	if tier, ok := idx.Countries[key]; ok {
		return idx.estimate(tier, false)
	}
	// A "City, Country" string falls back to the country half when the city is
	// not mapped — common for smaller destinations.
	if i := strings.LastIndex(key, ","); i >= 0 {
		if tier, ok := idx.Countries[strings.TrimSpace(key[i+1:])]; ok {
			return idx.estimate(tier, false)
		}
	}
	return idx.estimate(idx.DefaultTier, true)
}

func (i index) estimate(tier string, fallback bool) Estimate {
	amount, ok := i.Tiers[tier]
	if !ok {
		// Mapped to a tier that does not exist — treat as default. mustLoad
		// guarantees the default tier resolves.
		tier = i.DefaultTier
		amount = i.Tiers[tier]
		fallback = true
	}
	return Estimate{
		PerPersonPerDay: amount,
		Currency:        i.Currency,
		Tier:            tier,
		Fallback:        fallback,
		Source:          i.Source,
	}
}
