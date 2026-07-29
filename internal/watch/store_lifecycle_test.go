package watch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
	if _, _, err := s.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "EUR"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	s.watches[0].RenewedAt = time.Time{} // pre-upgrade record
	if err := s.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Migrate stamps and writes back.
	first := NewStore(dir)
	if err := first.Load(); err != nil {
		t.Fatalf("first load: %v", err)
	}
	if _, err := first.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	stamped := first.List()[0].RenewedAt
	if stamped.IsZero() {
		t.Fatal("migration did not stamp RenewedAt")
	}

	// A later load must see the SAME stamp, not a new one. If the stamp were not
	// persisted, the TTL clock would reset forever and route watches never expire.
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

// Migrate must collapse duplicates the ad-hoc cleanup missed: DATED watches too,
// not just dateless route ones. A real store kept 380 copies of one room watch
// because the earlier pass only handled route watches.
func TestMigrateCollapsesDatedDuplicates(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	room := Watch{
		Type: "room", Destination: "AMS", HotelName: "Hilton",
		RoomKeywords: []string{"king"}, DepartDate: "2026-06-15", ReturnDate: "2026-06-18",
		BelowPrice: 200, Currency: "EUR",
	}
	// Bypass Add's dedup to build a pre-fix store.
	for i := 0; i < 380; i++ {
		w := room
		w.ID = shortID()
		w.CreatedAt = time.Now()
		s.watches = append(s.watches, w)
	}
	if err := s.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	rep, err := s.Migrate()
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if got := len(s.List()); got != 1 {
		t.Errorf("380 identical room watches collapsed to %d, want 1", got)
	}
	if rep.DuplicatesRemoved != 379 {
		t.Errorf("reported %d duplicates removed, want 379", rep.DuplicatesRemoved)
	}
}

// The surviving record must be the richest one, so collapsing never trades away
// accumulated price history for an empty newer copy.
func TestMigrateKeepsRichestDuplicate(t *testing.T) {
	s := NewStore(t.TempDir())
	base := Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "EUR"}

	empty := base
	empty.ID = "empty"
	empty.CreatedAt = time.Now()

	rich := base
	rich.ID = "rich"
	rich.LowestPrice = 85
	rich.LastCheck = time.Now()
	rich.CreatedAt = time.Now().Add(-90 * 24 * time.Hour)

	s.watches = []Watch{empty, rich}
	if _, err := s.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	got := s.List()[0]
	if got.ID != "rich" {
		t.Errorf("collapse kept %q, want the record carrying price history", got.ID)
	}
	if got.LowestPrice != 85 {
		t.Errorf("collapse lost the all-time low: %v", got.LowestPrice)
	}
}

// Regression for the adversarial-review finding, 2026-07-29: when both
// duplicates in a group already carry observations, the surviving record's
// LowestPrice must be the true minimum of the two, not whichever duplicate
// happens to win on recency. A duplicate group with lows of 50 and 100 must
// never let migration keep 100 and silently erase the 50.
func TestMigrateMergesLowestPriceAcrossDuplicates(t *testing.T) {
	s := NewStore(t.TempDir())
	base := Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "EUR"}

	older := base
	older.ID = "older-cheaper"
	older.LowestPrice = 50
	older.LastCheck = time.Now().Add(-48 * time.Hour)
	older.CreatedAt = time.Now().Add(-90 * 24 * time.Hour)

	newer := base
	newer.ID = "newer-pricier"
	newer.LowestPrice = 100
	newer.LastCheck = time.Now()
	newer.CreatedAt = time.Now().Add(-1 * time.Hour)

	s.watches = []Watch{older, newer}
	s.history = []PricePoint{
		{WatchID: "older-cheaper", Price: 50, Timestamp: older.LastCheck},
		{WatchID: "newer-pricier", Price: 100, Timestamp: newer.LastCheck},
	}

	if _, err := s.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	watches := s.List()
	if len(watches) != 1 {
		t.Fatalf("duplicate group collapsed to %d watches, want 1", len(watches))
	}
	survivor := watches[0]
	if survivor.ID != "newer-pricier" {
		t.Fatalf("expected the more-recently-checked record to win identity, got %q", survivor.ID)
	}
	if survivor.LowestPrice != 50 {
		t.Errorf("LowestPrice = %v, want 50 (the group's true low must survive, not just the winner's recency)", survivor.LowestPrice)
	}

	// Neither duplicate's observation may be lost: both points must survive,
	// reassigned onto the surviving watch ID rather than dropped as orphans.
	var prices []float64
	for _, p := range s.history {
		if p.WatchID != "newer-pricier" {
			t.Errorf("history point still tagged with a collapsed-away ID %q", p.WatchID)
			continue
		}
		prices = append(prices, p.Price)
	}
	if len(prices) != 2 {
		t.Fatalf("history after migrate has %d points for the survivor, want 2 (50 and 100 both preserved)", len(prices))
	}
}

