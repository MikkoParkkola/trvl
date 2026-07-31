package watch

import (
	"context"
	"testing"
)

// sequencedChecker returns a different (price, currency) pair on each
// successive call, simulating a price source that flips currency between
// checks (e.g. a region/locale change upstream).
type sequencedChecker struct {
	calls int
	steps []struct {
		price    float64
		currency string
	}
}

func (c *sequencedChecker) CheckPrice(_ context.Context, _ Watch) (float64, string, string, error) {
	step := c.steps[c.calls]
	if c.calls < len(c.steps)-1 {
		c.calls++
	}
	return step.price, step.currency, "", nil
}

// sequencedRoomChecker mirrors sequencedChecker for the room-watch path
// (checkRoomWithWebhookContext), which dispatches through a SEPARATE code
// path from checkOneWithWebhookContext and needed its own currency-change
// reset. Found by adversarial review, 2026-07-29 (round 5): round 4's fix
// only covered the flight/non-room path.
type sequencedRoomChecker struct {
	calls int
	steps [][]RoomMatch
}

func (c *sequencedRoomChecker) CheckRooms(_ context.Context, _ Watch) ([]RoomMatch, error) {
	step := c.steps[c.calls]
	if c.calls < len(c.steps)-1 {
		c.calls++
	}
	return step, nil
}

