package watch

import (
	"context"
	"testing"
)

func TestCheckOneTreatsNonzeroLowestPriceAsPriorObservation(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	id, _, err := store.Add(Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-01",
		Currency:    "USD",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	// Simulate a post-merge survivor: LastPrice==0 (this watch object never
	// itself completed a poll) but LowestPrice>0 (inherited from a merged
	// duplicate's history during migrate.go's dedup pass).
	if _, err := store.Mutate(id, func(w *Watch) { w.LowestPrice = 450 }); err != nil {
		t.Fatalf("Mutate: %v", err)
	}

	checker := &sequencedChecker{steps: []struct {
		price    float64
		currency string
	}{
		{price: 10000, currency: "JPY"}, // currency mismatch against USD.
	}}

	results := CheckAll(context.Background(), store, checker)
	if len(results) != 1 || results[0].Error != nil {
		t.Fatalf("check: results=%+v", results)
	}

	got, ok := store.Get(id)
	if !ok {
		t.Fatalf("watch not found after check")
	}
	// The mismatch must be read as a real transition (currencyChanged), not
	// a first-quote mismatch: LowestPrice=450 was a genuine prior USD
	// observation and must be reset before the new JPY price lands, not
	// left in place to be compared cross-currency against it.
	if got.LowestPrice != 10000 {
		t.Errorf("LowestPrice = %v, want 10000 (stale 450 USD must be invalidated by the currency change, not silently compared against a 10000 JPY quote)", got.LowestPrice)
	}
	if got.Currency != "JPY" {
		t.Errorf("Currency = %q, want JPY", got.Currency)
	}
}

// TestCheckRejectsWhitespaceOnlyCurrency is the regression test for GPT's
// round-18 second-opinion finding: TrimSpace-ing a provider currency that is
// non-empty but ALL whitespace collapses it to "", which currencyMismatch's
// `currency != ""` check then reads as "provider gave no currency" -- the
// same code path used for a legitimate absent-currency provider. That
// silently bypasses the mismatch guard instead of triggering it, letting a
// garbage quote overwrite a real USD watch with an empty currency. The fix
// rejects the quote outright instead of guessing.
func TestCheckRejectsWhitespaceOnlyCurrency(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	id, _, err := store.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "USD"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := store.RecordPrice(id, 180, "USD"); err != nil {
		t.Fatalf("record price: %v", err)
	}

	checker := &sequencedChecker{steps: []struct {
		price    float64
		currency string
	}{
		{price: 10000, currency: "   "}, // whitespace-only: garbage, not absent.
	}}

	results := CheckAll(context.Background(), store, checker)
	if len(results) != 1 {
		t.Fatalf("results = %+v, want 1", results)
	}
	if results[0].Error == nil {
		t.Fatalf("check with whitespace-only currency returned no error, want rejection")
	}

	got, ok := store.Get(id)
	if !ok {
		t.Fatalf("watch not found after check")
	}
	if got.Currency != "USD" {
		t.Errorf("Currency = %q, want USD unchanged (a rejected quote must not overwrite real state)", got.Currency)
	}
	if got.LastPrice != 180 {
		t.Errorf("LastPrice = %v, want 180 unchanged (a rejected quote must not overwrite real state)", got.LastPrice)
	}
}

// TestCheckRoomRejectsWhitespaceOnlyCurrency mirrors the flight-path test
// above for the room-watch dispatch path.
func TestCheckRoomRejectsWhitespaceOnlyCurrency(t *testing.T) {
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
		BelowPrice:   200,
		Currency:     "USD",
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := store.RecordPrice(id, 180, "USD"); err != nil {
		t.Fatalf("record price: %v", err)
	}

	checker := &sequencedRoomChecker{steps: [][]RoomMatch{
		{{Name: "Deluxe", Price: 10000, Currency: "  "}},
	}}

	results := CheckAllWithRooms(context.Background(), store, nil, checker)
	found := false
	for _, r := range results {
		if r.Watch.ID == id {
			found = true
			if r.Error == nil {
				t.Errorf("room check with whitespace-only currency returned no error, want rejection")
			}
		}
	}
	if !found {
		t.Fatalf("results %+v missing watch %q", results, id)
	}

	got, ok := store.Get(id)
	if !ok {
		t.Fatalf("watch not found after check")
	}
	if got.Currency != "USD" {
		t.Errorf("Currency = %q, want USD unchanged (a rejected quote must not overwrite real state)", got.Currency)
	}
	if got.LastPrice != 180 {
		t.Errorf("LastPrice = %v, want 180 unchanged (a rejected quote must not overwrite real state)", got.LastPrice)
	}
}

// TestStoreLoadNormalizesLegacyCurrencyCase is the regression test for GPT's
// round-18 second-opinion finding: Add/check.go now normalize the FRESH side
// of every currency comparison, but a watch or history point written to disk
// before round 18 (or by any client that skipped normalization) keeps
// whatever case it was saved in. Store.Load must canonicalize on read, or a
// stored "usd" compares unequal to a freshly-normalized "USD" on the very
// next re-watch or poll, misreading a same-currency re-watch as a currency
// change and wiping real accumulated state.
func TestStoreLoadNormalizesLegacyCurrencyCase(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	id, _, err := store.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "USD"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := store.RecordPrice(id, 450, "USD"); err != nil {
		t.Fatalf("record price: %v", err)
	}

	// Simulate pre-round-18 data: lowercase the persisted currency directly
	// on disk, bypassing Add's normalization entirely.
	for i := range store.watches {
		if store.watches[i].ID == id {
			store.watches[i].Currency = "usd"
		}
	}
	for i := range store.history {
		if store.history[i].WatchID == id {
			store.history[i].Currency = "usd"
		}
	}
	if err := store.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Fresh Store instance, same directory: this is what every real process
	// restart does.
	reloaded := NewStore(dir)
	if err := reloaded.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	w, ok := reloaded.Get(id)
	if !ok {
		t.Fatalf("watch not found after reload")
	}
	if w.Currency != "USD" {
		t.Errorf("Currency after Load = %q, want USD (legacy lowercase currency must be normalized on read)", w.Currency)
	}
	for _, p := range reloaded.History(id) {
		if p.Currency != "USD" {
			t.Errorf("history point Currency = %q, want USD (legacy lowercase history must be normalized on read)", p.Currency)
		}
	}

	// The actual regression this guards: re-adding the SAME currency after
	// reload must not be misread as a currency change.
	if _, _, err := reloaded.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 150, Currency: "USD"}); err != nil {
		t.Fatalf("re-add: %v", err)
	}
	w, _ = reloaded.Get(id)
	if w.LastPrice != 450 {
		t.Errorf("LastPrice = %v, want 450 unchanged (re-adding the SAME currency, post-normalization, must not be read as a currency change)", w.LastPrice)
	}
	if got := reloaded.History(id); len(got) != 1 {
		t.Errorf("history = %d points, want 1 (unchanged currency must not purge history)", len(got))
	}
}

