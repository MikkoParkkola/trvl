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
// parameters a provider call is made from. It excludes BelowPrice, because the
// target price is a user policy, not an input to the search.
//
// Since identity is now target-level (see dedupeKey), a target carries at most
// one watch, so pollKey no longer collapses several watches onto one call. It
// still earns its place: pollcache keys on it, so concurrent checks of the same
// target wait for the one in-flight provider call instead of racing to issue
// their own.
//
// Currency is included: providers honour the requested currency, so two
// requests asking for different currencies are genuinely different searches and
// must not share a call.
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
func (w Watch) targetKey(withCurrency bool) string {
	norm := func(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }

	// Normalise BEFORE sorting, de-duplicate, and join with the unit separator
	// rather than a comma.
	//
	// All three matter, and the original order got each of them wrong:
	//   - sorting raw values put ["king","Balcony"] and ["KING","balcony"] in
	//     different orders, so two identical searches produced different keys
	//     and were polled twice;
	//   - a comma join let the single value "KRK,PRG" collide with the two
	//     values ["KRK","PRG"], so two different intents deduplicated into one;
	//   - without de-duplication ["king","king"] differed from ["king"].
	// Found by GPT second-opinion review, 2026-08-02.
	normSet := func(in []string) string {
		seen := make(map[string]struct{}, len(in))
		out := make([]string, 0, len(in))
		for _, v := range in {
			v = norm(v)
			if v == "" {
				continue
			}
			if _, dup := seen[v]; dup {
				continue
			}
			seen[v] = struct{}{}
			out = append(out, v)
		}
		slices.Sort(out)
		return strings.Join(out, keySep)
	}
	keywords := normSet(w.RoomKeywords)
	favourites := normSet(w.Favourites)

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
		keywords,
		favourites,
		strings.TrimSpace(w.WindowFrom),
		strings.TrimSpace(w.WindowTo),
		strconv.Itoa(w.MinScore),
		strconv.Itoa(w.MinNights),
		strconv.Itoa(w.MaxNights),
	}, keySep)
}

// dedupeKey identifies a stored watch as a *user intent*: the polled target.
//
// The price threshold is NOT part of the identity. Re-watching a route with a
// different target price ADJUSTS the existing watch rather than adding a second
// one, so a user ends up with one watch per route and re-running the watch
// command is how they change their mind about the price. Operator decision,
// 2026-08-02.
//
// This deliberately reverses #509's threshold-aware identity, which made
// "alert me at 200" and "alert me at 120" two coexisting records. That design
// was chosen to stop a repeated request silently discarding the second intent;
// the duplicate-accumulation bug it was really fixing (468 watches over 4
// destinations) is addressed by target-level dedupe just as well, and without
// the surprise of a settings-only re-watch forking a rival record. The cost is
// real and accepted: there is no way to hold two price targets on one route.
//
// Currency is excluded (targetKey(false)) even though pollKey includes it: a
// re-watch in a new currency is the same intent re-expressed and must MATCH the
// stored watch, so Add can migrate it through applyIntent — resetting the
// currency-denominated fields and purging old-currency history — instead of
// forking a rival watch that would then be polled and alerted independently.
//
// Notification settings (webhook, alert thresholds, last-minute mode) are NOT
// part of the identity either. applyIntent decides what a re-watch may
// overwrite; notably a re-watch that OMITS a setting must not delete it.
func (w Watch) dedupeKey() string {
	return w.targetKey(false)
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
