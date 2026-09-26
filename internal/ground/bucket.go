package ground

import (
	"sort"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// PreferBucket orders routes whose arrival city matches the user's bucket list
// ahead of routes that do not. Matching is a case-insensitive substring either
// way. Price order among matches, and among non-matches, is kept.
func PreferBucket(routes []models.GroundRoute, bucket []string) {
	if len(routes) < 2 || len(bucket) == 0 {
		return
	}
	sort.SliceStable(routes, func(i, j int) bool {
		return bucketMatch(routes[i], bucket) && !bucketMatch(routes[j], bucket)
	})
}

func bucketMatch(route models.GroundRoute, bucket []string) bool {
	city := strings.ToLower(strings.TrimSpace(route.Arrival.City))
	if city == "" {
		return false
	}
	for _, want := range bucket {
		want = strings.ToLower(strings.TrimSpace(want))
		if want == "" {
			continue
		}
		if strings.Contains(city, want) || strings.Contains(want, city) {
			return true
		}
		for _, alias := range bucketAliases[want] {
			if city == alias || strings.Contains(city, alias) {
				return true
			}
		}
	}
	return false
}

// bucketAliases maps a dream-destination name onto arrival cities a ground
// route actually uses. The profile stores "Iceland", not "Reykjavik".
var bucketAliases = map[string][]string{
	"iceland": {"reykjavik", "keflavik"},
	"balkans": {"split", "dubrovnik", "zagreb", "belgrade", "sarajevo", "sofia", "tirana", "skopje", "pristina", "podgorica"},
}
