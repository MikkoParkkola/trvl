// Package scoring computes multi-factor profile match scores for trvl results.
//
// ProfileMatch replaces the old two-factor ValueScore with a weighted sum
// across ≥ 12 factors that cover all major preference dimensions:
// budget fit, loyalty, time windows, routing, geography, and travel style.
//
// The score is in [0, 100] (integer). 0 is also returned when a hard-exclusion
// rule fires (e.g. a destination in the user's excluded list). Each call also
// returns a per-factor breakdown (map[string]float64 with values in [0,1]) so
// the user can see exactly why a trip scored 73 instead of 91.
package scoring

import (
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/preferences"
)

// Factor name constants for breakdown keys and weight maps.
const (
	FactorBudgetFit                 = "budget_fit"
	FactorLoyaltyEarn               = "loyalty_earn"
	FactorTimeWindowFit             = "time_window_fit"
	FactorDirectness                = "directness"
	FactorDistrictMatch             = "district_match"
	FactorAirportAffinity           = "airport_affinity"
	FactorEarlyConnectionCompliance = "early_connection_compliance"
	FactorStatusRetention           = "status_retention"
	FactorLoungeAtTransit           = "lounge_at_transit"
	FactorBucketListBoost           = "bucket_list_boost"
	FactorWarsawFilter              = "warsaw_filter"
	FactorFamilyModeCompatibility   = "family_mode_compatibility"
)

// DefaultWeights returns sensible default match weights.
// Values are relative importance; they do not need to sum to any fixed total.
// The final score is the weighted average of per-factor scores scaled to 0–100.
func DefaultWeights() map[string]float64 {
	return map[string]float64{
		FactorBudgetFit:                 25.0,
		FactorLoyaltyEarn:               12.0,
		FactorTimeWindowFit:             8.0,
		FactorDirectness:                10.0,
		FactorDistrictMatch:             8.0,
		FactorAirportAffinity:           8.0,
		FactorEarlyConnectionCompliance: 5.0,
		FactorStatusRetention:           7.0,
		FactorLoungeAtTransit:           4.0,
		FactorBucketListBoost:           8.0,
		FactorFamilyModeCompatibility:   5.0,
		// FactorWarsawFilter is a hard exclusion gate; it has no weight in the
		// weighted-sum path and is applied before the weighted sum.
	}
}

// DiscoverInput is the per-result data available when scoring a discover result.
// Fields that are unknown (e.g. no detailed flight leg data in explore-mode)
// may be left at their zero values; factor scorers handle zero gracefully by
// returning a neutral 0.5.
type DiscoverInput struct {
	// Destination identifiers.
	AirportCode string // IATA code of destination airport, e.g. "BCN"
	CityName    string // Human-readable city name, e.g. "Barcelona"

	// Trip economics.
	FlightPrice float64 // one-way fare component in user's currency
	HotelPrice  float64 // total hotel cost for the stay
	Total       float64 // FlightPrice + HotelPrice
	Budget      float64 // user's declared maximum

	// Hotel quality.
	HotelRating float64 // 0–10 scale (0 = unknown)
	HotelName   string

	// Optional flight detail (populated when calling from flights/hotels views).
	Stops        int      // number of stops (0 = direct)
	DepartTime   string   // "HH:MM" 24h format; "" if unknown
	AirlineCodes []string // IATA airline codes for all legs; nil if unknown
}

// ComputeProfileMatch scores a single discover/flight/hotel result against the
// user's full preference profile.
//
// Returns:
//   - score:     0–100 (int). 0 when a hard-exclusion rule fires.
//   - breakdown: per-factor raw score in [0,1] for the --explain output.
func ComputeProfileMatch(prefs *preferences.Preferences, input DiscoverInput) (score int, breakdown map[string]float64) {
	breakdown = make(map[string]float64, 12)

	if prefs == nil {
		prefs = preferences.Default()
	}

	// Resolve effective weights (user-tunable via preferences.json).
	weights := DefaultWeights()
	for k, v := range prefs.MatchWeights {
		if v >= 0 {
			weights[k] = v
		}
	}

	// ── Hard-exclusion gate ────────────────────────────────────────────────
	if isExcluded(prefs, input.AirportCode, input.CityName) {
		breakdown[FactorWarsawFilter] = 0.0
		return 0, breakdown
	}
	breakdown[FactorWarsawFilter] = 1.0 // destination passes the exclusion filter

	// ── Per-factor scores ─────────────────────────────────────────────────
	breakdown[FactorBudgetFit] = scoreBudgetFit(input)
	breakdown[FactorLoyaltyEarn] = scoreLoyaltyEarn(prefs, input)
	breakdown[FactorTimeWindowFit] = scoreTimeWindowFit(prefs, input)
	breakdown[FactorDirectness] = scoreDirectness(prefs, input)
	breakdown[FactorDistrictMatch] = scoreDistrictMatch(prefs, input)
	breakdown[FactorAirportAffinity] = scoreAirportAffinity(prefs, input)
	breakdown[FactorEarlyConnectionCompliance] = scoreEarlyConnectionCompliance(prefs, input)
	breakdown[FactorStatusRetention] = scoreStatusRetention(prefs, input)
	breakdown[FactorLoungeAtTransit] = scoreLoungeAtTransit(prefs, input)
	breakdown[FactorBucketListBoost] = scoreBucketListBoost(prefs, input)
	breakdown[FactorFamilyModeCompatibility] = scoreFamilyModeCompatibility(prefs, input)

	// ── Weighted average → 0–100 ─────────────────────────────────────────
	var weightedSum, totalWeight float64
	for factor, w := range weights {
		if s, ok := breakdown[factor]; ok {
			weightedSum += w * s
			totalWeight += w
		}
	}
	if totalWeight == 0 {
		return 50, breakdown
	}
	raw := weightedSum / totalWeight * 100.0
	if raw < 0 {
		raw = 0
	}
	if raw > 100 {
		raw = 100
	}
	return int(raw + 0.5), breakdown // round to nearest integer
}

