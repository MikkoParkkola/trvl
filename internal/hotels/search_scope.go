package hotels

import (
	"fmt"
	"slices"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// What a search's location names, a place or one property, and how by-name
// lookups type their candidate searches (#812).

// HotelSearchScope is what a search's location string names.
type HotelSearchScope int

const (
	// SearchScopeArea: the location is a place; every provider runs.
	SearchScopeArea HotelSearchScope = iota
	// SearchScopeProperty: the location names one property (a by-name
	// lookup). Only providers that search by property name run, Google Hotels
	// and Booking.com, and no city centre is geocoded from it. City-slug sites
	// would answer 404 for a hotel name, and each 404 trips their breaker.
	SearchScopeProperty
)

// LocationKind says what a room or price lookup's Location hint holds.
type LocationKind int

const (
	// LocationArea: the hint is a place (a city, or the entity page's city).
	LocationArea LocationKind = iota
	// LocationHotelQuery: the hint is the caller's hotel query, so only the
	// text after a comma names a place.
	LocationHotelQuery
)

// locationCandidate is one search a lookup tries, with what its text names.
type locationCandidate struct {
	query string
	scope HotelSearchScope
}

// locationCandidates types buildLocationCandidates' searches. In a hotel
// query every candidate that carries the name is a property search; the comma
// suffixes, and any hint that is a place, are area searches.
func locationCandidates(hint string, kind LocationKind) []locationCandidate {
	name := strings.TrimSpace(strings.Split(hint, ",")[0])
	var out []locationCandidate
	for _, q := range buildLocationCandidates(hint) {
		scope := SearchScopeArea
		if kind == LocationHotelQuery && strings.Contains(q, name) {
			scope = SearchScopeProperty
		}
		out = append(out, locationCandidate{query: q, scope: scope})
	}
	return out
}

// googleStatusErr returns the Google Hotels failure a search recorded only in
// its provider statuses (another provider returned hotels, so the search
// itself succeeded). A rate-limited row wraps models.ErrRateLimited.
func googleStatusErr(res *models.HotelSearchResult) error {
	if res == nil {
		return nil
	}
	for _, ps := range res.ProviderStatuses {
		if ps.ID != "google_hotels" || ps.Error == "" {
			continue
		}
		if ps.Status == models.StatusRateLimited {
			return fmt.Errorf("google hotels: %s: %w", ps.Error, models.ErrRateLimited)
		}
		return fmt.Errorf("google hotels: %s", ps.Error)
	}
	return nil
}

// scopedAuxTasks keeps the auxiliary providers a scope allows. A property
// name is not a place: of the auxiliary providers only Booking.com searches it
// as free text, and its row carries the room URL the rooms lookup needs.
func scopedAuxTasks(tasks []hotelAuxTask, scope HotelSearchScope) []hotelAuxTask {
	if scope != SearchScopeProperty {
		return tasks
	}
	return slices.DeleteFunc(tasks, func(task hotelAuxTask) bool { return task.id != "booking" })
}
