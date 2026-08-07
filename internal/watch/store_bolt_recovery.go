package watch

// Recovery for a watch database that never finished converting from the legacy
// JSON pair, and the atomic first publish that makes such a state rare.
//
// Split out of store_bolt.go because this is where the danger is. Five
// revisions, five defects, every one a way for recovery to destroy the data it
// was meant to save: quarantining on any load failure, on any schema failure,
// on a missing version key alone, measuring emptiness with a statistic that
// counted the bucket itself, and racing a writer that publishes into the same
// file. None was caught by the test suite or the gates; all five came from
// adversarial review, by three different vendors.
//
// If you widen anything here, the question is not "is this state broken" but
// "does this state PROVE the publishing transaction never committed" -- because
// the fallback replaces live data with a pre-migration snapshot, and nothing
// dual-writes the legacy files once the conversion has completed.

import (
	"errors"
	"fmt"
	"os"

	bolt "go.etcd.io/bbolt"
)

// errNeverPublished marks a database that opened cleanly but carries no usable
// schema, which is the ONLY state in which falling back to the legacy JSON is
// safe.
//
// publishFirstGenerationLocked commits the schema key inside the transaction
// and only then renames the file into place, so a database at watch.db without
// a schema never completed a publish -- and if no publish ever completed, the
// legacy pair beside it was never superseded and is still authoritative.
//
// Every other failure is deliberately NOT this. An open that fails may be a
// five-second flock timeout behind a long write in another process, and a
// database that opens with a valid schema but will not decode may hold months
// of history. Treating either as "unreadable, use the legacy files" replaces
// live data with a frozen pre-migration snapshot and then republishes that
// snapshot as the new truth. That is a worse outcome than the one this recovery
// path was added to prevent (adversarial review of #588, finding 1).
var errNeverPublished = errors.New("watch database carries no schema; the conversion never completed")

// creatingPath is where the first generation is assembled before it is
// published. Kept beside the real file so the rename is same-filesystem and
// therefore atomic.
func (s *Store) creatingPath() string { return s.databasePath() + ".creating" }

// quarantinePath is where an unreadable database is moved aside so the legacy
// JSON can be used instead.
func (s *Store) quarantinePath() string { return s.databasePath() + ".unreadable" }

// quarantineUnreadableDatabaseLocked moves an unusable watch.db aside so the
// legacy JSON beside it can be loaded instead.
//
// Returns false, with nothing moved, when there is no legacy store to fall back
// to. In that case the unreadable database is the only copy of anything and
// renaming it would turn a diagnosable failure into an empty one.
func (s *Store) quarantineUnreadableDatabaseLocked() (bool, string, error) {
	// THE CROSS-PROCESS LOCK, AND A RE-CHECK INSIDE IT.
	//
	// Load holds only s.mu, which bounds this process. The observation that led
	// here was made without the .lock that writers take, so between "this file
	// has no schema" and "rename it aside" another process can take the lock,
	// see the file exists, and commit a full generation into it -- schema and
	// data. The rename would then quarantine a LIVE database and the frozen
	// legacy JSON would be republished over it.
	//
	// That window is not hypothetical: the schemaless state is exactly what
	// this recovery exists to handle, so a writer finding one and filling it in
	// is the expected repair, racing the reader's decision to discard it.
	//
	// A check and the mutation it guards must be under one lock. The re-check
	// is not belt-and-braces; it IS the fix, because the state can change
	// between the read-only observation and acquiring the lock.
	//
	// Found by a third-vendor review after two others passed this code.
	lock, lockErr := acquireFileLock(s.lockPath())
	if lockErr != nil {
		return false, "", lockErr
	}
	defer releaseFileLock(lock)

	stillUnpublished, checkErr := s.databaseIsUnpublished()
	if checkErr != nil {
		return false, "", checkErr
	}
	if !stillUnpublished {
		// A writer published while we were deciding. Its generation is the
		// truth; ours was a stale read.
		return false, "", nil
	}

	legacyPresent := false
	for _, path := range []string{s.watchesPath(), s.historyPath()} {
		if _, err := os.Stat(path); err == nil {
			legacyPresent = true
		}
	}
	if !legacyPresent {
		return false, "", nil
	}

	// A stable suffix would collide with an earlier quarantine and either fail
	// or overwrite the older evidence. The sequence is bounded so a repeatedly
	// failing store cannot fill the directory.
	var target string
	for i := 0; i < 100; i++ {
		candidate := s.quarantinePath()
		if i > 0 {
			candidate = fmt.Sprintf("%s.%d", s.quarantinePath(), i)
		}
		if _, err := os.Stat(candidate); errors.Is(err, os.ErrNotExist) {
			target = candidate
			break
		}
	}
	if target == "" {
		return false, "", fmt.Errorf("100 quarantined databases already exist beside %s", s.databasePath())
	}
	if err := os.Rename(s.databasePath(), target); err != nil {
		return false, "", err
	}
	return true, target, nil
}

