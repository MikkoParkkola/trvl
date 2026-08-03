package watch

import (
	"testing"
	"time"
)

// The bug this file exists for: Store.Add was a bare append, so every
// watch_price call added another row for the same route. One real store reached
// 468 permanently-active watches covering 4 distinct routes (HEL->BCN alone was
// watched 319 times), and the scheduler re-checked every one against live
// providers every 30 minutes, forever.
func TestStoreAddIsIdempotentOnTarget(t *testing.T) {
	s := NewStore(t.TempDir())

	first, _, err := s.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "EUR"})
	if err != nil {
		t.Fatalf("first add: %v", err)
	}

	// Same route asked for again: same intent, not a second watch.
	second, _, err := s.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "EUR"})
	if err != nil {
		t.Fatalf("second add: %v", err)
	}

	if got := len(s.List()); got != 1 {
		t.Fatalf("re-watching one route produced %d watches, want 1", got)
	}
	if first != second {
		t.Errorf("re-watch returned a new id %q, want the existing %q", second, first)
	}
}

// Re-watching a route the agent already watched must not throw away the price
// history that makes a long-running watch worth anything.
func TestStoreAddPreservesAccumulatedStateOnRewatch(t *testing.T) {
	s := NewStore(t.TempDir())

	id, _, err := s.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "EUR"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	// Simulate months of observation.
	checked := time.Now().Add(-2 * time.Hour)
	for i := range s.watches {
		if s.watches[i].ID == id {
			s.watches[i].LowestPrice = 85
			s.watches[i].LastPrice = 120
			s.watches[i].BaselinePrice = 240
			s.watches[i].LastCheck = checked
		}
	}
	created := s.watches[0].CreatedAt
	// #553: Add now reloads committed state inside its cross-process
	// transaction, so simulated accumulated state must be persisted (as it
	// always would be in production, via RecordPrice/UpdateWatchAndRecordPrice)
	// rather than left as an unsaved in-memory mutation.
	if err := s.Save(); err != nil {
		t.Fatalf("save simulated state: %v", err)
	}

	if _, _, err := s.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 150, Currency: "EUR"}); err != nil {
		t.Fatalf("re-add: %v", err)
	}

	w := s.List()[0]
	if w.LowestPrice != 85 || w.LastPrice != 120 || w.BaselinePrice != 240 {
		t.Errorf("re-watch discarded price history: lowest=%v last=%v baseline=%v",
			w.LowestPrice, w.LastPrice, w.BaselinePrice)
	}
	if !w.LastCheck.Equal(checked) {
		t.Errorf("re-watch reset LastCheck to %v, want %v", w.LastCheck, checked)
	}
	if !w.CreatedAt.Equal(created) {
		t.Errorf("re-watch reset CreatedAt to %v, want %v", w.CreatedAt, created)
	}
	// The adjustable part DOES update.
	if w.BelowPrice != 150 {
		t.Errorf("re-watch did not update target price: got %v, want 150", w.BelowPrice)
	}
}

// A re-watch that omits optional settings must not silently delete them.
func TestStoreAddRewatchDoesNotClearOmittedSettings(t *testing.T) {
	s := NewStore(t.TempDir())

	if _, _, err := s.Add(Watch{
		Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "EUR",
		WebhookURL: "https://example.test/hook", AlertDropPct: 15,
	}); err != nil {
		t.Fatalf("add: %v", err)
	}

	if _, _, err := s.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "EUR"}); err != nil {
		t.Fatalf("re-add: %v", err)
	}

	w := s.List()[0]
	if w.WebhookURL != "https://example.test/hook" {
		t.Errorf("re-watch cleared webhook: %q", w.WebhookURL)
	}
	if w.AlertDropPct != 15 {
		t.Errorf("re-watch cleared alert threshold: %v", w.AlertDropPct)
	}
}

