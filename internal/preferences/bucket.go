package preferences

import "strings"

// MatchesBucket reports whether a city or airport is on the user's bucket list.
// "Iceland" matches Reykjavik and Keflavik. "Balkans" and "Balkan" match the
// cities a ground route to that region actually arrives in.
func MatchesBucket(city, airport string, bucket []string) bool {
	cityLow := strings.ToLower(strings.TrimSpace(city))
	airportUp := strings.ToUpper(strings.TrimSpace(airport))
	for _, raw := range bucket {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		low := strings.ToLower(raw)
		if strings.EqualFold(raw, city) || (airportUp != "" && strings.EqualFold(raw, airportUp)) {
			return true
		}
		if cityLow != "" && strings.Contains(low, cityLow) {
			return true
		}
		key := low
		if key == "balkan" {
			key = "balkans"
		}
		for _, alias := range bucketRegionCities[key] {
			if cityLow == alias || strings.EqualFold(airportUp, alias) {
				return true
			}
			if len(alias) >= 5 && strings.Contains(cityLow, alias) {
				return true
			}
		}
	}
	return false
}

// bucketRegionCities maps a dream-region name onto arrival cities and airport
// codes. The profile stores "Iceland", not "Reykjavik".
var bucketRegionCities = map[string][]string{
	"iceland": {"reykjavik", "keflavik", "kef", "rkv"},
	"balkans": {"split", "dubrovnik", "zagreb", "belgrade", "sarajevo", "sofia", "tirana", "skopje", "pristina", "podgorica"},
}