// Compaction is what actually recovers the memory: the retention caps otherwise
// apply only to NEW writes, leaving an existing 39MB / 320k-point file exactly
// as large as it was.
func TestMigrateCompactsExistingHistory(t *testing.T) {
	s := NewStore(t.TempDir())
	id, _, err := s.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "EUR"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	// Build an oversized history directly, as a pre-fix store would have.
	for i := 0; i < maxObservationsPerWatch*3; i++ {
		s.history = append(s.history, PricePoint{WatchID: id, Price: float64(i), Currency: "EUR", Timestamp: time.Now()})
	}
	before := len(s.history)

	rep, err := s.Migrate()
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if got := len(s.History(id)); got > maxObservationsPerWatch {
		t.Errorf("history still %d points after compaction, cap is %d", got, maxObservationsPerWatch)
	}
	if rep.HistoryCompacted <= 0 {
		t.Errorf("report claims %d compacted from %d points", rep.HistoryCompacted, before)
	}
	// Newest points survive.
	h := s.History(id)
	if h[len(h)-1].Price != float64(before-1) {
		t.Errorf("compaction dropped the newest point: got %v", h[len(h)-1].Price)
	}
}

// History belonging to collapsed-away duplicates must not linger.
func TestMigrateDropsOrphanedHistory(t *testing.T) {
	s := NewStore(t.TempDir())
	base := Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "EUR"}
	a, b := base, base
	a.ID, b.ID = "keeper", "dupe"
	a.LowestPrice = 85
	a.CreatedAt, b.CreatedAt = time.Now(), time.Now()
	s.watches = []Watch{a, b}
	s.history = []PricePoint{
		{WatchID: "keeper", Price: 100, Timestamp: time.Now()},
		{WatchID: "dupe", Price: 200, Timestamp: time.Now()},
	}

	if _, err := s.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, p := range s.history {
		if p.WatchID == "dupe" {
			t.Error("history for a collapsed duplicate was left behind")
		}
	}
}

// Safe to run repeatedly: a clean store reports no changes and is not rewritten.
func TestMigrateIsIdempotent(t *testing.T) {
	s := NewStore(t.TempDir())
	if _, _, err := s.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "EUR"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := s.Migrate(); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	second, err := s.Migrate()
	if err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if second.Changed() {
		t.Errorf("re-running migrate on a clean store reported changes: %s", second.Summary())
	}
}

// Migrate must back up before it writes: it deletes records.
func TestMigrateBacksUpBeforeWriting(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	base := Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "EUR"}
	for i := 0; i < 3; i++ {
		w := base
		w.ID = shortID()
		w.CreatedAt = time.Now()
		s.watches = append(s.watches, w)
	}
	if err := s.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	if _, err := s.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	var backups int
	for _, e := range entries {
		if strings.Contains(e.Name(), ".bak-") {
			backups++
		}
	}
	if backups == 0 {
		t.Error("migrate deleted records without writing a backup first")
	}
}

// Load must never write. Migrating inside Load made every process rewrite the
// whole store at startup - the last-writer-wins hazard this store cannot survive.
func TestLoadNeverWrites(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if _, _, err := s.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "EUR"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	s.watches[0].RenewedAt = time.Time{} // legacy record that Load used to stamp
	if err := s.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	path := filepath.Join(dir, "watches.json")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	for i := 0; i < 3; i++ {
		fresh := NewStore(dir)
		if err := fresh.Load(); err != nil {
			t.Fatalf("load %d: %v", i, err)
		}
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) || after.Size() != before.Size() {
		t.Error("Load wrote to the store; readers must be pure-read")
	}
}
