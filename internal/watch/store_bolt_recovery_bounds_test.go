package watch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// corruptWatchesPayload replaces the stored watches blob with bytes that are
// not JSON, leaving the schema key untouched. That is a database which opens
// cleanly, declares a supported schema, and cannot be decoded.
func corruptWatchesPayload(path string) error {
	db, err := bolt.Open(path, 0o600, nil)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketMeta).Put(keyWatches, []byte("{ not json"))
	})
}

// TRVL.BOLTM1.3 -- a database that cannot be OPENED is left exactly where it is.
//
// This is the counterpart to the recovery in store_bolt_recovery_test.go, and
// it guards a data-loss path that recovery introduced.
//
// Load does not take the cross-process lock, and it opens the database
// read-only with a five-second flock timeout. Writers hold an exclusive lock
// for the length of a full generation rewrite. So a large history on a slow
// disk makes a concurrent `trvl watch list` see "cannot open" -- an ordinary
// lock wait, not a broken file.
//
// The first version of the recovery treated every load failure alike. It would
// therefore rename the LIVE database aside, load the frozen pre-migration JSON,
// and the next save would republish that snapshot as the new database. Months
// of price history, lost to a five-second wait. That is a worse failure than
// the one the recovery exists to prevent, and it fires far more often.
//
// Simulated with an unreadable-by-permissions file rather than a real lock
// contention: the property under test is "an open failure must not trigger
// recovery", and any open failure exercises it. A two-process flock race is not
// reproducible in a unit test, which is exactly why the code must not depend on
// distinguishing one.
//
// Raised by adversarial review of #588 as finding 1.
func TestOpenFailureDoesNotQuarantineTheDatabase(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits do not deny access")
	}
	dir := t.TempDir()
	seedLegacyStore(t, dir)

	// Build a real, valid database first, then make it unopenable.
	s := &Store{dir: dir}
	if err := s.Load(); err != nil {
		t.Fatalf("initial load: %v", err)
	}
	if err := s.Save(); err != nil {
		t.Fatalf("initial save: %v", err)
	}
	dbPath := filepath.Join(dir, "watch.db")
	if err := os.Chmod(dbPath, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dbPath, 0o600) })

	fresh := &Store{dir: dir}
	err := fresh.Load()

	if err == nil {
		t.Fatal("Load succeeded against a database it cannot open. It must report the failure, " +
			"because the alternative is presenting some other source as if it were the truth.")
	}
	if _, statErr := os.Stat(dbPath); statErr != nil {
		t.Fatalf("the database was moved aside because it could not be OPENED: %v. An open failure "+
			"is usually transient -- a lock held by a writer, a permission blip -- and quarantining "+
			"on it replaces live history with a frozen pre-migration snapshot that the next save "+
			"then republishes as authoritative.", statErr)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "watch.db.unreadable") {
			t.Errorf("a quarantine copy (%s) was created for a database that merely failed to open", e.Name())
		}
	}
}

// And a database that opens but holds a VALID schema it cannot decode is also
// left alone. Only "opened cleanly, no schema at all" proves the conversion
// never completed, and only that makes the legacy files authoritative again.
//
// Without this boundary the recovery reads as "any bolt error means use the old
// files", which after months of price checks means rolling back to a frozen
// snapshot and calling it recovery.
func TestSchemaValidDatabaseIsNotQuarantinedOnDecodeFailure(t *testing.T) {
	dir := t.TempDir()
	seedLegacyStore(t, dir)

	s := &Store{dir: dir}
	if err := s.Load(); err != nil {
		t.Fatalf("initial load: %v", err)
	}
	if err := s.Save(); err != nil {
		t.Fatalf("initial save: %v", err)
	}

	// Corrupt the stored watches payload while leaving the schema key intact.
	if err := corruptWatchesPayload(filepath.Join(dir, "watch.db")); err != nil {
		t.Skipf("could not corrupt the payload for this test: %v", err)
	}

	fresh := &Store{dir: dir}
	if err := fresh.Load(); err == nil {
		t.Fatal("Load succeeded against a database whose payload does not decode")
	}
	if _, err := os.Stat(filepath.Join(dir, "watch.db")); err != nil {
		t.Errorf("a database with a VALID schema was moved aside: %v. It may hold months of "+
			"history; the legacy files beside it are a pre-migration snapshot, so swapping one for "+
			"the other is a silent rollback rather than a recovery.", err)
	}
}

// TRVL.BOLTM1.4 -- a database with a PRESENT but unsupported schema holds real
// data and must never be rolled back to the legacy files.
//
// This is the downgrade case: a newer trvl writes a later schema, then an older
// binary runs and cannot read it. The publishing transaction plainly committed
// -- the version key is there -- so the store is live and the legacy JSON
// beside it is a frozen pre-migration snapshot. Quarantining here would swap
// months of history for that snapshot and republish it as current, losing data
// because someone ran an older build for an afternoon.
//
// The first version of this fix attached the "never published" sentinel to
// EVERY schema validation failure, which was wider than the proof it claimed.
// Raised by the confirmation review of #588.
func TestUnsupportedSchemaIsNotTreatedAsNeverPublished(t *testing.T) {
	dir := t.TempDir()
	seedLegacyStore(t, dir)

	s := &Store{dir: dir}
	if err := s.Load(); err != nil {
		t.Fatalf("initial load: %v", err)
	}
	if err := s.Save(); err != nil {
		t.Fatalf("initial save: %v", err)
	}

	if err := setSchemaVersion(filepath.Join(dir, "watch.db"), "99"); err != nil {
		t.Fatalf("stamping a future schema: %v", err)
	}

	fresh := &Store{dir: dir}
	if err := fresh.Load(); err == nil {
		t.Fatal("Load accepted a schema this binary does not support")
	}
	if _, err := os.Stat(filepath.Join(dir, "watch.db")); err != nil {
		t.Errorf("a database with a PRESENT schema was moved aside: %v. The version key proves the "+
			"publishing transaction committed, so this store is live and the legacy files are a "+
			"frozen snapshot -- swapping them loses every point recorded since the migration.", err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "watch.db.unreadable") {
			t.Errorf("a quarantine copy (%s) was created for a downgrade, which is a recoverable "+
				"state: running the newer build again reads the store fine", e.Name())
		}
	}
}

// setSchemaVersion rewrites the stored schema version, leaving everything else
// intact -- what a newer binary's store looks like to an older one.
func setSchemaVersion(path, version string) error {
	db, err := bolt.Open(path, 0o600, nil)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketMeta).Put(keySchemaVersion, []byte(version))
	})
}
