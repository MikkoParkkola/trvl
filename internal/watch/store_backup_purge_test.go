package watch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TRVL.BOLTM4.1 -- removing a watch removes its price history with it.
//
// Remove dropped the watch row and left the points. The global cap counts EVERY
// watch-keyed point, orphan or not, so a deleted watch went on consuming the
// budget and live watches were trimmed to make room for a series nobody could
// see, list, or ask about. The bolt store makes it worse rather than better: it
// rewrites the whole history on the next transaction, so the orphans were
// actively re-published.
//
// Raised as M4 by adversarial review of #587.
func TestRemoveDropsTheWatchsHistory(t *testing.T) {
	dir := t.TempDir()
	s := &Store{dir: dir}
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}

	doomedID, _, err := s.Add(Watch{Origin: "HEL", Destination: "CDG", Currency: "EUR"})
	if err != nil {
		t.Fatalf("add doomed: %v", err)
	}
	keeperID, _, err := s.Add(Watch{Origin: "HEL", Destination: "LHR", Currency: "EUR"})
	if err != nil {
		t.Fatalf("add keeper: %v", err)
	}
	for i := 0; i < 5; i++ {
		if err := s.RecordPrice(doomedID, float64(100+i), "EUR"); err != nil {
			t.Fatalf("record doomed: %v", err)
		}
		if err := s.RecordPrice(keeperID, float64(200+i), "EUR"); err != nil {
			t.Fatalf("record keeper: %v", err)
		}
	}

	if ok, err := s.Remove(doomedID); err != nil || !ok {
		t.Fatalf("Remove = %v, %v", ok, err)
	}

	// Reload from disk: the question is what PERSISTS, not what this process
	// happens to be holding.
	fresh := &Store{dir: dir}
	if err := fresh.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}

	orphans, kept := 0, 0
	for _, p := range fresh.history {
		switch p.WatchID {
		case doomedID:
			orphans++
		case keeperID:
			kept++
		}
	}
	if orphans != 0 {
		t.Errorf("%d price points survive for a watch that was removed. They still count against "+
			"the global cap, so live watches get trimmed to make room for a series nobody can see "+
			"or ask about.", orphans)
	}
	if kept != 5 {
		t.Errorf("the surviving watch kept %d of its 5 points; removing one watch must not touch "+
			"another's history", kept)
	}
	if len(fresh.watches) != 1 || fresh.watches[0].ID != keeperID {
		t.Errorf("watches after removal = %+v, want only the keeper", fresh.watches)
	}
}

// TRVL.BOLTM2.1 -- a backup that cannot be read back is not a backup.
//
// writeNewBackup wrote bytes and closed the handle. That proves the write call
// returned, not that anything durable and readable landed. A backup is used
// exactly once, after something has already gone wrong, so the failure that
// matters is the one discovered THEN -- when the original is gone and there is
// nothing left to compare against.
//
// Raised as M2 by adversarial review of #587: the ORDER was already right
// (backup before any destructive step); "verified readable" was not implemented.
func TestBackupIsVerifiedReadableAfterWriting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "watches.json")
	payload := []byte(`[{"id":"w1","origin":"HEL"}]`)

	dst, err := writeNewBackup(path, "20260807T000000Z", payload)
	if err != nil {
		t.Fatalf("writeNewBackup: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("the backup it reported writing cannot be read: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("backup content = %q, want %q", got, payload)
	}
}

// And the verification must REFUSE a copy that is byte-identical to a source
// which is itself unusable. A faithful copy of corruption is not a backup, and
// migrating past it spends the only chance anyone had to notice.
func TestBackupRefusesAnUnparseableSource(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "watches.json")

	_, err := writeNewBackup(path, "20260807T000000Z", []byte("{ this is not json"))
	if err == nil {
		t.Fatal("writeNewBackup accepted a source that is not valid JSON. The backup would be a " +
			"perfect copy of something that cannot be restored, and the migration would proceed " +
			"believing it had a safety net.")
	}
	if !strings.Contains(err.Error(), "not valid JSON") {
		t.Errorf("error = %v, want it to name the reason so the operator can act on it", err)
	}
}

// An empty legacy file is a legitimate state, not corruption: a store with no
// watches yet writes one. Refusing it would block the migration for every new
// user, which is how a safety check gets removed.
func TestBackupAcceptsAnEmptySource(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "watches.json")

	if _, err := writeNewBackup(path, "20260807T000000Z", nil); err != nil {
		t.Fatalf("writeNewBackup refused an empty source: %v", err)
	}
}
