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
	var currencyChanged bool

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

		// A currency change invalidates every scalar this watch has
		// previously observed, same invariant and same fix as
		// checkOneWithWebhookContext (round 4 of adversarial review found
		// this path was missed the first time -- round 5). BelowPrice and
		// AlertDropAbs are also cleared, not just skipped for this poll --
		// same round-7 finding as the flight path: this poller has no fresh
		// user-supplied threshold to fall back on, so leaving them set would
		// silently reinterpret an old-currency magnitude as the new currency
		// on every later poll.
		//
		// Round 14 found the same first-poll false-positive as the flight
		// path: gate the DESTRUCTIVE reset on w.LastPrice > 0 so a watch's
		// first-ever observation establishes the baseline currency instead of
		// "changing" against a user-intent field it never actually observed a
		// price in.
		// Found by adversarial review, 2026-07-29 (round 14).
		//
		// Round 15 found the same gap as the flight path: gating the guard
		// ENTIRELY on w.LastPrice > 0 let a first quote whose currency
		// differs from the watch's assumed/created currency fall straight
		// through to the BelowGoal comparison below, comparing a NEW-currency
		// price directly against a BelowPrice set in the OLD assumed
		// currency. skipThresholdChecks covers both the real transition and
		// this first-quote mismatch. Found by adversarial review, 2026-07-30
		// (round 15).
		//
		// Round 16 found leaving BelowPrice/AlertDropAbs live through a
		// first-quote mismatch only POSTPONED round 15's bug: w.Currency is
		// still set to the newly-adopted currency below, so a later,
		// currency-STABLE poll silently reinterprets the OLD currency's
		// numeric threshold as the NEW currency (same false-BelowGoal
		// failure, one poll later). Zero BelowPrice/AlertDropAbs on ANY
		// currencyMismatch, not just the real transition -- unlike LastPrice/
		// LowestPrice/CheapestDate/BaselinePrice/LastAlertedPrice, which only
		// need invalidating when there was a prior OBSERVATION to begin with.
		// Found by adversarial review, 2026-07-30 (round 16).
		//
		// Round 18: same LastPrice==0 unreliability as the flight path
		// (checkOneWithWebhookContext) -- migrate.go's dedup merge can leave a
		// survivor with LastPrice==0 but LowestPrice>0 inherited from a
		// merged-away duplicate. Treat LowestPrice>0 as an equally valid
		// prior-observation signal so that case takes the currencyChanged
		// (reset) path instead of being misread as a fresh first quote.
		// Found by adversarial review, 2026-07-30 (round 18).
		hasPriorObservation := w.LastPrice > 0 || w.LowestPrice > 0
		// Round 19: mirrors the flight-path fix above -- w.Currency=="" with
		// real prior history is an untrustworthy-currency signal, not a
		// safe "no mismatch possible" signal. Found by GPT second-opinion
		// review, 2026-07-30 (round 19).
		unknownCurrencyWithHistory := w.Currency == "" && hasPriorObservation
		currencyMismatch := cheapest.Price > 0 && cheapest.Currency != "" && ((w.Currency != "" && w.Currency != cheapest.Currency) || unknownCurrencyWithHistory)
		currencyChanged = hasPriorObservation && currencyMismatch
		firstQuoteMismatch := !hasPriorObservation && currencyMismatch
		skipThresholdChecks := currencyChanged || firstQuoteMismatch
		if currencyMismatch {
			// Round 23: match checkOneWithWebhookContext's predicate exactly
			// (gate on an actual threshold being lost, not merely on
			// currencyChanged) -- the round-22 version set the flag whenever
			// currencyChanged was true, unconditionally, which could both
			// under-fire (currencyMismatch without currencyChanged, e.g.
			// firstQuoteMismatch, also zeroes BelowPrice/AlertDropAbs just
			// below but was never flagged) and mislabel a no-op reset as a
			// lost alert. Found by GPT second-opinion review, 2026-07-30
			// (round 23).
			if w.BelowPrice > 0 || w.AlertDropAbs > 0 {
				result.AlertsClearedByCurrencyChange = true
			}
			w.BelowPrice = 0
			w.AlertDropAbs = 0
		}
		if currencyChanged {
			w.LastPrice = 0
			w.LowestPrice = 0
			w.CheapestDate = ""
			w.BaselinePrice = 0
			w.LastAlertedPrice = 0
		}

		// Check threshold. BelowPrice is skipped on a currency change (real or
		// first-quote mismatch) for the same reason as checkOneWithWebhookContext:
		// it is a user-set magnitude in the OLD/assumed currency, and comparing
		// it straight against a NEW-currency price reinterprets the threshold
		// rather than converting it, which can fire a false BelowGoal alert on
		// an unrelated magnitude (e.g. a JPY 15,000 target vs. a EUR 180 quote).
		// Found by adversarial review, 2026-07-29 (round 6); widened round 15.
		if !skipThresholdChecks && w.BelowPrice > 0 && cheapest.Price > 0 && cheapest.Price <= w.BelowPrice {
			result.BelowGoal = true
		}

		// Calculate price change from last check.
		result.PrevPrice = w.LastPrice
		if !skipThresholdChecks && w.LastPrice > 0 && cheapest.Price > 0 {
			result.PriceDrop = cheapest.Price - w.LastPrice
		}

		// Round 19: mirrors the flight-path fix -- a genuinely absent
		// currency (not whitespace-garbage, which the usable-filter above
		// already rejects) must not blank out an already-known watch
		// currency. Found by GPT second-opinion review, 2026-07-30
		// (round 19).
		if cheapest.Price > 0 && cheapest.Currency == "" && w.Currency != "" {
			return CheckResult{Watch: w, Error: fmt.Errorf("checker returned no currency for price %v on a watch already tracking %s", cheapest.Price, w.Currency)}
		}

		// Update watch state.
		w.LastCheck = time.Now()
		w.MatchedRoom = cheapest.Name
		if cheapest.Price > 0 {
			w.LastPrice = cheapest.Price
			w.Currency = cheapest.Currency
			if w.LowestPrice == 0 || cheapest.Price < w.LowestPrice {
				w.LowestPrice = cheapest.Price
			}
		}
	} else {
		// No matches — still mark as checked.
		w.LastCheck = time.Now()
	}

	// Persist updates. When a new price was found, the watch update and the
	// new price point are persisted atomically (a single lock+save),
	// purging prior-currency history first when currencyChanged -- same
	// crash-window fix as checkOneWithWebhookContext. Found by adversarial
	// review, 2026-07-29 (round 11).
	//
	// Same scope caveat as checkOneWithWebhookContext above: "atomically"
	// covers the in-memory multi-call race on this *Store instance only, not
	// cross-process coordination or on-disk two-file crash atomicity -- both
	// pre-existing, store-wide, documented in
	// docs/design/2026-07-26-watch-store-coordination.md and persistLocked's
	// comment.
	if result.NewPrice > 0 {
		if err := store.UpdateWatchAndRecordPrice(w, currencyChanged, result.NewPrice, result.Currency); err != nil {
			result.Error = fmt.Errorf("update watch and record price: %w", err)
			return result
		}
	} else {
		if err := store.UpdateWatch(w); err != nil {
			result.Error = fmt.Errorf("update watch: %w", err)
			return result
		}
	}

	result.Watch = w

	// Fire webhook on price drop.
	if result.PriceDrop < 0 {
		go fireWebhook(webhookCtx, result)
	}

	return result
}
