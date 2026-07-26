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

	first, err := s.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "EUR"})
	if err != nil {
		t.Fatalf("first add: %v", err)
	}

	// Same route asked for again: same intent, not a second watch.
	second, err := s.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "EUR"})
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

	id, err := s.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "EUR"})
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

	if _, err := s.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 150, Currency: "EUR"}); err != nil {
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

	if _, err := s.Add(Watch{
		Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "EUR",
		WebhookURL: "https://example.test/hook", AlertDropPct: 15,
	}); err != nil {
		t.Fatalf("add: %v", err)
	}

	if _, err := s.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "EUR"}); err != nil {
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
		if _, err := s.Add(w); err != nil {
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

	if _, err := s.Add(a); err != nil {
		t.Fatalf("add a: %v", err)
	}
	if _, err := s.Add(b); err != nil {
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
		if _, err := s.Add(opp); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}
	if got := len(s.List()); got != 1 {
		t.Errorf("10 identical opportunity watches produced %d rows, want 1", got)
	}

	different := opp
	different.MinScore = 90
	if _, err := s.Add(different); err != nil {
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
			if _, err := s.Add(Watch{
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
	id, err := s.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "EUR"})
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
	id, err := s.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "EUR"})
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
	id, err := s.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "EUR"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	for i := range s.watches {
		if s.watches[i].ID == id {
			s.watches[i].LowestPrice = 42
			s.watches[i].CheapestDate = "2026-05-01"
		}
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
	id, err := s.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "EUR"})
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
	if _, err := s.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "EUR"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	// Age it past the TTL.
	s.watches[0].RenewedAt = time.Now().Add(-routeWatchTTL - time.Hour)
	today := time.Now().Format("2006-01-02")
	if isActive(s.watches[0], today) {
		t.Fatal("precondition: aged watch should be inactive")
	}

	if _, err := s.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "EUR"}); err != nil {
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
	if _, err := s.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "EUR"}); err != nil {
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
	if !isActive(fresh.List()[0], time.Now().Format("2006-01-02")) {
		t.Error("upgrade mass-expired a legacy route watch instead of granting a fresh TTL")
	}
}

// Every `trvl mcp` process starts its own scheduler. MCP clients spawn a server
// per session and some leak them: 15 orphaned processes were observed alive at
// once, each running a full round against the same watches — ~7,000 provider
// queries per 30-minute round instead of 468, plus concurrent writes to the same
// JSON files.
func TestSchedulerLockIsExclusivePerDir(t *testing.T) {
	dir := t.TempDir()

	first, held, err := TryLockScheduler(dir)
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	if !held {
		t.Fatal("first caller must win the scheduler lock")
	}
	defer first.Release()

	second, held2, err := TryLockScheduler(dir)
	if err != nil {
		t.Fatalf("second lock returned an error; contention must be a normal outcome: %v", err)
	}
	if held2 {
		second.Release()
		t.Fatal("two processes acquired the scheduler lock for one directory")
	}
}

// Releasing must hand ownership to the next caller, so a restarted scheduler
// takes over rather than leaving price checks permanently unowned.
func TestSchedulerLockIsReacquirableAfterRelease(t *testing.T) {
	dir := t.TempDir()

	first, held, err := TryLockScheduler(dir)
	if err != nil || !held {
		t.Fatalf("first lock: held=%v err=%v", held, err)
	}
	first.Release()

	second, held2, err := TryLockScheduler(dir)
	if err != nil {
		t.Fatalf("re-lock: %v", err)
	}
	if !held2 {
		t.Fatal("lock was not released: a crashed or restarted scheduler would wedge scheduling")
	}
	second.Release()
}

// Separate stores must not contend with each other.
func TestSchedulerLockIsPerDirectory(t *testing.T) {
	a, heldA, err := TryLockScheduler(t.TempDir())
	if err != nil || !heldA {
		t.Fatalf("lock a: held=%v err=%v", heldA, err)
	}
	defer a.Release()

	b, heldB, err := TryLockScheduler(t.TempDir())
	if err != nil || !heldB {
		t.Fatalf("a lock on one directory must not block another: held=%v err=%v", heldB, err)
	}
	b.Release()
}

// Release is defensive: nil and repeat calls must not panic.
func TestSchedulerLockReleaseIsSafeToRepeat(t *testing.T) {
	var nilLock *SchedulerLock
	nilLock.Release()

	l, held, err := TryLockScheduler(t.TempDir())
	if err != nil || !held {
		t.Fatalf("lock: held=%v err=%v", held, err)
	}
	l.Release()
	l.Release()
}

// A scheduler that loses the lock must still shut down cleanly - Stop() blocks
// on the done channel, so a non-scheduling process must not hang on exit.
func TestSchedulerStopReturnsWhenLockNotHeld(t *testing.T) {
	dir := t.TempDir()

	blocker, held, err := TryLockScheduler(dir)
	if err != nil || !held {
		t.Fatalf("blocker lock: held=%v err=%v", held, err)
	}
	defer blocker.Release()

	s := NewScheduler(dir, time.Minute, NoopChecker{})
	s.Start() // must no-op: another owner holds the lock

	done := make(chan struct{})
	go func() { s.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop() hung on a scheduler that never started; every leaked process would hang on exit")
	}
}

// The migration MUST be persisted. Stamping RenewedAt only in memory would
// re-grant a fresh TTL on every load, so a legacy watch could never age out and
// routeWatchTTL would be dead code for exactly the users who need it.
func TestLoadPersistsRenewedAtMigration(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if _, err := s.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "EUR"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	s.watches[0].RenewedAt = time.Time{} // pre-upgrade record
	if err := s.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	// First load stamps and should write back.
	first := NewStore(dir)
	if err := first.Load(); err != nil {
		t.Fatalf("first load: %v", err)
	}
	stamped := first.List()[0].RenewedAt
	if stamped.IsZero() {
		t.Fatal("migration did not stamp RenewedAt")
	}

	// A later load must see the SAME stamp, not a new one. If it re-grants, the
	// TTL clock resets forever and route watches never expire.
	time.Sleep(10 * time.Millisecond)
	second := NewStore(dir)
	if err := second.Load(); err != nil {
		t.Fatalf("second load: %v", err)
	}
	got := second.List()[0].RenewedAt
	if !got.Equal(stamped) {
		t.Errorf("RenewedAt re-granted on reload (%v -> %v): the TTL would never fire", stamped, got)
	}
}
