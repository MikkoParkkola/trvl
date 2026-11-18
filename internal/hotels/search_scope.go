package hotels

import (
	"slices"
	"strings"
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
// query only the text after a comma names a place; every other candidate
// carries the hotel name and is a property search. A hint that is a place
// keeps every candidate an area search.
func locationCandidates(hint string, kind LocationKind) []locationCandidate {
	places := map[string]bool{}
	parts := strings.Split(hint, ",")
	for i := 1; i < len(parts); i++ {
		places[strings.TrimSpace(strings.Join(parts[i:], ","))] = true
	}
	var out []locationCandidate
	for _, q := range buildLocationCandidates(hint) {
		scope := SearchScopeArea
		if kind == LocationHotelQuery && !places[q] {
			scope = SearchScopeProperty
		}
		out = append(out, locationCandidate{query: q, scope: scope})
	}
	return out
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

// queryLocality is the place a by-name query names: the text after its first
// comma, or, without a comma, the whole query.
func queryLocality(query string) string {
	if i := strings.Index(query, ","); i >= 0 {
		return strings.TrimSpace(query[i+1:])
	}
	return strings.TrimSpace(query)
}

// hasLocality reports whether query carries every word of locality, matched
// case-folded on whole words, so an answer for "Hotel Sacher" alone cannot
// stand in for Vienna (#826 rev 6 [2]).
func hasLocality(query, locality string) bool {
	words := map[string]bool{}
	for _, w := range hotelNameWords(query) {
		words[w] = true
	}
	want := hotelNameWords(locality)
	for _, w := range want {
		if !words[w] {
			return false
		}
	}
	return len(want) > 0
}