// databaseIsUnpublished re-answers neverPublished against what is on disk right
// now. Callers hold the cross-process lock, so the answer stays true until they
// release it.
func (s *Store) databaseIsUnpublished() (bool, error) {
	db, err := s.openBolt(true)
	if err != nil {
		// Cannot see the file, so cannot claim it is unpublished. Refusing here
		// leaves the database in place, which is the safe direction.
		return false, err
	}
	defer func() { _ = db.Close() }()
	unpublished := false
	if viewErr := db.View(func(tx *bolt.Tx) error {
		unpublished = neverPublished(tx)
		return nil
	}); viewErr != nil {
		return false, viewErr
	}
	return unpublished, nil
}

// neverPublished reports whether this database provably never completed a
// conversion, which is the only state that makes the legacy JSON beside it
// authoritative again.
//
// Two conditions, and the second is defence in depth. The schema version is
// written inside the same transaction as the data, so its ABSENCE means no
// publish committed -- that is the invariant. But an invariant is a statement
// about the code that writes the file, not about every way a file on disk can
// be damaged: corruption that wiped just the version key from a fully populated
// store would satisfy it while the store held months of history. Requiring the
// data buckets to be empty as well means the fallback cannot fire on anything
// that has data to lose.
//
// This is the third time in this branch that the same shape has come up -- a
// rule stated more broadly than the evidence supports -- and the previous two
// were both data-loss paths. Narrowing it once more costs a bucket scan on a
// path that only runs when a load has already failed.
func neverPublished(tx *bolt.Tx) bool {
	meta := tx.Bucket(bucketMeta)
	if meta != nil && len(meta.Get(keySchemaVersion)) > 0 {
		return false
	}
	for _, name := range [][]byte{bucketWatchHistory, bucketRouteHistory} {
		b := tx.Bucket(name)
		if b == nil {
			continue
		}
		// A cursor, not Stats(). BucketStats.BucketN counts the bucket ITSELF,
		// so `KeyN+BucketN > 0` is true for every bucket that exists, empty or
		// not -- a test that can never answer "empty" once the buckets are
		// created, which refuses recovery to exactly the unfinished conversion
		// it is meant to allow. First() asks the only question that matters: is
		// there anything in here, key or nested bucket.
		if k, _ := b.Cursor().First(); k != nil {
			return false
		}
	}
	return meta == nil || len(meta.Get(keyWatches)) == 0
}

// publishFirstGenerationLocked converts the legacy JSON pair into watch.db.
//
// IT WRITES SOMEWHERE ELSE AND RENAMES, and that is the whole point. bbolt
// creates its file on Open, before any schema is committed, while the
// conversion itself can rewrite hundreds of thousands of history points (the
// production tail this code cites is 320k for a single watch). The mere
// EXISTENCE of watch.db is what stops the legacy JSON being read ever again --
// both loadLocked and prepareBoltLocked gate on Stat alone.
//
// So an interrupted first conversion used to leave a present-but-schemaless
// file, after which every load failed schema validation and the intact JSON
// beside it was never consulted. Not a wipe: the data was still on disk, and
// unreachable, which for a user is the same morning. The window was widest for
// exactly the stores that had the most to lose.
//
// A temp file plus rename closes it. Either the rename happened and the
// database is complete, or it did not and the legacy JSON is still the source
// of truth. There is no third state to recover from.
//
// Found by adversarial review of #587 (M1), which called it a hard blocker for
// a release that migrates user price history. It was right.
func (s *Store) publishFirstGenerationLocked() error {
	creating := s.creatingPath()
	// A leftover from a previous interrupted attempt is not evidence of
	// anything and must not be reused: it may hold a partial generation.
	if err := os.Remove(creating); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("clear the incomplete watch database: %w", err)
	}

	db, err := s.openBoltAt(creating, false)
	if err != nil {
		return err
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		return s.writeGenerationTx(tx)
	}); err != nil {
		_ = db.Close()
		_ = os.Remove(creating)
		return err
	}
	// Sync before publishing: a rename that beats its own contents to disk
	// would recreate the state this function exists to prevent.
	if err := db.Sync(); err != nil {
		_ = db.Close()
		_ = os.Remove(creating)
		return fmt.Errorf("flush the new watch database: %w", err)
	}
	if err := db.Close(); err != nil {
		_ = os.Remove(creating)
		return fmt.Errorf("close the new watch database: %w", err)
	}
	if err := os.Rename(creating, s.databasePath()); err != nil {
		_ = os.Remove(creating)
		return fmt.Errorf("publish the new watch database: %w", err)
	}
	s.historyStale = false
	return nil
}