// TestRecordPriceSyncsScalarsForHasPriorObservation is the regression test
// for GPT's round-18 second-opinion finding: RecordPrice appended to history
// without updating LastPrice/LowestPrice, so a caller using RecordPrice
// directly (bypassing the normal UpdateWatchAndRecordPrice poll path) could
// build real price history while hasPriorObservation (round 18) still read
// false from the untouched scalar fields -- letting a later currency change
// slip past the mismatch guard as a misclassified "first quote."
func TestRecordPriceSyncsScalarsForHasPriorObservation(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	id, _, err := store.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "USD"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	if err := store.RecordPrice(id, 450, "USD"); err != nil {
		t.Fatalf("record price: %v", err)
	}
	w, ok := store.Get(id)
	if !ok {
		t.Fatalf("watch not found")
	}
	if w.LastPrice != 450 {
		t.Errorf("LastPrice = %v, want 450 (RecordPrice must sync scalars, not leave hasPriorObservation blind to real history)", w.LastPrice)
	}
	if w.LowestPrice != 450 {
		t.Errorf("LowestPrice = %v, want 450", w.LowestPrice)
	}

	if err := store.RecordPrice(id, 400, "USD"); err != nil {
		t.Fatalf("record price: %v", err)
	}
	w, _ = store.Get(id)
	if w.LastPrice != 400 {
		t.Errorf("LastPrice = %v, want 400", w.LastPrice)
	}
	if w.LowestPrice != 400 {
		t.Errorf("LowestPrice = %v, want 400 (lower of the two observations)", w.LowestPrice)
	}
}

