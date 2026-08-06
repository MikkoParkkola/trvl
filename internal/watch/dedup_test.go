package watch

import (
	"strconv"
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
	// Persisted, not just poked in memory: store mutations are transactional
	// and reload committed state from disk, so an unsaved in-memory edit is
	// (correctly) invisible to the call under test.
	if err := s.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	created := s.watches[0].CreatedAt

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

// newCappedStore returns a loaded store whose per-watch retention cap is small,
// plus the cap in force.
//
// Every RecordPrice is a committed transaction, so a test that pushes the
// production cap of 1000 past its limit performs ~1500 disk-syncing writes. Four
// such tests were 54s of this package's 83s locally, and on 2026-08-06 the
// package hit Go's 600s per-package ceiling on a CI runner and failed a merge on
// a PR that had not touched it (trvl#585, run 31055857360).
//
// Driving a cap of 20 over its limit proves exactly what driving 1000 over its
// limit proves, for 2% of the writes. The rule this follows: invert the budget
// rather than generate the load. A test that races a real clock is measuring the
// runner, not the code.
//
// The override is the same one operators get (TRVL_WATCH_MAX_POINTS_PER_WATCH),
// so this exercises the shipping configuration path rather than a test-only back
// door.
//
// It returns the store already Loaded, and that is the whole reason it exists as
// a constructor rather than a bare setenv helper: retention is read ONLY by
// Load (store.go:104). withTxn reloads committed state through loadLocked,
// which does not re-read it. So a store built with NewStore and never loaded
// silently keeps the compiled 1000 default, the override appears to do nothing,
// and the test still passes -- it just writes 50x more than intended. Handing
// back a loaded store makes that mistake unavailable.
//
// t.Setenv is deliberate: it forbids t.Parallel in these tests, and they must
// not run in parallel anyway because they each drive a shared cap.
func newCappedStore(t *testing.T) (*Store, int) {
	t.Helper()
	const capacity = 20
	t.Setenv(EnvMaxPointsPerWatch, strconv.Itoa(capacity))
	s := NewStore(t.TempDir())
	if err := s.Load(); err != nil {
		t.Fatalf("load store with %s=%d: %v", EnvMaxPointsPerWatch, capacity, err)
	}
	return s, capacity
}

// The cap helper must actually lower the cap. Without this, a future change to
// where retention is read turns every test below into a slow no-op that still
// passes -- which is exactly the failure this helper was written to escape.
func TestNewCappedStoreReallyLowersTheCap(t *testing.T) {
	s, capacity := newCappedStore(t)
	if capacity >= maxObservationsPerWatch {
		t.Fatalf("test cap %d is not below the production cap %d", capacity, maxObservationsPerWatch)
	}
	if got := s.retentionOrDefault().MaxPointsPerWatch; got != capacity {
		t.Fatalf("store retains %d points per watch, want the injected %d -- the override did not "+
			"reach the store, so every test using this helper is writing 50x what it thinks", got, capacity)
	}
}

// Watch-keyed price points were exempt from every cap — the only unbounded
// corpus in the store. One real price-history.json reached 320,028 points in
// 41MB (319,966 watch-keyed, 36 route-keyed), which is what made each
// `trvl mcp` process cost ~686MB resident.
func TestRecordPriceIsBoundedPerWatch(t *testing.T) {
	s, capacity := newCappedStore(t)
	id, _, err := s.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "EUR"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	// i%3 rather than i%50: with only capacity+10 iterations, i%50 never wraps and
	// every price would be distinct. The original loop ran 1500 times over a cycle
	// of 50, so each price recurred ~30 times, and shrinking the loop silently
	// dropped repeated values from the input. Keeping the cycle SHORTER than the
	// loop preserves that property at the new size. Raised by adversarial review,
	// which asked the right question: does shrinking the cap weaken what the test
	// proves? For the cap boundary, no. For input diversity, it did.
	for i := 0; i < capacity+10; i++ {
		if err := s.RecordPrice(id, float64(100+i%3), "EUR"); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}

	got := len(s.History(id))
	if got > capacity {
		t.Errorf("watch history grew to %d points, cap is %d", got, capacity)
	}
	if got != capacity {
		t.Errorf("expected the cap to be filled exactly: got %d, want %d", got, capacity)
	}
}

// Eviction must drop the OLDEST points: recent prices are what sparklines and
// drop detection read.
func TestRecordPriceEvictsOldestFirst(t *testing.T) {
	s, capacity := newCappedStore(t)
	id, _, err := s.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "EUR"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	total := capacity + 5
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
	if h[0].Price != float64(total-capacity) {
		t.Errorf("wrong eviction boundary: oldest retained is %v, want %v",
			h[0].Price, total-capacity)
	}
}

// A watch's all-time low lives on the Watch record, so bounding history cannot
// lose it. This is what makes eviction safe.
func TestWatchLowestPriceSurvivesHistoryEviction(t *testing.T) {
	s, capacity := newCappedStore(t)
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
	// Persisted, not just poked in memory: store mutations are transactional
	// and reload committed state from disk, so an unsaved in-memory edit is
	// (correctly) invisible to the call under test.
	if err := s.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	for i := 0; i < capacity+5; i++ {
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
	s, capacity := newCappedStore(t)
	id, _, err := s.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "EUR"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := s.RecordObservation("HEL-BCN", 199, "EUR"); err != nil {
		t.Fatalf("observation: %v", err)
	}

	for i := 0; i < capacity+5; i++ {
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

	if !isActive(fresh, today, defaultRetention().RouteTTL) {
		t.Error("a recently renewed route watch must stay active")
	}
	if isActive(stale, today, defaultRetention().RouteTTL) {
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
	if isActive(s.watches[0], today, defaultRetention().RouteTTL) {
		t.Fatal("precondition: aged watch should be inactive")
	}

	if _, _, err := s.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "EUR"}); err != nil {
		t.Fatalf("re-add: %v", err)
	}
	if !isActive(s.watches[0], today, defaultRetention().RouteTTL) {
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
	if !isActive(upcoming, today, defaultRetention().RouteTTL) {
		t.Error("an upcoming dated watch must not be expired by the route TTL")
	}

	// Renewed today, but the trip has been and gone: must be inactive.
	departed := Watch{Type: "flight", Origin: "HEL", Destination: "BCN",
		DepartDate: past, RenewedAt: time.Now()}
	if isActive(departed, today, defaultRetention().RouteTTL) {
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
	if !isActive(fresh.List()[0], time.Now().Format("2006-01-02"), defaultRetention().RouteTTL) {
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
	// Persisted, not just poked in memory: store mutations are transactional
	// and reload committed state from disk, so an unsaved in-memory edit is
	// (correctly) invisible to the call under test.
	if err := s.Save(); err != nil {
		t.Fatalf("save: %v", err)
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

// WATCHID.6 -- adjusting the price on a hotel watch must not reset a custom
// last-minute threshold to the caller's default.
//
// Callers supply LastMinuteDropPct=25 whether or not the request is about
// last-minute mode (the MCP argFloat default and the CLI --last-minute-drop
// flag default both do). applyIntent used to treat any positive value as
// intentional, so a re-watch that only changed the target price stamped 25 over
// a stored 40. That became a main-path bug the moment re-watching became the
// way to change a price. Found by grok second-opinion review, 2026-08-02.
func TestRewatchKeepsCustomLastMinuteThreshold(t *testing.T) {
	s := NewStore(t.TempDir())

	base := Watch{
		Type: "hotel", Destination: "Lisbon",
		DepartFrom: "2027-03-01", DepartTo: "2027-03-05",
		Currency: "EUR", BelowPrice: 200,
		LastMinuteMode: true, LastMinuteDropPct: 40,
	}
	id, _, err := s.Add(base)
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	// Re-watch to change ONLY the price. The caller's default threshold rides
	// along uninvited, exactly as the real MCP and CLI call sites send it.
	adjust := base
	adjust.BelowPrice = 150
	adjust.LastMinuteMode = false
	adjust.LastMinuteDropPct = 25
	if _, _, err := s.Add(adjust); err != nil {
		t.Fatalf("adjust: %v", err)
	}

	w, ok := s.Get(id)
	if !ok {
		t.Fatalf("watch %s vanished", id)
	}
	if w.BelowPrice != 150 {
		t.Errorf("WATCHID.6: target price = %v, want the adjusted 150", w.BelowPrice)
	}
	if w.LastMinuteDropPct != 40 {
		t.Errorf("WATCHID.6: last-minute threshold reset to %v%%, want the stored 40%% -- "+
			"a price adjustment must not carry the caller's default over a custom value",
			w.LastMinuteDropPct)
	}
	if !w.LastMinuteMode {
		t.Errorf("WATCHID.6: last-minute mode was switched off by a price adjustment")
	}
}

// WATCHID.7 -- deliberately changing the last-minute threshold still works.
// Without this, WATCHID.6 could be satisfied by never applying the field at all.
func TestRewatchCanStillChangeLastMinuteThreshold(t *testing.T) {
	s := NewStore(t.TempDir())

	base := Watch{
		Type: "hotel", Destination: "Porto",
		DepartFrom: "2027-04-01", DepartTo: "2027-04-04",
		Currency: "EUR", BelowPrice: 300,
		LastMinuteMode: true, LastMinuteDropPct: 40,
	}
	id, _, err := s.Add(base)
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	change := base
	change.LastMinuteDropPct = 15
	if _, _, err := s.Add(change); err != nil {
		t.Fatalf("re-add: %v", err)
	}

	w, _ := s.Get(id)
	if w.LastMinuteDropPct != 15 {
		t.Errorf("WATCHID.7: last-minute threshold = %v%%, want the requested 15%%", w.LastMinuteDropPct)
	}
}