// A currency-changing re-watch that omits a fresh absolute threshold must not
// leave the OLD currency's absolute threshold silently attached to the NEW
// currency. Found by adversarial review, 2026-07-30 (round 15): applyIntent
// only ever overwrote BelowPrice/AlertDropAbs when the caller supplied a new
// positive value, so a JPY->EUR re-watch that only touches AlertDropPct kept
// comparing quotes against a stale JPY BelowPrice as if it were EUR.
func TestStoreAddRewatchClearsAbsoluteThresholdsOnCurrencyChange(t *testing.T) {
	s := NewStore(t.TempDir())

	if _, _, err := s.Add(Watch{
		Type: "flight", Origin: "HEL", Destination: "BCN",
		Currency: "JPY", BelowPrice: 20000, AlertDropAbs: 2000,
	}); err != nil {
		t.Fatalf("add: %v", err)
	}

	// Re-watch switches currency to EUR and only sets AlertDropPct -- BelowPrice
	// and AlertDropAbs are omitted (zero-value), same as any caller that isn't
	// specifically re-supplying an absolute threshold on every re-watch call.
	if _, _, err := s.Add(Watch{
		Type: "flight", Origin: "HEL", Destination: "BCN",
		Currency: "EUR", AlertDropPct: 10,
	}); err != nil {
		t.Fatalf("re-add: %v", err)
	}

	w := s.List()[0]
	if w.Currency != "EUR" {
		t.Fatalf("Currency = %q, want EUR", w.Currency)
	}
	if w.BelowPrice != 0 {
		t.Errorf("BelowPrice = %v, want 0 (stale JPY 20000 must not survive a currency change to EUR)", w.BelowPrice)
	}
	if w.AlertDropAbs != 0 {
		t.Errorf("AlertDropAbs = %v, want 0 (stale JPY 2000 must not survive a currency change to EUR)", w.AlertDropAbs)
	}
	if w.AlertDropPct != 10 {
		t.Errorf("AlertDropPct = %v, want 10 (percentage is currency-invariant and should be applied)", w.AlertDropPct)
	}
}

// Round 17 (adversarial review, 2026-07-30): applyIntent has the identical
// regression check.go's currencyMismatch handling had -- zeroing
// AlertDropAbs on a currency-changing re-watch that was the watch's ONLY
// threshold (AlertDropPct <= 0) must not let pricealert.Evaluate's
// Threshold.effective() silently substitute DefaultDropPercent (10%) on the
// next poll. AlertDropAbsClearedByCurrency must be set when the re-watch
// omits a fresh threshold, and cleared the instant a later re-watch
// re-supplies either limb.
func TestStoreAddRewatchMarksAbsOnlyThresholdClearedByCurrency(t *testing.T) {
	s := NewStore(t.TempDir())

	if _, _, err := s.Add(Watch{
		Type: "flight", Origin: "HEL", Destination: "BCN",
		Currency: "JPY", AlertDropAbs: 2000, // absolute-only: AlertDropPct never set.
	}); err != nil {
		t.Fatalf("add: %v", err)
	}

	// Re-watch switches currency and supplies no fresh threshold at all.
	if _, _, err := s.Add(Watch{
		Type: "flight", Origin: "HEL", Destination: "BCN",
		Currency: "EUR",
	}); err != nil {
		t.Fatalf("re-add: %v", err)
	}

	w := s.List()[0]
	if w.AlertDropAbs != 0 {
		t.Errorf("AlertDropAbs = %v, want 0", w.AlertDropAbs)
	}
	if !w.AlertDropAbsClearedByCurrency {
		t.Fatalf("AlertDropAbsClearedByCurrency = false, want true (the watch's only threshold was force-cleared with no replacement)")
	}

	// A THIRD re-watch that finally supplies a fresh absolute threshold in
	// the new currency must clear the marker and resume normal evaluation.
	if _, _, err := s.Add(Watch{
		Type: "flight", Origin: "HEL", Destination: "BCN",
		Currency: "EUR", AlertDropAbs: 30,
	}); err != nil {
		t.Fatalf("re-add 2: %v", err)
	}

	w = s.List()[0]
	if w.AlertDropAbs != 30 {
		t.Errorf("AlertDropAbs = %v, want 30", w.AlertDropAbs)
	}
	if w.AlertDropAbsClearedByCurrency {
		t.Errorf("AlertDropAbsClearedByCurrency = true, want false (a fresh threshold was supplied -- alerting must resume)")
	}
}