// TestCheckTreatsUnknownCurrencyWithHistoryAsMismatch is the regression test
// for GPT's round-19 finding: Load (round 18) normalizes legacy
// whitespace-only currency to "", so a watch can carry real price history
// (LastPrice/LowestPrice) while w.Currency reads empty. The old
// `w.Currency != ""` guard treated that as "no mismatch possible" and
// compared a fresh currency-bearing quote directly against the
// unknown-currency history, risking a fabricated drop/below-goal alert.
// w.Currency=="" with a prior observation must now be treated as a mismatch
// (reset to a fresh baseline), not a safe direct comparison.
func TestCheckTreatsUnknownCurrencyWithHistoryAsMismatch(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	id, _, err := store.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 1000})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	// Simulate pre-round-18 corrupted data: real history, unknown currency.
	if err := store.RecordPrice(id, 20000, ""); err != nil {
		t.Fatalf("record price: %v", err)
	}
	w, _ := store.Get(id)
	w.LastPrice = 20000
	w.LowestPrice = 20000
	w.Currency = ""

	checker := &stubPriceChecker{price: 180, currency: "EUR"}
	r := checkOne(context.Background(), store, checker, w)
	if r.Error != nil {
		t.Fatalf("unexpected error: %v", r.Error)
	}
	if r.BelowGoal {
		t.Errorf("BelowGoal = true, want false (comparing a new EUR quote against unknown-currency history must not fire a threshold alert)")
	}
	updated, _ := store.Get(id)
	if updated.LastPrice != 180 {
		t.Errorf("LastPrice = %v, want 180 (fresh baseline, not a fabricated drop from unknown-currency history)", updated.LastPrice)
	}
	if updated.Currency != "EUR" {
		t.Errorf("Currency = %q, want EUR", updated.Currency)
	}
}

// TestCheckRoomPicksValidCurrencyOverGarbageSibling is the regression test
// for GPT's round-19 finding: picking the raw numeric minimum across ALL
// matches before filtering garbage currency let one malformed-currency quote
// (cheaper by price) hide a real, valid-currency sibling. The cheapest VALID
// match must win, not be discarded because a cheaper garbage match existed.
func TestCheckRoomPicksValidCurrencyOverGarbageSibling(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	w := Watch{
		Type:         "room",
		HotelName:    "City Hotel",
		RoomKeywords: []string{"double"},
		DepartDate:   "2099-07-01",
		ReturnDate:   "2099-07-08",
	}
	id, _, err := store.Add(w)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	w.ID = id

	checker := &stubRoomChecker{matches: []RoomMatch{
		{Name: "Suspect Room", Price: 100, Currency: "   "},
		{Name: "Valid Room", Price: 120, Currency: "EUR"},
	}}
	r := checkRoom(context.Background(), store, checker, w)
	if r.Error != nil {
		t.Fatalf("unexpected error: %v", r.Error)
	}
	if r.NewPrice != 120 {
		t.Errorf("NewPrice = %v, want 120 (valid-currency sibling, not the discarded garbage-currency minimum)", r.NewPrice)
	}
	if r.Currency != "EUR" {
		t.Errorf("Currency = %q, want EUR", r.Currency)
	}
}

// TestRecordPriceRejectsCurrencyMismatch is the regression test for GPT's
// round-20 finding: round-19's fix skipped the scalar sync on a currency
// mismatch but still appended the mismatched observation to history,
// leaving a USD watch's history mixed with a raw JPY point even though its
// scalars stayed correctly labeled. RecordPrice now rejects a mismatched
// observation outright -- no append, no sync -- since a real currency
// change must go through the full reset the check.go poll path performs,
// which this raw recorder does not do.
func TestRecordPriceRejectsCurrencyMismatch(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	id, _, err := store.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN", Currency: "USD"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := store.RecordPrice(id, 450, "USD"); err != nil {
		t.Fatalf("record price: %v", err)
	}
	// Mismatched currency: must be rejected, not appended.
	if err := store.RecordPrice(id, 50000, "JPY"); err == nil {
		t.Fatal("expected error rejecting a JPY observation on a USD watch, got nil")
	}
	w, _ := store.Get(id)
	if w.LastPrice != 450 {
		t.Errorf("LastPrice = %v, want 450 unchanged (rejected JPY observation must not overwrite a USD watch's scalars)", w.LastPrice)
	}
	if w.LowestPrice != 450 {
		t.Errorf("LowestPrice = %v, want 450 unchanged", w.LowestPrice)
	}
	if got := store.History(id); len(got) != 1 {
		t.Errorf("history = %d points, want 1 (the mismatched observation must not be appended at all)", len(got))
	}
}

