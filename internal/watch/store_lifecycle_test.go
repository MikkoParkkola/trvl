package watch

import (
	"os"
	"path/filepath"
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

// The pairwise-merge scalar (and its lowerPositive helper) that used to carry
// LowestPrice across a duplicate group is gone: collapseDuplicatesLocked now
// recomputes the group's low from every member's original value in one pass
// (see TestMigrateRecoversTrueLowAcrossCurrencyResetMidChain), so there is no
// running merge value left to unit-test in isolation. A negative LowestPrice
// is excluded by the `w.LowestPrice <= 0` guard in that recomputation, which
// is what previously needed lowerPositive's explicit both-non-positive
// handling.

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