// Genuinely different targets must still create separate watches. Dedup that is
// too aggressive is a worse bug than no dedup.
func TestStoreAddKeepsDistinctTargetsSeparate(t *testing.T) {
	s := NewStore(t.TempDir())

	distinct := []Watch{
		{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "EUR"},
		{Type: "flight", Origin: "HEL", Destination: "PRG", BelowPrice: 200, Currency: "EUR"},
		{Type: "flight", Origin: "PRG", Destination: "AMS", BelowPrice: 200, Currency: "EUR"},
		{Type: "flight", Origin: "HEL", Destination: "BCN", DepartDate: "2026-09-01", BelowPrice: 200, Currency: "EUR"},
		{Type: "flight", Origin: "HEL", Destination: "BCN", DepartFrom: "2026-09-01", DepartTo: "2026-09-30", BelowPrice: 200, Currency: "EUR"},
		{Type: "room", Destination: "AMS", HotelName: "Hilton", RoomKeywords: []string{"king"}, DepartDate: "2026-06-15", ReturnDate: "2026-06-18", BelowPrice: 200, Currency: "EUR"},
		{Type: "room", Destination: "AMS", HotelName: "Marriott", RoomKeywords: []string{"king"}, DepartDate: "2026-06-15", ReturnDate: "2026-06-18", BelowPrice: 200, Currency: "EUR"},
	}
	for i, w := range distinct {
		if _, _, err := s.Add(w); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}

	if got := len(s.List()); got != len(distinct) {
		t.Fatalf("distinct targets collapsed: got %d watches, want %d", got, len(distinct))
	}
}

// Room keyword order is not part of a watch's meaning.
func TestStoreAddRoomKeywordOrderDoesNotSplitWatches(t *testing.T) {
	s := NewStore(t.TempDir())
	base := Watch{
		Type: "room", Destination: "AMS", HotelName: "Hilton",
		DepartDate: "2026-06-15", ReturnDate: "2026-06-18", BelowPrice: 200, Currency: "EUR",
	}

	a := base
	a.RoomKeywords = []string{"king", "balcony"}
	b := base
	b.RoomKeywords = []string{"balcony", "king"}

	if _, _, err := s.Add(a); err != nil {
		t.Fatalf("add a: %v", err)
	}
	if _, _, err := s.Add(b); err != nil {
		t.Fatalf("add b: %v", err)
	}

	if got := len(s.List()); got != 1 {
		t.Errorf("keyword reordering created %d watches, want 1", got)
	}
}

// Opportunity watches carry no route, so their identity is the window config.
// 147 identical opportunity watches accumulated in the real store.
func TestStoreAddDedupesOpportunityWatchesByWindow(t *testing.T) {
	s := NewStore(t.TempDir())
	opp := Watch{Type: "opportunity", WindowFrom: "next_30d", WindowTo: "next_90d", MinScore: 85, MinNights: 3, MaxNights: 14}

	for i := 0; i < 10; i++ {
		if _, _, err := s.Add(opp); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}
	if got := len(s.List()); got != 1 {
		t.Errorf("10 identical opportunity watches produced %d rows, want 1", got)
	}

	different := opp
	different.MinScore = 90
	if _, _, err := s.Add(different); err != nil {
		t.Fatalf("add different: %v", err)
	}
	if got := len(s.List()); got != 2 {
		t.Errorf("a different min_score must be its own watch: got %d, want 2", got)
	}
}

