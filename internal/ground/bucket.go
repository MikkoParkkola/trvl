package ground

import (
	"sort"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/preferences"
)

// PreferBucket orders routes whose arrival city matches the user's bucket list
// ahead of routes that do not. Price order inside each group is kept. Routes
// travelling in different directions stay in their original order, so a
// round-trip's outbound and inbound legs are not shuffled together.
func PreferBucket(routes []models.GroundRoute, bucket []string) {
	if len(routes) < 2 || len(bucket) == 0 {
		return
	}
	sort.SliceStable(routes, func(i, j int) bool {
		if routes[i].Direction != routes[j].Direction {
			return false
		}
		return preferences.MatchesBucket(routes[i].Arrival.City, "", bucket) &&
			!preferences.MatchesBucket(routes[j].Arrival.City, "", bucket)
	})
}
