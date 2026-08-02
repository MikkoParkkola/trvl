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
// watches asking for different currencies are genuinely different searches and
// must not be collapsed into one provider call.
func (w Watch) pollKey() string {
	return w.targetKey(true)
}

// targetKey builds the identity of a watch's target. withCurrency selects
// between the two jobs this key does, which are NOT the same job:
//
//   - Polling (withCurrency=true, see pollKey): currency changes the provider
//     request, so two currencies are two searches and must not share a call.
//   - Deduplication (withCurrency=false, see dedupeKey): a re-watch in a new
//     currency is the same user intent re-expressed, not a rival watch. It must
//     match the stored record so applyIntent can migrate it — resetting the
//     currency-denominated fields and purging history that is denominated in
//     the old currency.
//
// Collapsing these into one key forces a choice between polling a currency
// change incorrectly and forking a duplicate watch on every currency change.
// Keeping them separate is what lets #509's threshold-aware identity and the
// round-18/28 currency-migration behaviour both hold.
func (w Watch) targetKey(withCurrency bool) string {
	norm := func(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }

	// Sorted copies: keyword and favourite matching is order-insensitive
	// (MatchRoomKeywords requires all keywords), so two watches listing the same
	// set in a different order are the same search.
	keywords := append([]string(nil), w.RoomKeywords...)
	slices.Sort(keywords)
	favourites := append([]string(nil), w.Favourites...)
	slices.Sort(favourites)

	currency := ""
	if withCurrency {
		currency = norm(w.Currency)
	}

	return strings.Join([]string{
		norm(w.Type),
		norm(w.Origin),
		norm(w.Destination),
		strings.TrimSpace(w.DepartDate),
		strings.TrimSpace(w.ReturnDate),
		strings.TrimSpace(w.DepartFrom),
		strings.TrimSpace(w.DepartTo),
		currency,
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
// Currency is excluded (targetKey(false)) even though pollKey includes it: a
// re-watch in a new currency is the same intent re-expressed and must MATCH the
// stored watch, so Add can migrate it through applyIntent — resetting the
// currency-denominated fields and purging old-currency history — instead of
// forking a rival watch that would then be polled and alerted independently.
//
// Notification settings (webhook, alert thresholds, last-minute mode) are NOT
// part of the identity. A repeat request that differs only in those fields
// matches the existing watch; applyIntent decides what it may overwrite.
func (w Watch) dedupeKey() string {
	return w.targetKey(false) + keySep + strconv.FormatFloat(w.BelowPrice, 'f', -1, 64)
}

// findByDedupeKeyIndex returns the index of the first watch sharing key, or -1.
// Callers must hold the store transaction so the slice reflects committed
// on-disk state. The index (not a copy) is what lets Add mutate the stored
// record in place via applyIntent.
func findByDedupeKeyIndex(watches []Watch, key string) int {
	for i := range watches {
		if watches[i].dedupeKey() == key {
			return i
		}
	}
	return -1
}

// findByTargetIndex returns the index of the first watch on the same target,
// ignoring the price threshold, or -1.
//
// This is the "omitted, not zero" rule. Threshold-aware identity (#509) is
// right when the caller NAMES a price: "alert me at 200" and "alert me at 120"
// are two intents. It is wrong when the caller names no price at all — a
// re-watch that only changes the currency or a notification setting carries
// BelowPrice == 0 as an absence, not as a request for a zero-price alert.
// Treating that absence as a distinct identity forks a duplicate watch on every
// settings-only re-watch, which is the exact duplicate-accumulation bug #509
// existed to stop.
func findByTargetIndex(watches []Watch, target string) int {
	for i := range watches {
		if watches[i].targetKey(false) == target {
			return i
		}
	}
	return -1
}