// The scenario end to end: repeated agent sessions watching a handful of routes
// must not grow the store without bound.
func TestStoreAddDoesNotAccumulateAcrossSessions(t *testing.T) {
	s := NewStore(t.TempDir())
	routes := [][2]string{{"HEL", "BCN"}, {"HEL", "PRG"}, {"PRG", "AMS"}}

	for session := 0; session < 200; session++ {
		for _, r := range routes {
			if _, _, err := s.Add(Watch{
				Type: "flight", Origin: r[0], Destination: r[1], BelowPrice: 200, Currency: "EUR",
			}); err != nil {
				t.Fatalf("session %d: %v", session, err)
			}
		}
	}

	if got := len(s.List()); got != len(routes) {
		t.Fatalf("200 sessions over %d routes produced %d watches, want %d (this is the 468-watch bug)",
			len(routes), got, len(routes))
	}
}

// Watch-keyed price points were exempt from every cap — the only unbounded
// corpus in the store. One real price-history.json reached 320,028 points in
// 41MB (319,966 watch-keyed, 36 route-keyed), which is what made each
// `trvl mcp` process cost ~686MB resident.
func TestRecordPriceIsBoundedPerWatch(t *testing.T) {
	s := NewStore(t.TempDir())
	id, _, err := s.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "EUR"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	for i := 0; i < maxObservationsPerWatch+500; i++ {
		if err := s.RecordPrice(id, float64(100+i%50), "EUR"); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}

	got := len(s.History(id))
	if got > maxObservationsPerWatch {
		t.Errorf("watch history grew to %d points, cap is %d", got, maxObservationsPerWatch)
	}
	if got != maxObservationsPerWatch {
		t.Errorf("expected the cap to be filled exactly: got %d, want %d", got, maxObservationsPerWatch)
	}
}

// Eviction must drop the OLDEST points: recent prices are what sparklines and
// drop detection read.
func TestRecordPriceEvictsOldestFirst(t *testing.T) {
	s := NewStore(t.TempDir())
	id, _, err := s.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "EUR"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	total := maxObservationsPerWatch + 100
	for i := 0; i < total; i++ {
		if err := s.RecordPrice(id, float64(i), "EUR"); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}

	h := s.History(id)
	if len(h) == 0 {
		t.Fatal("no history retained")
	}
	// The newest point must survive; the oldest must not.
	if h[len(h)-1].Price != float64(total-1) {
		t.Errorf("newest point missing: got %v, want %v", h[len(h)-1].Price, total-1)
	}
	if h[0].Price != float64(total-maxObservationsPerWatch) {
		t.Errorf("wrong eviction boundary: oldest retained is %v, want %v",
			h[0].Price, total-maxObservationsPerWatch)
	}
}

// A watch's all-time low lives on the Watch record, so bounding history cannot
// lose it. This is what makes eviction safe.
func TestWatchLowestPriceSurvivesHistoryEviction(t *testing.T) {
	s := NewStore(t.TempDir())
	id, _, err := s.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "EUR"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	for i := range s.watches {
		if s.watches[i].ID == id {
			s.watches[i].LowestPrice = 42
			s.watches[i].CheapestDate = "2026-05-01"
		}
	}
	// #553: RecordPrice reloads committed state inside its cross-process
	// transaction; persist the simulated all-time low first.
	if err := s.Save(); err != nil {
		t.Fatalf("save simulated state: %v", err)
	}

	for i := 0; i < maxObservationsPerWatch+50; i++ {
		if err := s.RecordPrice(id, 300, "EUR"); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}

	w := s.List()[0]
	if w.LowestPrice != 42 || w.CheapestDate != "2026-05-01" {
		t.Errorf("history eviction lost the all-time low: lowest=%v date=%q",
			w.LowestPrice, w.CheapestDate)
	}
}

