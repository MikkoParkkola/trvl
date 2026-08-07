package watch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// TRVL.BOLTM1.1 -- an interrupted first conversion must not hide intact legacy
// price history behind an unreadable database.
//
// bbolt creates its file on Open, before any schema is committed, and the mere
// EXISTENCE of watch.db is what stops the legacy JSON being read again. So a
// crash during the conversion -- which rewrites every history point, and the
// production tail this code cites is 320,000 of them for a single watch -- used
// to leave a present-but-schemaless file that failed validation on every
// subsequent load while the complete JSON sat untouched beside it. Not a wipe.
// Just permanently unreachable, which for the person who owns the data is the
// same morning.
//
// Simulated by planting exactly what an interrupted conversion leaves: a bolt
// file that opens cleanly and carries no schema, with the legacy pair still
// present. Crashing a real migration mid-transaction is not reproducible in a
// unit test, and the state it leaves is the thing under test.
//
// That schemaless state is also the ONLY one in which falling back is safe.
// The schema key is committed inside the publishing transaction, so a database
// without it never completed a publish -- which means the legacy files beside
// it were never superseded and are still the whole history. After a completed
// conversion they are a frozen snapshot, and swapping one for the other would
// be a silent rollback rather than a recovery.
//
// Raised as M1, a hard blocker, by adversarial review of #587. The narrowing to
// "schemaless only" came from review of #588, which found that acting on ANY
// load failure was itself a data-loss path.
func TestUnreadableDatabaseFallsBackToTheLegacyStore(t *testing.T) {
	dir := t.TempDir()
	s := &Store{dir: dir}

	seedLegacyStore(t, dir)

	// What an interrupted first publish leaves: a bolt file that OPENS cleanly
	// and carries no schema, because publishFirstGenerationLocked commits the
	// schema key inside the transaction and only renames the file into place
	// afterwards.
	//
	// Deliberately not a file of garbage bytes. Garbage fails to OPEN, and an
	// open failure is usually a lock held by another process rather than a
	// broken file -- acting on it is the data-loss path that
	// TestOpenFailureDoesNotQuarantineTheDatabase now guards. The fixture has
	// to be the state actually under test.
	if err := writeSchemalessDatabase(filepath.Join(dir, "watch.db")); err != nil {
		t.Fatalf("planting an unconverted database: %v", err)
	}

	if err := s.Load(); err != nil {
		t.Fatalf("Load refused to recover from an unreadable database while the legacy store was "+
			"intact beside it. That is the failure this test exists to prevent: the data is on disk "+
			"and unreachable. err = %v", err)
	}

	if len(s.watches) != 1 || s.watches[0].ID != "w1" {
		t.Fatalf("the legacy watches were not recovered: %+v", s.watches)
	}
	if len(s.history) != 2 {
		t.Fatalf("recovered %d history points, want 2 -- the legacy history was not read", len(s.history))
	}

	// The unreadable file must be kept, not deleted: it is the only artefact of
	// whatever went wrong, and the store it was replaced by is intact.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var quarantined string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "watch.db.unreadable") {
			quarantined = e.Name()
		}
	}
	if quarantined == "" {
		t.Error("the unreadable database was not kept for inspection; it is the only evidence of " +
			"what went wrong and the legacy store that replaced it is intact, so deleting it trades " +
			"a diagnosis for a filename")
	}
	if _, err := os.Stat(filepath.Join(dir, "watch.db")); err == nil {
		t.Error("the unreadable database is still at watch.db, so the next load will hit it again " +
			"and the recovery is not durable")
	}
}

// The control: an unreadable database with NO legacy store must still be an
// error. Moving it aside there would replace a diagnosable failure with a
// silently empty store, which is the worse answer -- the user would see their
// watches gone and no reason given.
func TestUnreadableDatabaseWithNoLegacyStoreIsStillAnError(t *testing.T) {
	dir := t.TempDir()
	s := &Store{dir: dir}

	if err := writeSchemalessDatabase(filepath.Join(dir, "watch.db")); err != nil {
		t.Fatalf("planting an unconverted database: %v", err)
	}

	if err := s.Load(); err == nil {
		t.Fatal("Load succeeded on an unconverted database with nothing to fall back to. It must " +
			"report the failure rather than present an empty store as if it were the truth.")
	}
	if _, err := os.Stat(filepath.Join(dir, "watch.db")); err != nil {
		t.Error("the database was moved aside even though there was no legacy store to recover " +
			"from, so the only copy of anything is now under a different name")
	}
}

// TRVL.BOLTM1.2 -- the first publish must not leave a file at watch.db unless
// it is complete.
//
// The guarantee is the temp-file-and-rename in publishFirstGenerationLocked.
// This asserts the observable half: after a successful conversion the staging
// file is gone, so no later run can mistake a leftover for a finished database.
func TestFirstPublishLeavesNoStagingFile(t *testing.T) {
	dir := t.TempDir()
	s := &Store{dir: dir}
	seedLegacyStore(t, dir)

	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "watch.db")); err != nil {
		t.Fatalf("the database was not published: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "watch.db.creating")); err == nil {
		t.Error("the staging file survived a successful publish. A leftover .creating file is " +
			"harmless only while nothing reads it; leaving one is how the next change starts " +
			"trusting it.")
	}
}

// writeSchemalessDatabase creates a valid, openable bolt file with no schema
// key -- the state an interrupted first conversion leaves behind, and the ONLY
// state in which the legacy JSON beside it is still authoritative.
func writeSchemalessDatabase(path string) error {
	db, err := bolt.Open(path, 0o600, nil)
	if err != nil {
		return err
	}
	return db.Close()
}

// seedLegacyStore writes a legacy JSON pair: one watch and two history points.
func seedLegacyStore(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	watches := []Watch{{ID: "w1", Origin: "HEL", Destination: "CDG", Currency: "EUR"}}
	history := []PricePoint{
		{WatchID: "w1", Price: 100, Currency: "EUR"},
		{WatchID: "w1", Price: 110, Currency: "EUR"},
	}
	if err := saveJSON(filepath.Join(dir, "watches.json"), watches); err != nil {
		t.Fatalf("seeding watches: %v", err)
	}
	if err := saveJSON(filepath.Join(dir, "price-history.json"), history); err != nil {
		t.Fatalf("seeding history: %v", err)
	}
}
