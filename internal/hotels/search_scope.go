package hotels

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
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

// googleRefusal returns a failed search's error as a rate-limit error when it
// is refusal-class (429, cooldown, a bot block), and nil for any other
// failure. Only a refusal may replace a recorded Google outcome: another
// failure is not a Google answer and must never erase one (#813).
func googleRefusal(err error) error {
	if err == nil || models.ClassifyProviderError(err) != models.StatusRateLimited {
		return nil
	}
	if errors.Is(err, models.ErrRateLimited) {
		return err
	}
	return fmt.Errorf("%w: %w", models.ErrRateLimited, err)
}

// googleCooldown reports whether a Google refusal armed the client's cooldown:
// an HTTP 429 or a quota body (worded "refused (HTTP n)" wherever the hotels
// package reports one) or a call skipped inside the cooldown
// (batchexec.ErrRefused). A 403 block or a 503 outage is refusal-class too but
// arms none, so its message must not send anyone to wait one out. Matched on
// text as well, because a provider status keeps only the error's message.
func googleCooldown(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, batchexec.ErrRefused) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "refused (HTTP ") || strings.Contains(msg, batchexec.ErrRefused.Error())
}