// Bounding watch history must not disturb the separately-capped route corpus.
func TestWatchEvictionLeavesRouteObservationsAlone(t *testing.T) {
	s := NewStore(t.TempDir())
	id, _, err := s.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "EUR"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := s.RecordObservation("HEL-BCN", 199, "EUR"); err != nil {
		t.Fatalf("observation: %v", err)
	}

	for i := 0; i < maxObservationsPerWatch+200; i++ {
		if err := s.RecordPrice(id, float64(100+i), "EUR"); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}

	if got := len(s.RouteHistory("HEL-BCN")); got != 1 {
		t.Errorf("route observation was collateral damage: %d retained, want 1", got)
	}
}

// isActive() returned true unconditionally for dateless route watches, so they
// were checked forever. Combined with the missing dedup, one store reached 468
// permanently-active route watches.
func TestRouteWatchExpiresAfterTTL(t *testing.T) {
	fresh := Watch{Type: "flight", Origin: "HEL", Destination: "BCN", RenewedAt: time.Now()}
	stale := Watch{Type: "flight", Origin: "HEL", Destination: "BCN", RenewedAt: time.Now().Add(-routeWatchTTL - time.Hour)}
	today := time.Now().Format("2006-01-02")

	if !isActive(fresh, today) {
		t.Error("a recently renewed route watch must stay active")
	}
	if isActive(stale, today) {
		t.Errorf("a route watch untouched for over %v must expire", routeWatchTTL)
	}
}

// Re-watching is the renewal signal: a route in active use never ages out.
func TestRewatchRenewsRouteWatch(t *testing.T) {
	s := NewStore(t.TempDir())
	if _, _, err := s.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "EUR"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	// Age it past the TTL.
	s.watches[0].RenewedAt = time.Now().Add(-routeWatchTTL - time.Hour)
	today := time.Now().Format("2006-01-02")
	if isActive(s.watches[0], today) {
		t.Fatal("precondition: aged watch should be inactive")
	}

	if _, _, err := s.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "EUR"}); err != nil {
		t.Fatalf("re-add: %v", err)
	}
	if !isActive(s.watches[0], today) {
		t.Error("re-watching must renew an expired route watch")
	}
}

// Dated watches keep expiring on their travel date, not on the route TTL.
func TestDatedWatchExpiryIsUnchangedByRouteTTL(t *testing.T) {
	today := time.Now().Format("2006-01-02")
	future := time.Now().AddDate(0, 2, 0).Format("2006-01-02")
	past := time.Now().AddDate(0, -2, 0).Format("2006-01-02")

	// Ancient renewal, but the trip is still ahead: must stay active.
	upcoming := Watch{Type: "flight", Origin: "HEL", Destination: "BCN",
		DepartDate: future, RenewedAt: time.Now().Add(-routeWatchTTL - time.Hour)}
	if !isActive(upcoming, today) {
		t.Error("an upcoming dated watch must not be expired by the route TTL")
	}

	// Renewed today, but the trip has been and gone: must be inactive.
	departed := Watch{Type: "flight", Origin: "HEL", Destination: "BCN",
		DepartDate: past, RenewedAt: time.Now()}
	if isActive(departed, today) {
		t.Error("a past dated watch must stay expired regardless of renewal")
	}
}

// Upgrading must not retroactively expire watches the user still wants: legacy
// records have no RenewedAt and get a full TTL from first load.
func TestLoadGrantsLegacyWatchesAFreshTTL(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if _, _, err := s.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "EUR"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	// Simulate a pre-upgrade record: created long ago, no renewal stamp.
	s.watches[0].CreatedAt = time.Now().Add(-2 * routeWatchTTL)
	s.watches[0].RenewedAt = time.Time{}
	if err := s.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	fresh := NewStore(dir)
	if err := fresh.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, err := fresh.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if !isActive(fresh.List()[0], time.Now().Format("2006-01-02")) {
		t.Error("migration mass-expired a legacy route watch instead of granting a fresh TTL")
	}
}