// TestRecordPriceAdoptsCurrencyOnFirstObservation is the regression test for
// the other half of GPT's round-20 finding: a currencyless watch given a
// labeled observation previously never adopted that currency, so a LATER
// poll in the SAME currency misclassified this very history as an
// unknown-currency-with-history mismatch (checkOneWithWebhookContext's
// round-19 guard) and purged it. The first observation must adopt the
// recorded currency as the watch's baseline, mirroring the check.go poll
// path's own first-quote handling.
func TestRecordPriceAdoptsCurrencyOnFirstObservation(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	id, _, err := store.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := store.RecordPrice(id, 450, "EUR"); err != nil {
		t.Fatalf("record price: %v", err)
	}
	w, _ := store.Get(id)
	if w.Currency != "EUR" {
		t.Errorf("Currency = %q, want EUR adopted from the first observation", w.Currency)
	}

	checker := &stubPriceChecker{price: 400, currency: "EUR"}
	r := checkOne(context.Background(), store, checker, w)
	if r.Error != nil {
		t.Fatalf("unexpected error: %v", r.Error)
	}
	if r.PriceDrop != -50 {
		t.Errorf("PriceDrop = %v, want -50 (a genuine drop from the adopted EUR baseline, not a currency-mismatch reset)", r.PriceDrop)
	}
}

// TestRecordPriceRejectsMalformedCurrency is the regression test for GPT's
// round-20 finding: RecordPrice only trimmed and uppercased the recorded
// currency, so a malformed value like "EU R" was accepted as real and could
// poison the watch's Currency/history with a format that never validates
// against a real quote again.
func TestRecordPriceRejectsMalformedCurrency(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	id, _, err := store.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := store.RecordPrice(id, 450, "EU R"); err == nil {
		t.Fatal("expected error rejecting malformed currency \"EU R\", got nil")
	}
	if got := store.History(id); len(got) != 0 {
		t.Errorf("history = %d points, want 0 (malformed-currency observation must not be appended)", len(got))
	}
}

// TestCheckRejectsAbsentCurrencyOverwritingKnown is the regression test for
// GPT's round-19 tail finding: a genuinely absent provider currency (not
// whitespace-garbage, which the round-18 guard already rejects) was still
// accepted and silently overwrote an already-known watch currency.
func TestCheckRejectsAbsentCurrencyOverwritingKnown(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	id, _, err := store.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN", Currency: "USD"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := store.RecordPrice(id, 450, "USD"); err != nil {
		t.Fatalf("record price: %v", err)
	}
	w, _ := store.Get(id)

	checker := &stubPriceChecker{price: 400, currency: ""}
	r := checkOne(context.Background(), store, checker, w)
	if r.Error == nil {
		t.Fatalf("expected error rejecting an absent currency on a watch already tracking USD, got nil")
	}
	updated, _ := store.Get(id)
	if updated.Currency != "USD" {
		t.Errorf("Currency = %q, want USD unchanged (must not be blanked by an absent-currency quote)", updated.Currency)
	}
}

// TestCheckRejectsMalformedCurrency is the regression test for GPT's
// round-20 finding: a value like "EU R" survives trim+uppercase as a
// non-empty string, so the pre-round-20 guard (which only distinguished
// "empty" from "non-empty") accepted it as a real currency, clearing alert
// thresholds and purging history from a single malformed provider response.
func TestCheckRejectsMalformedCurrency(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	id, _, err := store.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "USD"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := store.RecordPrice(id, 180, "USD"); err != nil {
		t.Fatalf("record price: %v", err)
	}

	checker := &stubPriceChecker{price: 170, currency: "EU R"}
	w, _ := store.Get(id)
	r := checkOne(context.Background(), store, checker, w)
	if r.Error == nil {
		t.Fatal("check with malformed currency \"EU R\" returned no error, want rejection")
	}

	got, ok := store.Get(id)
	if !ok {
		t.Fatalf("watch not found after check")
	}
	if got.Currency != "USD" {
		t.Errorf("Currency = %q, want USD unchanged (a malformed-currency quote must not overwrite real state)", got.Currency)
	}
	if got.BelowPrice != 200 {
		t.Errorf("BelowPrice = %v, want 200 unchanged (a rejected quote must not clear the alert threshold)", got.BelowPrice)
	}
}

// TestCheckRoomRejectsMalformedCurrency mirrors the flight-path test above
// for the room-watch selection filter.
func TestCheckRoomRejectsMalformedCurrency(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	w := Watch{
		Type:         "room",
		HotelName:    "City Hotel",
		RoomKeywords: []string{"double"},
		DepartDate:   "2099-07-01",
		ReturnDate:   "2099-07-08",
	}
	id, _, err := store.Add(w)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	w.ID = id

	checker := &stubRoomChecker{matches: []RoomMatch{
		{Name: "Malformed Room", Price: 100, Currency: "EU R"},
	}}
	r := checkRoom(context.Background(), store, checker, w)
	if r.Error == nil {
		t.Fatal("check with malformed currency \"EU R\" returned no error, want rejection")
	}
}

