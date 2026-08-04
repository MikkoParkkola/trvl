package watch

import (
	"os"
	"strings"
	"testing"
	"time"
)

// Migration's history compaction, its backup, and its behaviour against a
// concurrent writer.
//
// Split out of migrate_test.go at the 800-line ceiling. The seam is subject:
// migrate_test.go covers WHICH duplicate survives a collapse and what state it
// carries; this file covers what migration does to price history and to the
// rollback point it leaves behind.

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

	if err := s.Save(); err != nil {
		t.Fatalf("save seeded store: %v", err)
	}
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

// History points denominated in a watch's OLD currency must not linger
// alongside points in its current currency under the same live WatchID.
// Found by adversarial review, 2026-07-30 (round 15): compactHistoryLocked's
// live-watch filter only checked "does this ID still exist," never "is this
// point in the ID's CURRENT currency" -- so history left mixed-currency by
// the pre-round-14/15 poller (or any future currency change) survived
// compaction indefinitely under a live ID.
func TestMigrateDropsMixedCurrencyHistory(t *testing.T) {
	s := NewStore(t.TempDir())
	w := Watch{ID: "w1", Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "EUR", CreatedAt: time.Now()}
	s.watches = []Watch{w}
	s.history = []PricePoint{
		{WatchID: "w1", Price: 15000, Currency: "JPY", Timestamp: time.Now().Add(-time.Hour)},
		{WatchID: "w1", Price: 180, Currency: "EUR", Timestamp: time.Now()},
		{WatchID: "w1", Price: 999, Currency: "", Timestamp: time.Now()}, // legacy, no currency recorded
	}

	if err := s.Save(); err != nil {
		t.Fatalf("save seeded store: %v", err)
	}
	if _, err := s.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := s.History("w1")
	for _, p := range h {
		if p.Currency == "JPY" {
			t.Error("stale JPY history point survived compaction under a watch now denominated in EUR")
		}
	}
	if len(h) != 2 {
		t.Errorf("history len = %d, want 2 (EUR point + legacy no-currency point kept, JPY point dropped)", len(h))
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

// TRVL.MIGRATE.1 -- Migrate must not publish this process's stale snapshot over
// a concurrent writer's work.
//
// Migrate is the one operation that rewrites the ENTIRE store, so it had the
// widest blast radius of any writer and, before trvl#562, the weakest
// guarantee: it took only s.mu, so it migrated whatever this process happened
// to be holding and wrote that over the files. A watch another process added
// after this one's last load was silently deleted.
//
// The window is real and needs no crash: a CLI `trvl watch migrate` reads at
// T0, the scheduler adds a watch at T1, the migration publishes T0 at T2.
//
// The stale snapshot is seeded with DUPLICATES on purpose. A migration with
// nothing to change returns errTxnNoop and never writes -- and a test whose
// dangerous write never happens cannot detect a dangerous write. The first
// version of this test did exactly that and passed against deliberately broken
// code; the duplicates are what make the persist actually run.
func TestMigrateDoesNotClobberAConcurrentWrite(t *testing.T) {
	if !lockSupported {
		t.Skip("store transactions are not enforced on this platform")
	}
	dir := t.TempDir()

	dup := Watch{
		Type: "flight", Origin: "HEL", Destination: "BCN",
		DepartDate: "2027-01-01", Currency: "EUR", BelowPrice: 200,
	}
	a, b := dup, dup
	a.ID, b.ID = "dup-a", "dup-b"
	a.CreatedAt, b.CreatedAt = time.Now(), time.Now()

	seed := NewStore(dir)
	seed.watches = []Watch{a, b}
	if err := seed.Save(); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// This process loads, and then goes stale.
	stale := NewStore(dir)
	if err := stale.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	// Another process adds a watch the stale snapshot has never seen.
	other := NewStore(dir)
	if _, _, err := other.Add(Watch{
		Type: "flight", Origin: "AMS", Destination: "VLC",
		DepartDate: "2027-02-01", Currency: "EUR", BelowPrice: 150,
	}); err != nil {
		t.Fatalf("concurrent add: %v", err)
	}

	rep, err := stale.Migrate()
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if !rep.Changed() {
		t.Fatal("fixture failure: the migration changed nothing, so it never wrote and this test " +
			"would pass against any implementation")
	}

	reader := NewStore(dir)
	if err := reader.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	var found bool
	for _, w := range reader.List() {
		if w.Destination == "VLC" {
			found = true
		}
	}
	if !found {
		t.Errorf("the concurrently added watch is gone: migrate published a snapshot taken "+
			"before it existed. Store now holds %d watch(es)", len(reader.List()))
	}
}

// TRVL.MIGRATE.2 -- a backup must never overwrite an earlier one.
//
// The stamp is second-resolution, so two migrations within the same second
// produced the same filename and the second silently destroyed the first's
// rollback point -- the one file whose entire purpose is surviving a bad
// migration. A migration that eats its own escape hatch is worse than one that
// refuses to start.
func TestBackupNeverOverwritesAnEarlierOne(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if _, _, err := s.Add(Watch{
		Type: "flight", Origin: "HEL", Destination: "BCN",
		DepartDate: "2027-01-01", Currency: "EUR",
	}); err != nil {
		t.Fatalf("add: %v", err)
	}

	const stamp = "20260804-120000"
	first, err := writeNewBackup(s.watchesPath(), stamp, []byte(`["first"]`))
	if err != nil {
		t.Fatalf("first backup: %v", err)
	}
	second, err := writeNewBackup(s.watchesPath(), stamp, []byte(`["second"]`))
	if err != nil {
		t.Fatalf("second backup: %v", err)
	}

	if first == second {
		t.Fatalf("both backups wrote to %s; the second destroyed the first's rollback point", first)
	}
	got, err := os.ReadFile(first)
	if err != nil {
		t.Fatalf("read first backup: %v", err)
	}
	if string(got) != `["first"]` {
		t.Errorf("the first backup's contents changed to %q; a rollback point must be immutable once written", got)
	}
}
