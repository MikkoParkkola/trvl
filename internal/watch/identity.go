package watch

import (
	"slices"
	"strconv"
	"strings"
)

// keySep separates fields inside a composed identity key. It is a unit
// separator so it cannot collide with anything a user can type into a city
// name, a hotel name or a room keyword.
const keySep = "\x1f"

// pollKey identifies the *thing being polled*: the route, dates and search
// parameters a provider call is made from. It deliberately excludes
// BelowPrice, because the target price is a user policy, not an input to the
// search — two watches on AMS→VLC differing only in threshold produce the
// identical provider request and must therefore cost exactly one round trip
// (#509, MULTIPRICE.2).
//
// Currency is included: providers honour the requested currency, so two
// watches asking for different currencies are genuinely different searches.
func (w Watch) pollKey() string {
	norm := func(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }

	// Sorted copies: keyword and favourite matching is order-insensitive
	// (MatchRoomKeywords requires all keywords), so two watches listing the same
	// set in a different order are the same search.
	keywords := append([]string(nil), w.RoomKeywords...)
	slices.Sort(keywords)
	favourites := append([]string(nil), w.Favourites...)
	slices.Sort(favourites)

	return strings.Join([]string{
		norm(w.Type),
		norm(w.Origin),
		norm(w.Destination),
		strings.TrimSpace(w.DepartDate),
		strings.TrimSpace(w.ReturnDate),
		strings.TrimSpace(w.DepartFrom),
		strings.TrimSpace(w.DepartTo),
		norm(w.Currency),
		norm(w.HotelName),
		norm(strings.Join(keywords, ",")),
		norm(strings.Join(favourites, ",")),
		strings.TrimSpace(w.WindowFrom),
		strings.TrimSpace(w.WindowTo),
		strconv.Itoa(w.MinScore),
		strconv.Itoa(w.MinNights),
		strconv.Itoa(w.MaxNights),
	}, keySep)
}

// dedupeKey identifies a stored watch as a *user intent*: the polled target
// plus the price threshold attached to it.
//
// Making the threshold part of the identity is the whole point of #509.
// Deduplicating on the target alone would collapse "alert me at 200" and
// "alert me at 120" into one record and silently discard the second intent;
// deduplicating on nothing lets a repeated identical request accumulate
// unbounded duplicates, which is what produced 468 watches over 4
// destinations. Threshold-aware identity keeps distinct intents (MULTIPRICE.1)
// while collapsing exact repeats (MULTIPRICE.4).
//
// Notification settings (webhook, alert thresholds, last-minute mode) are NOT
// part of the identity. A repeat request that differs only in those fields
// matches the existing watch and leaves it untouched; changing them on an
// existing watch is a separate, explicit update surface (#510).
func (w Watch) dedupeKey() string {
	return w.pollKey() + keySep + strconv.FormatFloat(w.BelowPrice, 'f', -1, 64)
}

// findByDedupeKey returns the first watch sharing key. Callers must hold the
// store transaction so the slice reflects committed on-disk state.
func findByDedupeKey(watches []Watch, key string) (Watch, bool) {
	for _, w := range watches {
		if w.dedupeKey() == key {
			return w, true
		}
	}
	return Watch{}, false
}
