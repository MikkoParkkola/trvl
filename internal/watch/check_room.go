package watch

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func checkRoom(ctx context.Context, store *Store, checker RoomChecker, w Watch) CheckResult {
	return checkRoomWithWebhookContext(ctx, ctx, store, checker, w)
}

func checkRoomWithWebhookContext(checkCtx, webhookCtx context.Context, store *Store, checker RoomChecker, w Watch) CheckResult {
	checkCtx, webhookCtx = normalizeCheckAndWebhookContexts(checkCtx, webhookCtx)

	// See checkOneWithWebhookContext: poll identity captured before the
	// provider call, so a result for a re-targeted watch is discarded.
	pollKey := w.pollKey()

	matches, err := checker.CheckRooms(checkCtx, w)
	if err != nil {
		return CheckResult{Watch: w, Error: err}
	}

	result := CheckResult{
		Watch:       w,
		RoomMatches: matches,
		RoomFound:   len(matches) > 0,
	}

	// Hoisted out of the `if len(matches) > 0` block below: the persist
	// section after that block needs to know whether a currency change
	// happened this poll, to purge this watch's (now stale-currency) history
	// before recording the new-currency observation.
	var matchedRoom string

	// If matches found, record the cheapest matching room price.
	if len(matches) > 0 {
		// Round 19 found the original approach -- pick the raw numeric
		// minimum across ALL matches, THEN reject if that one happens to
		// have garbage currency -- discards a real, valid-currency sibling
		// whenever the single cheapest-by-price entry is the malformed one,
		// stranding the watch on a provider's repeated bad quote instead of
		// just ignoring it. Filter garbage-currency matches out first, then
		// take the minimum among what remains; only error when every match
		// is garbage. Found by GPT second-opinion review, 2026-07-30
		// (round 19).
		type roomCandidate struct {
			match RoomMatch
			raw   string
		}
		usable := make([]roomCandidate, 0, len(matches))
		var firstGarbageRaw string
		for _, m := range matches {
			raw := m.Currency
			// Same provider-case risk as the flight path
			// (checkOneWithWebhookContext): canonicalize before any
			// comparison or persisted assignment. Found by adversarial
			// review, 2026-07-30 (round 18).
			m.Currency = strings.ToUpper(strings.TrimSpace(m.Currency))
			if m.Price > 0 && m.Currency == "" && raw != "" {
				if firstGarbageRaw == "" {
					firstGarbageRaw = raw
				}
				continue
			}
			// Round 20 found a non-empty, non-whitespace value that still
			// isn't a real currency code (e.g. "EU R") slipped past this
			// filter -- it is neither "" nor whitespace-garbage, so it was
			// classified as usable and could win selection outright. Reject
			// it the same way as whitespace-garbage. Found by GPT
			// second-opinion review, 2026-07-30 (round 20).
			if m.Price > 0 && m.Currency != "" && !IsValidCurrencyFormat(m.Currency) {
				if firstGarbageRaw == "" {
					firstGarbageRaw = raw
				}
				continue
			}
			usable = append(usable, roomCandidate{match: m, raw: raw})
		}
		if len(usable) == 0 {
			// Same whitespace-garbage risk as the flight path: a non-empty
			// but unusable currency must not be silently treated as "no
			// currency provided," which would bypass the mismatch guard
			// below entirely. Only reached here when EVERY match was
			// garbage. Found by GPT second-opinion review, 2026-07-30
			// (round 18/19).
			return CheckResult{Watch: w, Error: fmt.Errorf("checker returned unusable currency %q", firstGarbageRaw)}
		}
		// Round 20 found the previous "cheapest across all usable matches"
		// selection was order-dependent whenever usable held matches with
		// DIFFERENT valid currencies (e.g. a USD watch quoted [180 EUR, 180
		// USD]): the raw minimum treated the two magnitudes as comparable
		// numbers, so which one won depended on provider ordering alone --
		// EUR-first triggered the destructive currency-change branch below,
		// USD-first produced a genuine same-currency price-drop alert for
		// the identical input set. Select deterministically instead:
		//   1. the cheapest match in the watch's OWN currency, if any exist
		//      (never compares across currencies to pick this).
		//   2. else the cheapest match carrying ANY known currency (this is
		//      either the watch's first-ever observation or a genuine
		//      currency-change candidate -- both handled by the
		//      currencyMismatch logic below, unaffected by this change).
		//   3. else (every usable match lacks a currency entirely) the
		//      cheapest of those, preserving the pre-round-20 behavior for a
		//      provider that never returns currency for this route.
		// Step 2 also fixes a second round-20 finding: a genuinely
		// currency-absent match could previously win over a valid-currency
		// sibling (e.g. {100, ""} hiding {120, "USD"}) purely because it was
		// numerically cheaper; absent-currency matches are now only chosen
		// when nothing else qualifies. Found by GPT second-opinion review,
		// 2026-07-30 (round 20).
		selectCheapest := func(pool []roomCandidate) RoomMatch {
			var best RoomMatch
			for _, c := range pool {
				if c.match.Price > 0 && (best.Price == 0 || c.match.Price < best.Price) {
					best = c.match
				}
			}
			return best
		}
		var cheapest RoomMatch
		if w.Currency != "" {
			var sameCurrency []roomCandidate
			for _, c := range usable {
				if c.match.Currency == w.Currency {
					sameCurrency = append(sameCurrency, c)
				}
			}
			cheapest = selectCheapest(sameCurrency)
		}
		if cheapest.Price == 0 {
			// Round 21 found this "any known currency" tier still compared
			// magnitudes across DIFFERENT currencies via selectCheapest's raw
			// numeric minimum -- exactly the bug class this whole selection
			// ladder exists to eliminate, just moved one tier down: a USD
			// watch quoted [180 EUR, 100 JPY] with no USD match selected JPY
			// purely because 100 < 180, not because JPY was actually
			// cheaper. Group by currency instead and pick within the single
			// largest group -- never comparing magnitudes across currencies
			// to choose a winner -- tie-broken by lexicographically smallest
			// currency code so the result is deterministic regardless of
			// provider order. Found by GPT second-opinion review, 2026-07-30
			// (round 21).
			//
			// Round 24 found zero/negative-price rows were still being
			// grouped here even though selectCheapest can never choose one as
			// a winner (it requires Price > 0): a currency whose only usable
			// entries were zero-price could still win the largest-group
			// tie-break purely on row count, leaving cheapest.Price == 0
			// after this tier and falling through to tier 3, which compares
			// raw price magnitudes across ALL usable rows regardless of
			// currency -- reopening the exact cross-currency comparison bug
			// rounds 20/21 closed. Require Price > 0 to enter a group so
			// group size (and thus which currency wins) reflects only rows
			// that could actually win selection. Found by GPT second-opinion
			// review, 2026-07-31 (round 24).
			byCurrency := map[string][]roomCandidate{}
			for _, c := range usable {
				if c.match.Price > 0 && c.match.Currency != "" {
					byCurrency[c.match.Currency] = append(byCurrency[c.match.Currency], c)
				}
			}
			switch len(byCurrency) {
			case 0:
				// No candidate carries a currency; tier 3 below handles it.
			case 1:
				for _, group := range byCurrency {
					cheapest = selectCheapest(group)
				}
			default:
				chosenCur, chosenCount := "", -1
				for cur, group := range byCurrency {
					n := len(group)
					if n > chosenCount || (n == chosenCount && cur < chosenCur) {
						chosenCur, chosenCount = cur, n
					}
				}
				cheapest = selectCheapest(byCurrency[chosenCur])
			}
		}
		if cheapest.Price == 0 {
			cheapest = selectCheapest(usable)
		}
		result.NewPrice = cheapest.Price
		result.Currency = cheapest.Currency
		// Round 20 found notify.go's notifyRoom rendered r.RoomMatches
		// straight from the unfiltered `matches` slice this result was
		// initialized with, so a whitespace/malformed-currency offer
		// rejected for persistence purposes above was still printed and
		// counted as "available" to the user. Narrow the reported matches to
		// the usable set now that it's computed. Found by GPT second-opinion
		// review, 2026-07-30 (round 20).
		usableOnly := make([]RoomMatch, len(usable))
		for i, c := range usable {
			usableOnly[i] = c.match
		}
		result.RoomMatches = usableOnly

		// Round 19: a genuinely absent currency (not whitespace-garbage, which
		// the usable-filter above already rejects) must not blank out an
		// already-known watch currency. Checked before the transaction opens:
		// it aborts the whole check rather than deciding what to persist.
		if cheapest.Price > 0 && cheapest.Currency == "" && w.Currency != "" {
			return CheckResult{Watch: w, Error: fmt.Errorf("checker returned no currency for price %v on a watch already tracking %s", cheapest.Price, w.Currency)}
		}
		matchedRoom = cheapest.Name
	}

	// Persist. Same structure, and the same reasoning, as
	// checkOneWithWebhookContext: the currency decision, the threshold wipes,
	// the observation resets and every derived signal are computed INSIDE the
	// transaction against the committed record.
	//
	// The room path previously wrote a whole detached Watch through
	// UpdateWatchAndRecordPrice, which held only s.mu -- no advisory lock, no
	// reload. A concurrent settings edit during a room check was silently
	// reverted and two processes last-writer-wins'd each other, which is the
	// hazard the flight path had already been fixed for. Found by adversarial
	// review, 2026-08-02.
	var (
		alertsCleared bool
		prevPrice     float64
		priceDrop     float64
		belowGoal     bool
		saved         Watch
		applied       = true
	)
	if result.NewPrice > 0 {
		price, currency := result.NewPrice, result.Currency
		saved, applied, err = store.MutateAndRecordPrice(w.ID, pollKey, price, currency, func(cur *Watch) (bool, bool) {
			prevPrice = cur.LastPrice

			// Round 18: cur.LastPrice == 0 is not a reliable "no prior
			// observation" signal -- migrate.go's dedup merge can leave a
			// survivor with LastPrice == 0 beside a nonzero LowestPrice
			// inherited from a merged-away duplicate.
			hasPriorObservation := cur.LastPrice > 0 || cur.LowestPrice > 0
			// Round 19: cur.Currency == "" with real prior history is an
			// untrustworthy-currency signal, not a safe "no mismatch possible".
			unknownCurrencyWithHistory := cur.Currency == "" && hasPriorObservation
			currencyMismatch := currency != "" && ((cur.Currency != "" && cur.Currency != currency) || unknownCurrencyWithHistory)
			currencyChanged := hasPriorObservation && currencyMismatch
			firstQuoteMismatch := !hasPriorObservation && currencyMismatch
			skipThresholdChecks := currencyChanged || firstQuoteMismatch

			// Round 6 (widened round 15): BelowPrice is skipped on any currency
			// mismatch. It is a user-set magnitude in the OLD/assumed currency,
			// and comparing it straight against a NEW-currency price
			// reinterprets the threshold rather than converting it -- a JPY
			// 15,000 target would fire against a EUR 180 quote.
			if !skipThresholdChecks {
				if cur.BelowPrice > 0 && price > 0 && price <= cur.BelowPrice {
					belowGoal = true
				}
				if cur.LastPrice > 0 && price > 0 {
					priceDrop = price - cur.LastPrice
				}
			}

			if currencyMismatch {
				// Round 23: gate on an actual threshold being lost, not merely
				// on currencyChanged, so a no-op reset is not mislabelled as a
				// lost alert and a firstQuoteMismatch is not missed.
				if cur.BelowPrice > 0 || cur.AlertDropAbs > 0 {
					alertsCleared = true
				}
				cur.BelowPrice = 0
				cur.AlertDropAbs = 0
			}
			if currencyChanged {
				cur.LowestPrice = 0
				cur.CheapestDate = ""
				cur.BaselinePrice = 0
				cur.LastAlertedPrice = 0
				prevPrice = 0
				priceDrop = 0
			}

			cur.LastCheck = time.Now()
			cur.MatchedRoom = matchedRoom
			cur.LastPrice = price
			cur.Currency = currency
			if cur.LowestPrice == 0 || price < cur.LowestPrice {
				cur.LowestPrice = price
			}

			// Decided from committed state: if another process already migrated
			// this watch, currencyChanged is false here and its new-currency
			// history survives.
			return currencyChanged, true
		})
	} else {
		// No usable price: only the checked-at stamp (and the matched room, if
		// any) is owned by this check. Still transactional, so it cannot revert
		// a concurrent edit either.
		saved, err = store.Mutate(w.ID, func(cur *Watch) {
			cur.LastCheck = time.Now()
			if matchedRoom != "" {
				cur.MatchedRoom = matchedRoom
			}
		})
	}
	if err != nil {
		result.Error = fmt.Errorf("update watch and record price: %w", err)
		return result
	}
	if !applied {
		// Re-targeted mid-poll: this result is for a question nobody is asking
		// any more. Same reasoning as checkOneWithWebhookContext.
		result.Watch = saved
		result.Stale = true
		return result
	}
	result.PrevPrice = prevPrice
	result.PriceDrop = priceDrop
	result.BelowGoal = belowGoal
	result.AlertsClearedByCurrencyChange = alertsCleared
	w = saved

	result.Watch = w

	// Fire webhook on price drop.
	if result.PriceDrop < 0 {
		go fireWebhook(webhookCtx, result)
	}

	return result
}