// TestCheckRoomSelectionIsOrderIndependentAcrossValidCurrencies is the
// regression test for GPT's round-20 finding: filter-then-select-minimum
// picked the raw cheapest across ALL usable matches even when they carried
// DIFFERENT valid currencies, so a USD watch quoted [180 EUR, 180 USD] chose
// whichever currency the provider listed first -- EUR-first triggered the
// destructive currency-change branch, USD-first produced a genuine
// same-currency price-drop alert, for the identical input set reordered.
// Selection must prefer the watch's own currency regardless of order.
func TestCheckRoomSelectionIsOrderIndependentAcrossValidCurrencies(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	w := Watch{
		Type:         "room",
		HotelName:    "City Hotel",
		RoomKeywords: []string{"double"},
		DepartDate:   "2099-07-01",
		ReturnDate:   "2099-07-08",
		Currency:     "USD",
		LastPrice:    200,
		LowestPrice:  200,
	}
	id, _, err := store.Add(w)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	w.ID = id

	orderings := [][]RoomMatch{
		{{Name: "EUR Room", Price: 180, Currency: "EUR"}, {Name: "USD Room", Price: 180, Currency: "USD"}},
		{{Name: "USD Room", Price: 180, Currency: "USD"}, {Name: "EUR Room", Price: 180, Currency: "EUR"}},
	}
	for _, matches := range orderings {
		checker := &stubRoomChecker{matches: matches}
		r := checkRoom(context.Background(), store, checker, w)
		if r.Error != nil {
			t.Fatalf("unexpected error: %v", r.Error)
		}
		if r.Currency != "USD" {
			t.Errorf("Currency = %q, want USD (the watch's own currency, regardless of provider order)", r.Currency)
		}
		if r.NewPrice != 180 {
			t.Errorf("NewPrice = %v, want 180", r.NewPrice)
		}
	}
}

// TestCheckRoomAbsentCurrencyDoesNotStrandValidSibling is the regression
// test for GPT's round-20 finding: a genuinely absent-currency match (price
// but no currency at all, distinct from whitespace-garbage) survived the
// usable filter and could win selection purely on price, hiding a real
// valid-currency sibling -- e.g. {100, ""} stranding {120, "USD"}
// indefinitely.
func TestCheckRoomAbsentCurrencyDoesNotStrandValidSibling(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	w := Watch{
		Type:         "room",
		HotelName:    "City Hotel",
		RoomKeywords: []string{"double"},
		DepartDate:   "2099-07-01",
		ReturnDate:   "2099-07-08",
	}
	id, _, err := store.Add(w)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	w.ID = id

	checker := &stubRoomChecker{matches: []RoomMatch{
		{Name: "Currencyless Room", Price: 100, Currency: ""},
		{Name: "USD Room", Price: 120, Currency: "USD"},
	}}
	r := checkRoom(context.Background(), store, checker, w)
	if r.Error != nil {
		t.Fatalf("unexpected error: %v", r.Error)
	}
	if r.NewPrice != 120 {
		t.Errorf("NewPrice = %v, want 120 (the valid-currency sibling, not the cheaper currencyless match)", r.NewPrice)
	}
	if r.Currency != "USD" {
		t.Errorf("Currency = %q, want USD", r.Currency)
	}
}

// TestNotifyRoomOmitsRejectedQuotesFromDisplay is the regression test for
// GPT's round-20 finding: notifyRoom rendered r.RoomMatches straight from
// the unfiltered slice CheckResult was initialized with, so a
// whitespace-currency offer rejected for persistence was still printed and
// counted as "available."
func TestNotifyRoomOmitsRejectedQuotesFromDisplay(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	w := Watch{
		Type:         "room",
		HotelName:    "City Hotel",
		RoomKeywords: []string{"double"},
		DepartDate:   "2099-07-01",
		ReturnDate:   "2099-07-08",
	}
	id, _, err := store.Add(w)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	w.ID = id

	checker := &stubRoomChecker{matches: []RoomMatch{
		{Name: "Suspect Room", Price: 90, Currency: "   "},
		{Name: "Valid Room", Price: 120, Currency: "EUR"},
	}}
	r := checkRoom(context.Background(), store, checker, w)
	if r.Error != nil {
		t.Fatalf("unexpected error: %v", r.Error)
	}
	if len(r.RoomMatches) != 1 {
		t.Fatalf("RoomMatches = %d, want 1 (the rejected whitespace-currency quote must not be reported)", len(r.RoomMatches))
	}
	if r.RoomMatches[0].Name != "Valid Room" {
		t.Errorf("RoomMatches[0].Name = %q, want %q", r.RoomMatches[0].Name, "Valid Room")
	}
}
