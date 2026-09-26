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
	// Rank inside each direction, writing the result back into that direction's
	// own slots. A comparator that treats different directions as equal is not
	// a valid order, and it can leave a matching outbound stuck behind another.
	slots := map[string][]int{}
	var dirs []string
	for i, route := range routes {
		if _, ok := slots[route.Direction]; !ok {
			dirs = append(dirs, route.Direction)
		}
		slots[route.Direction] = append(slots[route.Direction], i)
	}
	next := make([]models.GroundRoute, len(routes))
	for _, dir := range dirs {
		idxs := slots[dir]
		group := make([]models.GroundRoute, len(idxs))
		for j, i := range idxs {
			group[j] = routes[i]
		}
		sort.SliceStable(group, func(a, b int) bool {
			return preferences.MatchesBucket(group[a].Arrival.City, "", bucket) &&
				!preferences.MatchesBucket(group[b].Arrival.City, "", bucket)
		})
		for j, i := range idxs {
			next[i] = group[j]
		}
	}
	copy(routes, next)
}