func TestCheckRoomResetsScalarsOnCurrencyChange(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	id, _, err := store.Add(Watch{
		Type:         "room",
		HotelName:    "Grand Hotel",
		RoomKeywords: []string{"deluxe"},
		Origin:       "HEL",
		Destination:  "BCN",
		DepartDate:   "2026-07-01",
		ReturnDate:   "2026-07-05",
		Currency:     "JPY",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	checker := &sequencedRoomChecker{steps: [][]RoomMatch{
		{{Name: "Deluxe", Price: 20000, Currency: "JPY"}},
		{{Name: "Deluxe", Price: 180, Currency: "EUR"}},
	}}

	if results := CheckAllWithRooms(context.Background(), store, nil, checker); len(results) != 1 || results[0].Error != nil {
		t.Fatalf("first check: results=%+v", results)
	}
	got, ok := store.Get(id)
	if !ok || got.Currency != "JPY" || got.LowestPrice != 20000 {
		t.Fatalf("after JPY check: got=%+v ok=%v, want Currency=JPY LowestPrice=20000", got, ok)
	}

	results := CheckAllWithRooms(context.Background(), store, nil, checker)
	if len(results) != 1 || results[0].Error != nil {
		t.Fatalf("second check: results=%+v", results)
	}
	if results[0].PriceDrop != 0 {
		t.Errorf("PriceDrop = %v, want 0 (JPY 20000 -> EUR 180 is a currency change, not a real drop)", results[0].PriceDrop)
	}

	got, ok = store.Get(id)
	if !ok {
		t.Fatalf("watch not found after second check")
	}
	if got.Currency != "EUR" {
		t.Fatalf("Currency = %q, want EUR", got.Currency)
	}
	if got.LowestPrice != 180 {
		t.Errorf("LowestPrice = %v, want 180 (the stale JPY 20000 must not survive as a fake EUR low)", got.LowestPrice)
	}
}

// A currency change on the periodic-check path must invalidate every scalar
// the watch previously observed, exactly like Watch.applyIntent already does
// on the re-watch path (store.go). Without this, an EUR 50 low followed by a
// JPY 10,000 observation leaves Currency=JPY next to a stale LowestPrice=50
// -- a false "JPY 50" low that migrate.go's group-first recompute would then
// treat as genuine, since it trusts Watch.Currency/Watch.LowestPrice as
// already internally consistent. Found by adversarial review, 2026-07-29
// (round 4).
func TestCheckOneResetsScalarsOnCurrencyChange(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	id, _, err := store.Add(Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-01",
		Currency:    "EUR",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	checker := &sequencedChecker{steps: []struct {
		price    float64
		currency string
	}{
		{price: 50, currency: "EUR"},
		{price: 10000, currency: "JPY"},
	}}

	if results := CheckAll(context.Background(), store, checker); len(results) != 1 || results[0].Error != nil {
		t.Fatalf("first check: results=%+v", results)
	}
	got, ok := store.Get(id)
	if !ok {
		t.Fatalf("watch not found after first check")
	}
	if got.Currency != "EUR" || got.LowestPrice != 50 {
		t.Fatalf("after EUR check: Currency=%q LowestPrice=%v, want EUR/50", got.Currency, got.LowestPrice)
	}

	results := CheckAll(context.Background(), store, checker)
	if len(results) != 1 || results[0].Error != nil {
		t.Fatalf("second check: results=%+v", results)
	}
	if results[0].PriceDrop != 0 {
		t.Errorf("PriceDrop = %v, want 0 (EUR 50 -> JPY 10000 is not a real price drop, it's a currency change)", results[0].PriceDrop)
	}

	got, ok = store.Get(id)
	if !ok {
		t.Fatalf("watch not found after second check")
	}
	if got.Currency != "JPY" {
		t.Fatalf("Currency = %q, want JPY", got.Currency)
	}
	if got.LowestPrice != 10000 {
		t.Errorf("LowestPrice = %v, want 10000 (the stale EUR 50 must not survive as a fake JPY low)", got.LowestPrice)
	}
}

// BelowPrice is a user-set alert threshold in the watch's PRIOR currency.
// Comparing a fresh quote in a NEW currency straight against it reinterprets
// the threshold instead of converting it -- e.g. a JPY 15,000 target
// compared against a EUR 180 quote sets BelowGoal=true purely because
// 180 <= 15000, a false alert on an unrelated magnitude. The threshold check
// must be skipped on a currency change, same as PriceDrop and the
// last-minute-deal signal. Found by adversarial review, 2026-07-29 (round 6).
//
// Round 7 found that skipping the check on the transition poll alone was not
// enough: BelowPrice must be CLEARED, not just skipped once, or the THIRD
// poll (now same-currency, EUR-to-EUR) compares the fresh price against the
// still-stale JPY threshold and fires a false alert one poll later. This
// test therefore checks a third time in the new currency and asserts the
// threshold stays disabled.
func TestCheckOneSkipsBelowGoalOnCurrencyChange(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	id, _, err := store.Add(Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-01",
		Currency:    "JPY",
		BelowPrice:  15000,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	checker := &sequencedChecker{steps: []struct {
		price    float64
		currency string
	}{
		{price: 20000, currency: "JPY"},
		{price: 180, currency: "EUR"},
		{price: 170, currency: "EUR"},
	}}

	if results := CheckAll(context.Background(), store, checker); len(results) != 1 || results[0].Error != nil {
		t.Fatalf("first check: results=%+v", results)
	}

	results := CheckAll(context.Background(), store, checker)
	if len(results) != 1 || results[0].Error != nil {
		t.Fatalf("second check: results=%+v", results)
	}
	if results[0].BelowGoal {
		t.Errorf("BelowGoal = true on a currency change (JPY 15000 target vs EUR 180 quote) -- " +
			"the threshold was reinterpreted in the new currency instead of being skipped")
	}
	got, ok := store.Get(id)
	if !ok || got.BelowPrice != 0 {
		t.Fatalf("after currency change: BelowPrice=%v ok=%v, want 0 (cleared, not left stale)", got.BelowPrice, ok)
	}

	// Third poll, now EUR-to-EUR (no currency change) -- the stale JPY
	// threshold must NOT resurface and fire a false alert one poll later.
	results = CheckAll(context.Background(), store, checker)
	if len(results) != 1 || results[0].Error != nil {
		t.Fatalf("third check: results=%+v", results)
	}
	if results[0].BelowGoal {
		t.Errorf("BelowGoal = true on the third (same-currency) poll -- " +
			"the stale pre-change threshold resurfaced instead of staying cleared")
	}
}

// Room-path counterpart of TestCheckOneSkipsBelowGoalOnCurrencyChange --
// checkRoomWithWebhookContext dispatches through a separate code path and
// needed the identical guard.
func TestCheckRoomSkipsBelowGoalOnCurrencyChange(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	id, _, err := store.Add(Watch{
		Type:         "room",
		HotelName:    "Grand Hotel",
		RoomKeywords: []string{"deluxe"},
		Origin:       "HEL",
		Destination:  "BCN",
		DepartDate:   "2026-07-01",
		ReturnDate:   "2026-07-05",
		Currency:     "JPY",
		BelowPrice:   15000,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	checker := &sequencedRoomChecker{steps: [][]RoomMatch{
		{{Name: "Deluxe", Price: 20000, Currency: "JPY"}},
		{{Name: "Deluxe", Price: 180, Currency: "EUR"}},
		{{Name: "Deluxe", Price: 170, Currency: "EUR"}},
	}}

	if results := CheckAllWithRooms(context.Background(), store, nil, checker); len(results) != 1 || results[0].Error != nil {
		t.Fatalf("first check: results=%+v", results)
	}

	results := CheckAllWithRooms(context.Background(), store, nil, checker)
	if len(results) != 1 || results[0].Error != nil {
		t.Fatalf("second check: results=%+v", results)
	}
	if results[0].BelowGoal {
		t.Errorf("BelowGoal = true on a currency change (JPY 15000 target vs EUR 180 quote) -- " +
			"the threshold was reinterpreted in the new currency instead of being skipped")
	}
	got, ok := store.Get(id)
	if !ok || got.BelowPrice != 0 {
		t.Fatalf("after currency change: BelowPrice=%v ok=%v, want 0 (cleared, not left stale)", got.BelowPrice, ok)
	}

	// Third poll, now EUR-to-EUR (no currency change) -- the stale JPY
	// threshold must NOT resurface and fire a false alert one poll later.
	results = CheckAllWithRooms(context.Background(), store, nil, checker)
	if len(results) != 1 || results[0].Error != nil {
		t.Fatalf("third check: results=%+v", results)
	}
	if results[0].BelowGoal {
		t.Errorf("BelowGoal = true on the third (same-currency) poll -- " +
			"the stale pre-change threshold resurfaced instead of staying cleared")
	}

	if _, ok := store.Get(id); !ok {
		t.Fatalf("watch not found after checks")
	}
}

// A currency change resets the watch's own scalar fields (covered above),
// but every PricePoint already recorded for the watch is still denominated
// in the OLD currency. Store.Add already purges history on this transition
// for the re-watch path (store.go's applyIntent); the periodic-check path
// needed the identical treatment or History/Sparkline/TrendArrow would mix
// an EUR 50 point with a JPY 10,000 point in one series. Found by
// adversarial review, 2026-07-29 (round 10).
func TestCheckOnePurgesHistoryOnCurrencyChange(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	id, _, err := store.Add(Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-01",
		Currency:    "EUR",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	checker := &sequencedChecker{steps: []struct {
		price    float64
		currency string
	}{
		{price: 50, currency: "EUR"},
		{price: 10000, currency: "JPY"},
	}}

	if results := CheckAll(context.Background(), store, checker); len(results) != 1 || results[0].Error != nil {
		t.Fatalf("first check: results=%+v", results)
	}
	if hist := store.History(id); len(hist) != 1 || hist[0].Currency != "EUR" {
		t.Fatalf("after first (EUR) check: history=%+v, want 1 EUR point", hist)
	}

	results := CheckAll(context.Background(), store, checker)
	if len(results) != 1 || results[0].Error != nil {
		t.Fatalf("second check: results=%+v", results)
	}

	hist := store.History(id)
	for _, p := range hist {
		if p.Currency != "JPY" {
			t.Fatalf("history = %+v, want only JPY points -- the stale EUR 50 point survived the currency change and would plot a fabricated cross-currency trend", hist)
		}
	}
	if len(hist) != 1 || hist[0].Price != 10000 {
		t.Fatalf("history = %+v, want exactly one JPY 10000 point", hist)
	}
}

// Room-path counterpart of TestCheckOnePurgesHistoryOnCurrencyChange --
// checkRoomWithWebhookContext dispatches through a separate code path and
// needed the identical purge.
func TestCheckRoomPurgesHistoryOnCurrencyChange(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	id, _, err := store.Add(Watch{
		Type:         "room",
		HotelName:    "Grand Hotel",
		RoomKeywords: []string{"deluxe"},
		Origin:       "HEL",
		Destination:  "BCN",
		DepartDate:   "2026-07-01",
		ReturnDate:   "2026-07-05",
		Currency:     "JPY",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	checker := &sequencedRoomChecker{steps: [][]RoomMatch{
		{{Name: "Deluxe", Price: 20000, Currency: "JPY"}},
		{{Name: "Deluxe", Price: 180, Currency: "EUR"}},
	}}

	if results := CheckAllWithRooms(context.Background(), store, nil, checker); len(results) != 1 || results[0].Error != nil {
		t.Fatalf("first check: results=%+v", results)
	}
	if hist := store.History(id); len(hist) != 1 || hist[0].Currency != "JPY" {
		t.Fatalf("after first (JPY) check: history=%+v, want 1 JPY point", hist)
	}

	results := CheckAllWithRooms(context.Background(), store, nil, checker)
	if len(results) != 1 || results[0].Error != nil {
		t.Fatalf("second check: results=%+v", results)
	}

	hist := store.History(id)
	for _, p := range hist {
		if p.Currency != "EUR" {
			t.Fatalf("history = %+v, want only EUR points -- the stale JPY 20000 point survived the currency change and would plot a fabricated cross-currency trend", hist)
		}
	}
	if len(hist) != 1 || hist[0].Price != 180 {
		t.Fatalf("history = %+v, want exactly one EUR 180 point", hist)
	}
}

// A watch's very FIRST successful poll must never be treated as a currency
// change, even if the quote currency differs from the user's configured
// Currency: the search backend's quote currency is IP/market-driven (see
// livecheck.go's SearchOptions.Currency note), not something the user
// actually controls, so a USD watch checked from EUR egress is normal, not a
// transition. Before round 14's fix, this silently zeroed BelowPrice and
// AlertDropAbs before the user ever got a single real observation.
// Found by adversarial review, 2026-07-29 (round 14).
func TestCheckOneDoesNotTreatFirstPollAsCurrencyChange(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	id, _, err := store.Add(Watch{
		Type:         "flight",
		Origin:       "HEL",
		Destination:  "BCN",
		DepartDate:   "2026-07-01",
		Currency:     "USD",
		BelowPrice:   500,
		AlertDropAbs: 50,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	// The watch was created wanting USD, but the very first check returns
	// EUR (e.g. egress-region-driven quote currency) -- this must NOT read
	// as a currency change, since there is no prior observation to change
	// from.
	checker := &sequencedChecker{steps: []struct {
		price    float64
		currency string
	}{
		{price: 450, currency: "EUR"},
	}}

	results := CheckAll(context.Background(), store, checker)
	if len(results) != 1 || results[0].Error != nil {
		t.Fatalf("first check: results=%+v", results)
	}
	// Round 15 (adversarial review, 2026-07-30): a first quote in a
	// different currency must not be compared against a threshold set in
	// the assumed/created currency -- 450 EUR <= 500 USD's numeric value
	// is not a real "below goal," it is two different currencies' numbers
	// compared as if they were the same unit. This is the exact scenario
	// round 15 found produced a silent false BelowGoal=true that this test
	// never asserted against.
	if results[0].BelowGoal {
		t.Errorf("BelowGoal = true, want false (450 EUR must not be compared against a 500 USD threshold on the first, currency-establishing poll)")
	}

	got, ok := store.Get(id)
	if !ok {
		t.Fatalf("watch not found after first check")
	}
	// Round 16 (adversarial review, 2026-07-30): leaving BelowPrice/
	// AlertDropAbs live here only postponed round 15's bug -- w.Currency
	// flips to EUR below, so a later EUR-stable poll would silently
	// reinterpret the OLD USD numeric threshold as a EUR one. They must be
	// zeroed on ANY currency mismatch, not just a real transition.
	if got.BelowPrice != 0 {
		t.Errorf("BelowPrice = %v, want 0 (a threshold set in the OLD assumed currency must not silently apply to the NEW adopted currency)", got.BelowPrice)
	}
	if got.AlertDropAbs != 0 {
		t.Errorf("AlertDropAbs = %v, want 0 (same reasoning as BelowPrice)", got.AlertDropAbs)
	}
	if got.Currency != "EUR" {
		t.Fatalf("Currency = %q, want EUR (the first observation establishes the baseline currency)", got.Currency)
	}
	if got.LowestPrice != 450 {
		t.Errorf("LowestPrice = %v, want 450 (first observation, not reset to 0)", got.LowestPrice)
	}
}

// Round 16 (adversarial review, 2026-07-30): proves the fix actually closes
// the gap, not just relocates it -- a SECOND poll, now currency-STABLE
// (EUR->EUR), must not inherit a threshold that was silently reinterpreted
// from the old assumed currency (USD) into the newly-adopted one (EUR).
func TestCheckOneSecondPollAfterFirstQuoteMismatchDoesNotReinterpretThreshold(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	id, _, err := store.Add(Watch{
		Type:         "flight",
		Origin:       "HEL",
		Destination:  "BCN",
		DepartDate:   "2026-07-01",
		Currency:     "USD",
		BelowPrice:   500,
		AlertDropAbs: 50,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	checker := &sequencedChecker{steps: []struct {
		price    float64
		currency string
	}{
		{price: 450, currency: "EUR"}, // first quote: mismatch, adopts EUR
		{price: 400, currency: "EUR"}, // second quote: currency-stable
	}}

	results := CheckAll(context.Background(), store, checker)
	if len(results) != 1 || results[0].Error != nil {
		t.Fatalf("first check: results=%+v", results)
	}

	results = CheckAll(context.Background(), store, checker)
	if len(results) != 1 || results[0].Error != nil {
		t.Fatalf("second check: results=%+v", results)
	}
	// Before the round-16 fix, BelowPrice=500 (the OLD USD numeric value)
	// stayed live and got silently compared against a EUR price on this
	// currency-stable second poll: 400 <= 500 fired a false BelowGoal even
	// though the watch never had a EUR-denominated 500 threshold.
	if results[0].BelowGoal {
		t.Errorf("BelowGoal = true, want false (400 EUR must not be compared against a threshold that was set in USD and never re-denominated)")
	}

	got, ok := store.Get(id)
	if !ok {
		t.Fatalf("watch not found after second check")
	}
	if got.BelowPrice != 0 {
		t.Errorf("BelowPrice = %v, want 0 (still cleared -- no fresh EUR threshold was ever supplied)", got.BelowPrice)
	}
}

// Room-path counterpart of TestCheckOneDoesNotTreatFirstPollAsCurrencyChange.
func TestCheckRoomDoesNotTreatFirstPollAsCurrencyChange(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	id, _, err := store.Add(Watch{
		Type:         "room",
		HotelName:    "Grand Hotel",
		RoomKeywords: []string{"deluxe"},
		Origin:       "HEL",
		Destination:  "BCN",
		DepartDate:   "2026-07-01",
		ReturnDate:   "2026-07-05",
		Currency:     "USD",
		BelowPrice:   500,
		AlertDropAbs: 50,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	checker := &sequencedRoomChecker{steps: [][]RoomMatch{
		{{Name: "Deluxe", Price: 450, Currency: "EUR"}},
	}}

	results := CheckAllWithRooms(context.Background(), store, nil, checker)
	if len(results) != 1 || results[0].Error != nil {
		t.Fatalf("first check: results=%+v", results)
	}
	// Room-path counterpart of the round-15 BelowGoal assertion above.
	if results[0].BelowGoal {
		t.Errorf("BelowGoal = true, want false (450 EUR must not be compared against a 500 USD threshold on the first, currency-establishing poll)")
	}

	got, ok := store.Get(id)
	if !ok {
		t.Fatalf("watch not found after first check")
	}
	// Round 16: same reasoning as the flight-path counterpart above.
	if got.BelowPrice != 0 {
		t.Errorf("BelowPrice = %v, want 0 (a threshold set in the OLD assumed currency must not silently apply to the NEW adopted currency)", got.BelowPrice)
	}
	if got.AlertDropAbs != 0 {
		t.Errorf("AlertDropAbs = %v, want 0 (same reasoning as BelowPrice)", got.AlertDropAbs)
	}
	if got.Currency != "EUR" {
		t.Fatalf("Currency = %q, want EUR (the first observation establishes the baseline currency)", got.Currency)
	}
}

// Room-path counterpart of
// TestCheckOneSecondPollAfterFirstQuoteMismatchDoesNotReinterpretThreshold.
func TestCheckRoomSecondPollAfterFirstQuoteMismatchDoesNotReinterpretThreshold(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	id, _, err := store.Add(Watch{
		Type:         "room",
		HotelName:    "Grand Hotel",
		RoomKeywords: []string{"deluxe"},
		Origin:       "HEL",
		Destination:  "BCN",
		DepartDate:   "2026-07-01",
		ReturnDate:   "2026-07-05",
		Currency:     "USD",
		BelowPrice:   500,
		AlertDropAbs: 50,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	checker := &sequencedRoomChecker{steps: [][]RoomMatch{
		{{Name: "Deluxe", Price: 450, Currency: "EUR"}}, // first: mismatch, adopts EUR
		{{Name: "Deluxe", Price: 400, Currency: "EUR"}}, // second: currency-stable
	}}

	results := CheckAllWithRooms(context.Background(), store, nil, checker)
	if len(results) != 1 || results[0].Error != nil {
		t.Fatalf("first check: results=%+v", results)
	}

	results = CheckAllWithRooms(context.Background(), store, nil, checker)
	if len(results) != 1 || results[0].Error != nil {
		t.Fatalf("second check: results=%+v", results)
	}
	if results[0].BelowGoal {
		t.Errorf("BelowGoal = true, want false (400 EUR must not be compared against a threshold that was set in USD and never re-denominated)")
	}

	got, ok := store.Get(id)
	if !ok {
		t.Fatalf("watch not found after second check")
	}
	if got.BelowPrice != 0 {
		t.Errorf("BelowPrice = %v, want 0 (still cleared -- no fresh EUR threshold was ever supplied)", got.BelowPrice)
	}
}

// Round 17 (adversarial review, 2026-07-30): proves the round-16 fix's own
// regression is closed. A watch configured with an absolute-only alert
// threshold (AlertDropAbs>0, AlertDropPct==0, i.e. no percentage limb ever
// set) must NOT silently acquire pricealert.DefaultDropPercent (10%) after
// AlertDropAbs is force-zeroed by a currency mismatch. Before the round-17
// fix, pricealert.Evaluate ran unconditionally every poll and
// Threshold.effective() substituted the 10% default the instant both limbs
// read zero -- so a user who deliberately chose an absolute-only policy
// silently got an unrequested percentage policy instead, with no
// notification and no recovery short of re-watching.
func TestCheckOneSuppressesDefaultAlertAfterAbsOnlyThresholdClearedByCurrency(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	id, _, err := store.Add(Watch{
		Type:         "flight",
		Origin:       "HEL",
		Destination:  "BCN",
		DepartDate:   "2026-07-01",
		Currency:     "USD",
		AlertDropAbs: 50, // absolute-only: AlertDropPct is never set (0).
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	checker := &sequencedChecker{steps: []struct {
		price    float64
		currency string
	}{
		{price: 450, currency: "EUR"}, // first quote: mismatch, adopts EUR, clears AlertDropAbs
		{price: 400, currency: "EUR"}, // second quote: currency-stable, an 11% drop from 450
	}}

	results := CheckAll(context.Background(), store, checker)
	if len(results) != 1 || results[0].Error != nil {
		t.Fatalf("first check: results=%+v", results)
	}
	if results[0].PriceDropAlert {
		t.Errorf("PriceDropAlert = true on the mismatch poll, want false (no baseline exists yet to drop from)")
	}

	got, ok := store.Get(id)
	if !ok {
		t.Fatalf("watch not found after first check")
	}
	if !got.AlertDropAbsClearedByCurrency {
		t.Fatalf("AlertDropAbsClearedByCurrency = false, want true (AlertDropAbs was this watch's only threshold)")
	}
	if got.AlertDropAbs != 0 {
		t.Errorf("AlertDropAbs = %v, want 0", got.AlertDropAbs)
	}

	results = CheckAll(context.Background(), store, checker)
	if len(results) != 1 || results[0].Error != nil {
		t.Fatalf("second check: results=%+v", results)
	}
	// 400 is an 11% drop from the 450 first observation -- comfortably past
	// pricealert.DefaultDropPercent (10%). Before the round-17 fix this
	// fired a PriceDropAlert under a policy the user never configured.
	if results[0].PriceDropAlert {
		t.Errorf("PriceDropAlert = true, want false (alerting must stay suspended -- the user's absolute-only threshold was cleared by a currency mismatch and never re-supplied, so no default policy should silently apply)")
	}

	got, ok = store.Get(id)
	if !ok {
		t.Fatalf("watch not found after second check")
	}
	if got.BaselinePrice != 0 {
		t.Errorf("BaselinePrice = %v, want 0 (Evaluate must never run while suspended, so no baseline should ever be captured)", got.BaselinePrice)
	}
	if !got.AlertDropAbsClearedByCurrency {
		t.Errorf("AlertDropAbsClearedByCurrency = false, want true (still pending -- no fresh threshold was ever supplied)")
	}
}

// Round 18 (adversarial review, 2026-07-30): a currency code arriving in a
// different CASE than the watch's stored currency ("eur" vs "EUR") must not
// be misread as a real currency change. Both check.go's currencyMismatch
// comparisons and Store.Add's entry-point normalization now canonicalize to
// uppercase; this proves the poll side actually stays quiet on a case-only
// difference instead of needlessly wiping BelowPrice/AlertDropAbs/history.
func TestCheckOneNormalizesCurrencyCaseAcrossPolls(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	id, _, err := store.Add(Watch{
		Type:         "flight",
		Origin:       "HEL",
		Destination:  "BCN",
		DepartDate:   "2026-07-01",
		Currency:     "usd", // lower-case caller input -- Add must normalize.
		BelowPrice:   500,
		AlertDropAbs: 50,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if got, ok := store.Get(id); !ok || got.Currency != "USD" {
		t.Fatalf("Currency after Add = %q, want USD (Store.Add must normalize case at the entry point)", got.Currency)
	}

	checker := &sequencedChecker{steps: []struct {
		price    float64
		currency string
	}{
		{price: 480, currency: "usd"}, // first quote: lower-case, same currency.
		{price: 460, currency: "USD"}, // second quote: upper-case, still same currency.
	}}

	results := CheckAll(context.Background(), store, checker)
	if len(results) != 1 || results[0].Error != nil {
		t.Fatalf("first check: results=%+v", results)
	}
	got, ok := store.Get(id)
	if !ok {
		t.Fatalf("watch not found after first check")
	}
	if got.Currency != "USD" {
		t.Errorf("Currency = %q, want USD (normalized, not the raw lower-case provider string)", got.Currency)
	}
	if got.BelowPrice != 500 || got.AlertDropAbs != 50 {
		t.Errorf("BelowPrice=%v AlertDropAbs=%v, want unchanged (a case-only difference is not a real currency change)", got.BelowPrice, got.AlertDropAbs)
	}
	if got.LowestPrice != 480 {
		t.Errorf("LowestPrice = %v, want 480 (a real observation, not a reset-then-refill)", got.LowestPrice)
	}

	results = CheckAll(context.Background(), store, checker)
	if len(results) != 1 || results[0].Error != nil {
		t.Fatalf("second check: results=%+v", results)
	}
	// 460 <= 500 is a real BelowGoal in a currency-stable series; it must
	// fire once thresholds were never wiped by the case-only "change."
	if !results[0].BelowGoal {
		t.Errorf("BelowGoal = false, want true (460 USD is genuinely below the 500 USD threshold)")
	}
}

// Round 18 (adversarial review, 2026-07-30): migrate.go's dedup merge
// (collapseDuplicatesLocked) can leave a surviving watch with LastPrice==0
// but LowestPrice>0, inherited from a currency-matching duplicate it merged
// away. Before the fix, checkOneWithWebhookContext treated LastPrice==0 as
// the sole "no prior observation" signal, so a currency-mismatched poll
// against this watch was misread as a fresh first quote: the reset branch
// (currencyChanged) never fired, and the stale OLD-currency LowestPrice
// survived to be compared, unconverted, against the NEW-currency price at
// the unconditional "if w.LowestPrice == 0 || price < w.LowestPrice" update.