// ── Factor implementations ────────────────────────────────────────────────────

// scoreBudgetFit: how much budget headroom remains (0 at budget, 1 when free).
func scoreBudgetFit(input DiscoverInput) float64 {
	if input.Budget <= 0 {
		return 0.5
	}
	fit := 1.0 - (input.Total / input.Budget)
	if fit < 0 {
		return 0
	}
	if fit > 1 {
		return 1
	}
	return fit
}

// scoreLoyaltyEarn: does the flight accrue miles in a preferred programme?
// 1.0 if at least one leg is on a loyalty airline; 0.5 neutral if unknown.
func scoreLoyaltyEarn(prefs *preferences.Preferences, input DiscoverInput) float64 {
	if len(prefs.LoyaltyAirlines) == 0 && len(prefs.FrequentFlyerPrograms) == 0 {
		return 0.5
	}
	if len(input.AirlineCodes) == 0 {
		return 0.5 // unknown — neutral
	}
	for _, code := range input.AirlineCodes {
		for _, la := range prefs.LoyaltyAirlines {
			if strings.EqualFold(code, la) {
				return 1.0
			}
		}
		for _, ffp := range prefs.FrequentFlyerPrograms {
			if strings.EqualFold(code, ffp.AirlineCode) {
				return 1.0
			}
		}
	}
	return 0.2
}

// scoreTimeWindowFit: does departure time fall within the user's preferred window?
// Returns 0.5 (neutral) when DepartTime is unknown.
func scoreTimeWindowFit(prefs *preferences.Preferences, input DiscoverInput) float64 {
	if input.DepartTime == "" {
		return 0.5
	}
	earliest := prefs.FlightTimeEarliest
	latest := prefs.FlightTimeLatest
	if earliest == "" && latest == "" {
		return 0.5
	}
	dep := parseHHMM(input.DepartTime)
	if dep < 0 {
		return 0.5
	}
	// Red-eye check: if departure is very early (before 06:00) and user doesn't want red-eye, penalise.
	if dep < 360 && !prefs.RedEyeOK { // 06:00 = 360 minutes
		return 0.1
	}
	if earliest != "" {
		e := parseHHMM(earliest)
		if e >= 0 && dep < e {
			return 0.3
		}
	}
	if latest != "" {
		l := parseHHMM(latest)
		if l >= 0 && dep > l {
			return 0.2
		}
	}
	return 1.0
}

// scoreDirectness: prefer direct flights when prefs.PreferDirect is set.
// Returns 0.5 (neutral) when stop count is unknown (Stops == 0 and input is
// from explore-mode which doesn't provide stop details).
func scoreDirectness(prefs *preferences.Preferences, input DiscoverInput) float64 {
	if !prefs.PreferDirect {
		return 0.5 // no preference
	}
	if len(input.AirlineCodes) == 0 {
		return 0.5 // unknown routing from explore-mode
	}
	if input.Stops == 0 {
		return 1.0
	}
	return 1.0 / (1.0 + float64(input.Stops)) // 0.5 for 1 stop, 0.33 for 2, etc.
}

// scoreDistrictMatch: is the hotel in a preferred district for this city?
func scoreDistrictMatch(prefs *preferences.Preferences, input DiscoverInput) float64 {
	city := input.CityName
	if city == "" || input.HotelName == "" {
		return 0.5
	}
	districts := prefs.DistrictsFor(city)
	if len(districts) == 0 {
		return 0.5
	}
	hotelLower := strings.ToLower(input.HotelName)
	for _, d := range districts {
		if strings.Contains(hotelLower, strings.ToLower(d)) {
			return 1.0
		}
	}
	return 0.3
}

