package preferences

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// MatchesBucket reports whether a city or airport is on the user's bucket list.
// "Iceland" matches Reykjavik and Keflavik. "Balkans" and "Balkan" match the
// cities a ground route to that region actually arrives in.
func MatchesBucket(city, airport string, bucket []string) bool {
	cityKey := fold(city)
	airportUp := strings.ToUpper(strings.TrimSpace(airport))
	for _, raw := range bucket {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		low := fold(raw)
		if (cityKey != "" && cityKey == low) || (airportUp != "" && strings.EqualFold(raw, airportUp)) {
			return true
		}
		if cityKey != "" && strings.Contains(low, cityKey) {
			return true
		}
		key := low
		if key == "balkan" {
			key = "balkans"
		}
		for _, alias := range bucketRegionCities[key] {
			if cityKey == alias || strings.EqualFold(airportUp, alias) {
				return true
			}
			if len(alias) >= 5 && strings.Contains(cityKey, alias) {
				return true
			}
		}
	}
	return false
}

// fold lowercases and strips accents so "Reykjavík" matches the ASCII alias.
func fold(s string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(strings.ToLower(strings.TrimSpace(s))) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// bucketRegionCities maps a dream-region name onto arrival cities and airport
// codes. The profile stores "Iceland", not "Reykjavik".
var bucketRegionCities = map[string][]string{
	"iceland": {"reykjavik", "keflavik", "kef", "rkv"},
	"balkans": {"split", "dubrovnik", "zagreb", "belgrade", "sarajevo", "sofia", "tirana", "skopje", "pristina", "podgorica"},
}