// TestStoreAddResetsAccumulatedStateOnCurrencyChange guards against the bug
// an adversarial review found on 2026-07-28: Currency is deliberately not
// part of SameTarget's identity (a re-watch with a new currency updates the
// existing watch rather than forking a rival one), but applyIntent used to
// overwrite Currency without resetting LastPrice, LowestPrice,
// BaselinePrice, LastAlertedPrice, CheapestDate, or this watch's price
// history -- all of which are numeric values denominated in the OLD
// currency. A ~20,000 JPY watch re-added in EUR would then compare a ~180
// EUR next check against a stale "last price" of 20,000, fabricating a
// ~99% drop and firing false alerts/webhooks off it.
func TestStoreAddResetsAccumulatedStateOnCurrencyChange(t *testing.T) {
	s := NewStore(t.TempDir())

	id, _, err := s.Add(Watch{Type: "flight", Origin: "HEL", Destination: "NRT", BelowPrice: 15000, Currency: "JPY"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	// Simulate an established JPY-denominated watch: accumulated state plus
	// recorded history, all in JPY's numeric scale.
	for i := range s.watches {
		if s.watches[i].ID == id {
			s.watches[i].LastPrice = 20000
			s.watches[i].LowestPrice = 18500
			s.watches[i].CheapestDate = "2026-08-01"
			s.watches[i].BaselinePrice = 22000
			s.watches[i].LastAlertedPrice = 19000
		}
	}
	if err := s.RecordPrice(id, 20000, "JPY"); err != nil {
		t.Fatalf("record price: %v", err)
	}
	if len(s.History(id)) == 0 {
		t.Fatal("setup: expected at least one recorded price point")
	}

	// Re-watch the SAME target (SameTarget ignores Currency), switching to EUR.
	if _, created, err := s.Add(Watch{Type: "flight", Origin: "HEL", Destination: "NRT", BelowPrice: 15000, Currency: "EUR"}); err != nil {
		t.Fatalf("re-add with new currency: %v", err)
	} else if created {
		t.Fatal("re-watch with a new currency must update the existing watch, not create a new one")
	}

	w, ok := s.Get(id)
	if !ok {
		t.Fatal("watch vanished after currency re-watch")
	}
	if w.Currency != "EUR" {
		t.Errorf("Currency = %q, want EUR", w.Currency)
	}
	if w.LastPrice != 0 {
		t.Errorf("LastPrice = %v, want 0 (stale JPY value must not survive a currency change)", w.LastPrice)
	}
	if w.LowestPrice != 0 {
		t.Errorf("LowestPrice = %v, want 0 (stale JPY value must not survive a currency change)", w.LowestPrice)
	}
	if w.CheapestDate != "" {
		t.Errorf("CheapestDate = %q, want empty (tied to the reset LowestPrice)", w.CheapestDate)
	}
	if w.BaselinePrice != 0 {
		t.Errorf("BaselinePrice = %v, want 0 (stale JPY value must not survive a currency change)", w.BaselinePrice)
	}
	if w.LastAlertedPrice != 0 {
		t.Errorf("LastAlertedPrice = %v, want 0 (stale JPY value must not survive a currency change)", w.LastAlertedPrice)
	}
	if got := s.History(id); len(got) != 0 {
		t.Errorf("history for this watch = %d points, want 0 (old-currency history must not survive a currency change)", len(got))
	}
	// The user-set threshold is untouched by this fix -- same
	// already-adjustable behavior as BelowPrice always had.
	if w.BelowPrice != 15000 {
		t.Errorf("BelowPrice = %v, want 15000 (unrelated to the currency reset)", w.BelowPrice)
	}
}

// TestStoreAddPreservesStateWhenCurrencyIsUnchanged is the control for the
// test above: re-watching WITHOUT changing currency must keep the existing
// "preserve accumulated state" behavior exactly as before this fix.
func TestStoreAddPreservesStateWhenCurrencyIsUnchanged(t *testing.T) {
	s := NewStore(t.TempDir())

	id, _, err := s.Add(Watch{Type: "flight", Origin: "HEL", Destination: "NRT", BelowPrice: 500, Currency: "EUR"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	for i := range s.watches {
		if s.watches[i].ID == id {
			s.watches[i].LastPrice = 450
			s.watches[i].LowestPrice = 400
		}
	}
	// #553: RecordPrice reloads committed state inside its cross-process
	// transaction; persist the simulated prior observation first.
	if err := s.Save(); err != nil {
		t.Fatalf("save simulated state: %v", err)
	}
	if err := s.RecordPrice(id, 450, "EUR"); err != nil {
		t.Fatalf("record price: %v", err)
	}

	// Same currency, only the threshold changes.
	if _, _, err := s.Add(Watch{Type: "flight", Origin: "HEL", Destination: "NRT", BelowPrice: 350, Currency: "EUR"}); err != nil {
		t.Fatalf("re-add: %v", err)
	}

	w, _ := s.Get(id)
	if w.LastPrice != 450 || w.LowestPrice != 400 {
		t.Errorf("re-watch without a currency change discarded state: last=%v lowest=%v", w.LastPrice, w.LowestPrice)
	}
	if got := s.History(id); len(got) != 1 {
		t.Errorf("history for this watch = %d points, want 1 (unchanged currency must not purge history)", len(got))
	}
}

// TestCompactHistoryLockedFiltersCurrencyMismatchBeforeRetentionCap is the
// regression test for round 18's reordering of compactHistoryLocked: the
// orphan/currency-mismatch filter must run BEFORE the per-watch retention
// cap, not after. Provider currency can flip poll-to-poll, so a
// stale-currency point is not guaranteed to be older (in slice/chronological
// order) than every currency-valid point under the same watch. If the cap
// (evictOldestLocked, recency-only) ran first, it could evict a real,
// currency-valid point purely for being the oldest by position -- while a
// currency-mismatched point survives the cap only to be dropped by the
// filter afterward anyway. Net effect of the bug: the store ends up BELOW
// its own retention cap, having sacrificed a valid point for nothing.
func TestCompactHistoryLockedFiltersCurrencyMismatchBeforeRetentionCap(t *testing.T) {
	s := NewStore(t.TempDir())

	id, _, err := s.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "USD"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	// Fill to exactly the retention cap with valid USD points, oldest first
	// (slice order mirrors chronological append order, as it does in
	// production).
	base := time.Now().Add(-24 * time.Hour)
	s.history = nil
	for i := 0; i < maxObservationsPerWatch; i++ {
		s.history = append(s.history, PricePoint{
			WatchID:   id,
			Price:     float64(100 + i),
			Currency:  "USD",
			Timestamp: base.Add(time.Duration(i) * time.Minute),
		})
	}
	// One extra point: newest by slice position, but in the watch's stale
	// (mismatched) currency -- e.g. left over from before a provider
	// currency flip. This is the one point that must not survive, no matter
	// how recent it looks.
	s.history = append(s.history, PricePoint{
		WatchID:   id,
		Price:     999,
		Currency:  "EUR",
		Timestamp: base.Add(time.Duration(maxObservationsPerWatch) * time.Minute),
	})

	s.compactHistoryLocked()

	got := s.History(id)
	if len(got) != maxObservationsPerWatch {
		t.Fatalf("history after compact = %d points, want %d (currency filter must run before the retention cap, not sacrifice a valid point to it)", len(got), maxObservationsPerWatch)
	}
	for _, p := range got {
		if p.Currency != "USD" {
			t.Errorf("surviving point has Currency=%q, want all-USD (stale-currency point must not survive the cap at a valid point's expense)", p.Currency)
		}
	}
}