// scoreAirportAffinity: user's auto-learned affinity for a destination airport.
func scoreAirportAffinity(prefs *preferences.Preferences, input DiscoverInput) float64 {
	if len(prefs.AirportAffinity) == 0 {
		return 0.5
	}
	if v, ok := prefs.AirportAffinity[strings.ToUpper(input.AirportCode)]; ok {
		if v < 0 {
			return 0
		}
		if v > 1 {
			return 1
		}
		return v
	}
	return 0.5
}

// scoreEarlyConnectionCompliance: penalise very early morning connections that
// require pre-dawn travel from home when the user hasn't opted in.
func scoreEarlyConnectionCompliance(prefs *preferences.Preferences, input DiscoverInput) float64 {
	if input.DepartTime == "" {
		return 0.5
	}
	dep := parseHHMM(input.DepartTime)
	if dep < 0 {
		return 0.5
	}
	if dep < 300 && !prefs.RedEyeOK { // before 05:00
		return 0.0
	}
	if dep < 360 && !prefs.RedEyeOK { // 05:00–06:00
		return 0.3
	}
	return 1.0
}

// scoreStatusRetention: does the trip help maintain elite status via miles?
// High score when the airline is a status partner and user has a programme near
// renewal. Without detailed flight data, returns neutral 0.5.
func scoreStatusRetention(prefs *preferences.Preferences, input DiscoverInput) float64 {
	if len(prefs.FrequentFlyerPrograms) == 0 {
		return 0.5
	}
	if len(input.AirlineCodes) == 0 {
		return 0.5
	}
	for _, ffp := range prefs.FrequentFlyerPrograms {
		for _, code := range input.AirlineCodes {
			if strings.EqualFold(code, ffp.AirlineCode) {
				// If they have miles balance, a qualifying trip is more valuable.
				if ffp.MilesBalance > 0 {
					return 1.0
				}
				return 0.8
			}
		}
	}
	return 0.2
}

// scoreLoungeAtTransit: does the user have lounge access at the transit airport?
// Without detailed routing, returns neutral 0.5. With routing, returns 1.0 if
// the user has any lounge card and there are transit airports.
func scoreLoungeAtTransit(prefs *preferences.Preferences, input DiscoverInput) float64 {
	if len(prefs.LoungeCards) == 0 {
		return 0.5
	}
	// Direct flight: lounge access is irrelevant at transit (there is none).
	if len(input.AirlineCodes) > 0 && input.Stops == 0 {
		return 0.5
	}
	if len(input.AirlineCodes) == 0 {
		return 0.5 // unknown
	}
	// Has stops + has lounge card → good
	return 0.8
}

// scoreBucketListBoost: is the destination on the user's bucket list?
func scoreBucketListBoost(prefs *preferences.Preferences, input DiscoverInput) float64 {
	if len(prefs.BucketList) == 0 {
		return 0.5
	}
	city := strings.ToLower(input.CityName)
	airport := strings.ToUpper(input.AirportCode)
	for _, item := range prefs.BucketList {
		item = strings.TrimSpace(item)
		if strings.EqualFold(item, city) || strings.EqualFold(item, airport) ||
			strings.Contains(strings.ToLower(item), city) {
			return 1.0
		}
	}
	return 0.5
}

// scoreFamilyModeCompatibility: is the trip suitable for the user's travel party?
// Penalises party-heavy trips for solo travellers and ensures family-suitable
// hotels when travelling with dependants.
func scoreFamilyModeCompatibility(prefs *preferences.Preferences, input DiscoverInput) float64 {
	if prefs.DefaultCompanions == 0 {
		// Solo traveller: no-dormitory and en-suite checks already handled by filters.
		return 0.5
	}
	// Group travel: prefer higher-rated hotels.
	if input.HotelRating >= 8.0 {
		return 1.0
	}
	if input.HotelRating >= 7.0 {
		return 0.7
	}
	if input.HotelRating > 0 {
		return 0.4
	}
	return 0.5
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// isExcluded returns true if the destination matches any entry in
// prefs.ExcludedDestinations (case-insensitive, matches airport code or city).
func isExcluded(prefs *preferences.Preferences, airportCode, cityName string) bool {
	for _, excl := range prefs.ExcludedDestinations {
		excl = strings.TrimSpace(excl)
		if strings.EqualFold(excl, airportCode) ||
			strings.EqualFold(excl, cityName) ||
			(cityName != "" && strings.Contains(strings.ToLower(cityName), strings.ToLower(excl))) {
			return true
		}
	}
	return false
}

// parseHHMM converts a "HH:MM" string to minutes-since-midnight.
// Returns -1 on parse failure.
func parseHHMM(s string) int {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return -1
	}
	return t.Hour()*60 + t.Minute()
}
