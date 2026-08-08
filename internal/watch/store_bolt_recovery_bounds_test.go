package watch

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
// Simulated by putting a DIRECTORY where the database belongs, rather than by
// real lock contention: the property under test is "an open failure must not
// trigger recovery", and any open failure exercises it. A two-process flock
// race is not reproducible in a unit test, which is exactly why the code must
// not depend on distinguishing one.
//
// A directory rather than chmod 000, and that is not a style preference. The
// first version made the file unreadable by permission bits, which does not
// deny reads on Windows -- Go maps chmod onto the read-only attribute there, so
// the open SUCCEEDED, Load returned no error, and the test failed on
// windows-latest while passing everywhere else. The property is
// platform-independent; the mechanism has to be too. bolt.Open on a directory
// fails on every platform trvl builds for.
//
// Raised by adversarial review of #588 as finding 1.
func TestOpenFailureDoesNotQuarantineTheDatabase(t *testing.T) {
	dir := t.TempDir()
	seedLegacyStore(t, dir)
	dbPath := filepath.Join(dir, "watch.db")

	// Stat must succeed so the load takes the database branch at all, and the
	// open must then fail. A directory does both.
	if err := os.Mkdir(dbPath, 0o700); err != nil {
		t.Fatalf("planting an unopenable database: %v", err)
	}

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
	err := fresh.Load()
	if err == nil {
		t.Fatal("Load accepted a schema this binary does not support")
	}
	// Assert the CONTRACT, not only its side effect. Checking that the file was
	// not renamed catches today's implementation; checking the sentinel catches
	// a future one that decides to quarantine under some other signal.
	if errors.Is(err, errNeverPublished) {
		t.Errorf("a present-but-unsupported schema was classified as never-published. The version "+
			"key proves the publishing transaction committed, so this store is live: err = %v", err)
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

// A schemaless database that still HOLDS DATA is not an unfinished conversion.
//
// The schema version is written in the same transaction as the data, so its
// absence normally proves no publish committed. Normally: corruption that wiped
// only the version key from a populated store would satisfy that test while the
// store held months of history, and falling back would trade it for a frozen
// pre-migration snapshot. Requiring the data buckets to be empty as well means
// the fallback cannot fire on anything with something to lose.
func TestSchemalessDatabaseWithDataIsNotTreatedAsNeverPublished(t *testing.T) {
	dir := t.TempDir()
	seedLegacyStore(t, dir)

	s := &Store{dir: dir}
	if err := s.Load(); err != nil {
		t.Fatalf("initial load: %v", err)
	}
	if err := s.Save(); err != nil {
		t.Fatalf("initial save: %v", err)
	}
	// A real store, then the version key wiped and nothing else.
	if err := setSchemaVersion(filepath.Join(dir, "watch.db"), ""); err != nil {
		t.Fatalf("wiping the schema version: %v", err)
	}

	fresh := &Store{dir: dir}
	err := fresh.Load()
	if err == nil {
		t.Fatal("Load succeeded against a database with no schema version")
	}
	// Parity with the unsupported-schema test: assert the classification, not
	// only its side effect, so a future change that quarantines under some
	// other signal still fails this contract.
	if errors.Is(err, errNeverPublished) {
		t.Errorf("a populated store was classified as never-published because its version key was "+
			"missing: err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "watch.db")); err != nil {
		t.Errorf("a populated database was moved aside because its version key was missing: %v. "+
			"It still holds every point recorded since the migration; the legacy files beside it "+
			"do not.", err)
	}
}

// An unfinished conversion that left EMPTY buckets behind must still recover.
//
// This pins the measure of "empty", which is easy to get wrong: bbolt's
// BucketStats.BucketN counts the bucket ITSELF, so KeyN+BucketN > 0 is true for
// any bucket that exists, empty or not. A guard written that way can never
// report a store as empty once the buckets are created, and a genuinely
// unfinished conversion is refused the recovery it needs.
//
// Raised by review of the emptiness guard, and written before the fix so the
// defect was demonstrated rather than assumed.
func TestUnfinishedConversionWithEmptyBucketsStillRecovers(t *testing.T) {
	dir := t.TempDir()
	seedLegacyStore(t, dir)

	if err := writeSchemalessDatabaseWithBuckets(filepath.Join(dir, "watch.db")); err != nil {
		t.Fatalf("planting an unconverted database: %v", err)
	}

	s := &Store{dir: dir}
	if err := s.Load(); err != nil {
		t.Fatalf("Load refused to recover from an unfinished conversion whose buckets exist but "+
			"hold nothing. An empty bucket is not data, and the legacy files beside it are still "+
			"the whole history: %v", err)
	}
	if len(s.history) != 2 {
		t.Errorf("recovered %d history points, want 2 from the legacy store", len(s.history))
	}
}

// writeSchemalessDatabaseWithBuckets creates the buckets and commits, without
// ever writing a schema version or any data -- an interrupted conversion that
// got one transaction further than the bare-file case.
func writeSchemalessDatabaseWithBuckets(path string) error {
	db, err := bolt.Open(path, 0o600, nil)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return db.Update(func(tx *bolt.Tx) error {
		for _, name := range [][]byte{bucketMeta, bucketWatchHistory, bucketRouteHistory, bucketWatchAll, bucketRouteAll} {
			if _, err := tx.CreateBucketIfNotExists(name); err != nil {
				return err
			}
		}
		return nil
	})
}

// TRVL.BOLTM1.5 -- a writer that publishes while a reader is deciding must win.
//
// The reader observes "no schema" WITHOUT the cross-process lock, then renames.
// Between those two steps a writer can take the lock, see the file exists, and
// commit a full generation into it. Renaming after that quarantines a LIVE
// database and republishes the frozen legacy JSON over it.
//
// Not hypothetical: the schemaless file is exactly what this recovery exists to
// handle, so a writer finding one and filling it in is the expected repair,
// racing the reader's decision to discard it.
//
// The window is driven directly rather than by two goroutines. Reproducing a
// real interleaving would need the reader parked mid-decision, and a test that
// cannot reliably enter the window proves nothing about it. Publishing before
// calling the quarantine puts us at the exact moment after the observation and
// before the rename, which is the state under test.
//
// Found by a third-vendor review after two others passed this code.
func TestLoadReloadsGenerationPublishedDuringRecoveryDecision(t *testing.T) {
	dir := t.TempDir()
	seedLegacyStore(t, dir)
	dbPath := filepath.Join(dir, "watch.db")

	// The reader's observation: a schemaless database, quarantine-eligible.
	if err := writeSchemalessDatabaseWithBuckets(dbPath); err != nil {
		t.Fatalf("planting an unconverted database: %v", err)
	}
	s := &Store{dir: dir}
	eligible, err := s.databaseIsUnpublished()
	if err != nil || !eligible {
		t.Fatalf("fixture is not quarantine-eligible: eligible=%v err=%v", eligible, err)
	}

	// Park the reader in the exact interval after its first observation and
	// before it acquires .lock. The writer wins that race and publishes a full
	// generation under the same lock production writers use.
	var hookErr error
	s.beforeRecoveryLock = func() {
		writerLock, lockErr := acquireFileLock(s.lockPath())
		if lockErr != nil {
			hookErr = lockErr
			return
		}
		defer releaseFileLock(writerLock)
		writer := &Store{dir: dir}
		writer.watches = []Watch{{ID: "live", Origin: "HEL", Destination: "JFK", Currency: "EUR"}}
		writer.history = []PricePoint{{WatchID: "live", Price: 500, Currency: "EUR"}}
		hookErr = writer.persistBoltLocked()
	}

	// This same Load call must re-check, notice the writer's generation, and
	// return it. Requiring a second Load would leak the stale errNeverPublished
	// observation to the user even though valid data is already on disk.
	if err := s.Load(); err != nil {
		t.Fatalf("Load returned the stale pre-publish error instead of reloading the writer's generation: %v", err)
	}
	if hookErr != nil {
		t.Fatalf("writer publish: %v", hookErr)
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Errorf("the published database is no longer at watch.db: %v", err)
	}
	if len(s.watches) != 1 || s.watches[0].ID != "live" {
		t.Errorf("watches = %+v, want the writer's generation from this Load call, not the legacy snapshot", s.watches)
	}
}

// A mutation already owns .lock before it reloads. Recovery must use that
// ownership instead of trying to acquire the non-reentrant lock a second time.
// The old implementation blocked forever here on both Unix and Windows.
func TestMutationRecoversSchemalessDatabaseWithoutRelocking(t *testing.T) {
	dir := t.TempDir()
	seedLegacyStore(t, dir)
	if err := writeSchemalessDatabaseWithBuckets(filepath.Join(dir, "watch.db")); err != nil {
		t.Fatalf("planting an unconverted database: %v", err)
	}

	s := &Store{dir: dir}
	done := make(chan error, 1)
	go func() {
		_, _, err := s.Add(Watch{Type: "flight", Origin: "AMS", Destination: "VLC", Currency: "EUR"})
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("mutation recovery failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("mutation deadlocked while recovering a schemaless database; recovery tried to acquire .lock while withTxn already held it")
	}

	fresh := &Store{dir: dir}
	if err := fresh.Load(); err != nil {
		t.Fatalf("load after recovered mutation: %v", err)
	}
	if len(fresh.watches) != 2 {
		t.Fatalf("watches = %+v, want recovered legacy watch plus the new mutation", fresh.watches)
	}
}
