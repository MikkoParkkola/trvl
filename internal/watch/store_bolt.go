package watch

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

// watch.db is the authoritative store from schema version 2 onward. Watches
// and history are committed by one bbolt transaction, so readers observe the
// complete old generation or the complete new generation after a crash.
// History is stored one point per key; recording a point never serializes or
// rewrites the retained corpus.
var (
	bucketMeta         = []byte("meta")
	bucketWatchHistory = []byte("history-watch")
	bucketRouteHistory = []byte("history-route")
	bucketWatchAll     = []byte("history-watch-all")
	bucketRouteAll     = []byte("history-route-all")
	keyWatches         = []byte("watches")
	keySchemaVersion   = []byte("schema-version")
)

const boltSchemaVersion = "2"

func (s *Store) databasePath() string { return filepathJoin(s.dir, "watch.db") }

// filepathJoin is kept tiny so all persistence paths remain in one place while
// store.go retains the legacy JSON path helpers used by migration and backups.
func filepathJoin(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + string(os.PathSeparator) + name
}

func boltOptions(readOnly bool) *bolt.Options {
	return &bolt.Options{ReadOnly: readOnly, Timeout: 5 * time.Second}
}

func (s *Store) openBolt(readOnly bool) (*bolt.DB, error) {
	return s.openBoltAt(s.databasePath(), readOnly)
}

// openBoltAt opens a bbolt database at an explicit path.
//
// The first publish needs this: it writes to a temporary path and renames on
// success, so an interrupted conversion cannot leave a file at the real path.
// See publishFirstGenerationLocked for why that matters.
func (s *Store) openBoltAt(path string, readOnly bool) (*bolt.DB, error) {
	db, err := bolt.Open(path, 0o600, boltOptions(readOnly))
	if err != nil {
		return nil, fmt.Errorf("open watch database: %w", err)
	}
	return db, nil
}

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

// prepareBoltLocked performs the one-time, backup-first conversion from the
// legacy JSON pair. The caller holds both s.mu and the cross-process .lock, so
// two processes cannot publish competing first generations.
func (s *Store) prepareBoltLocked() error {
	if _, err := os.Stat(s.databasePath()); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect watch database: %w", err)
	}
	if err := s.loadLocked(); err != nil {
		return err
	}
	return s.persistBoltLocked()
}

// withBoltMutation runs a narrow bbolt mutation without loading history. The
// watches snapshot is small and is re-read inside the transaction so field
// updates keep the same cross-process freshness guarantees as withTxn.
func (s *Store) withBoltMutation(apply func(tx *bolt.Tx, watches *[]Watch) (historyChanged bool, err error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureDir(); err != nil {
		return fmt.Errorf("create storage dir: %w", err)
	}
	lock, err := acquireFileLock(s.lockPath())
	if err != nil {
		return err
	}
	defer releaseFileLock(lock)
	if err := s.prepareBoltLocked(); err != nil {
		return err
	}

	db, err := s.openBolt(false)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	var committedWatches []Watch
	historyChanged := false
	err = db.Update(func(tx *bolt.Tx) error {
		if err := ensureBoltBuckets(tx); err != nil {
			return err
		}
		if err := validateBoltSchema(tx.Bucket(bucketMeta)); err != nil {
			return err
		}
		watches, err := decodeWatches(tx.Bucket(bucketMeta))
		if err != nil {
			return err
		}
		s.watches = watches
		s.fireTxnHook(stageAfterReload)
		changed, err := apply(tx, &watches)
		if err != nil {
			return err
		}
		historyChanged = changed
		if txnHook != nil && historyChanged {
			_, history, err := loadBoltStateTx(tx)
			if err != nil {
				return err
			}
			s.history = history
		}
		s.fireTxnHook(stageBeforeSave)
		if err := putJSON(tx.Bucket(bucketMeta), keyWatches, watches); err != nil {
			return fmt.Errorf("encode watches: %w", err)
		}
		committedWatches = append([]Watch(nil), watches...)
		return nil
	})
	if errors.Is(err, errTxnNoop) {
		return nil
	}
	if err != nil {
		return err
	}
	s.watches = committedWatches
	if historyChanged {
		s.historyStale = true
	}
	return nil
}

func ensureBoltBuckets(tx *bolt.Tx) error {
	for _, name := range [][]byte{bucketMeta, bucketWatchHistory, bucketRouteHistory, bucketWatchAll, bucketRouteAll} {
		if _, err := tx.CreateBucketIfNotExists(name); err != nil {
			return fmt.Errorf("create bucket %q: %w", name, err)
		}
	}
	return nil
}

func encodeSequence(n uint64) []byte {
	key := make([]byte, 8)
	binary.BigEndian.PutUint64(key, n)
	return key
}

func decodeSequence(key []byte) uint64 {
	if len(key) != 8 {
		return 0
	}
	return binary.BigEndian.Uint64(key)
}

func putJSON(bucket *bolt.Bucket, key []byte, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return bucket.Put(key, data)
}

func decodeWatches(meta *bolt.Bucket) ([]Watch, error) {
	if meta == nil || len(meta.Get(keyWatches)) == 0 {
		return nil, nil
	}
	var watches []Watch
	if err := json.Unmarshal(meta.Get(keyWatches), &watches); err != nil {
		return nil, fmt.Errorf("decode watches: %w", err)
	}
	return watches, nil
}

func validateBoltSchema(meta *bolt.Bucket) error {
	if meta == nil {
		return fmt.Errorf("watch database has no metadata bucket")
	}
	version := string(meta.Get(keySchemaVersion))
	if version != boltSchemaVersion {
		return fmt.Errorf("watch database schema %q is not supported by this binary (want %s)", version, boltSchemaVersion)
	}
	return nil
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

type sequencedPricePoint struct {
	sequence uint64
	point    PricePoint
}

// historyDecodeHook is a test seam proving hot writes do not deserialize the
// retained corpus. Tests set it serially; production leaves it nil.
var historyDecodeHook func()

func loadHistoryParent(parent *bolt.Bucket, out *[]sequencedPricePoint) error {
	if parent == nil {
		return nil
	}
	return parent.ForEach(func(series, value []byte) error {
		if value != nil {
			return fmt.Errorf("history series %q is not a bucket", series)
		}
		child := parent.Bucket(series)
		return child.ForEach(func(key, raw []byte) error {
			if historyDecodeHook != nil {
				historyDecodeHook()
			}
			var point PricePoint
			if err := json.Unmarshal(raw, &point); err != nil {
				return fmt.Errorf("decode history point: %w", err)
			}
			*out = append(*out, sequencedPricePoint{sequence: decodeSequence(key), point: point})
			return nil
		})
	})
}

func loadBoltStateTx(tx *bolt.Tx) ([]Watch, []PricePoint, error) {
	watches, err := decodeWatches(tx.Bucket(bucketMeta))
	if err != nil {
		return nil, nil, err
	}
	var rows []sequencedPricePoint
	if err := loadHistoryParent(tx.Bucket(bucketWatchHistory), &rows); err != nil {
		return nil, nil, err
	}
	if err := loadHistoryParent(tx.Bucket(bucketRouteHistory), &rows); err != nil {
		return nil, nil, err
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].sequence < rows[j].sequence })
	history := make([]PricePoint, 0, len(rows))
	for _, row := range rows {
		history = append(history, row.point)
	}
	return watches, history, nil
}

func loadBoltState(db *bolt.DB) ([]Watch, []PricePoint, error) {
	var watches []Watch
	var history []PricePoint
	err := db.View(func(tx *bolt.Tx) error {
		if err := validateBoltSchema(tx.Bucket(bucketMeta)); err != nil {
			// Wrapped as errNeverPublished ONLY when the schema is ABSENT.
			//
			// A schema that is present but unsupported is the opposite
			// situation: the publishing transaction committed, so the database
			// holds real data and the legacy files beside it are a frozen
			// pre-migration snapshot. That happens on a downgrade -- a newer
			// trvl writes a later schema, an older binary then cannot read it.
			// Treating it as "never published" would quarantine a live store
			// and republish months-old JSON as current, which is data loss
			// caused by running an older build for an afternoon.
			//
			// Absent means absent: no metadata bucket, or no version key. Those
			// cannot be produced by a completed publish, because the version is
			// written inside the same transaction as the data.
			//
			// Raised by the confirmation review of #588: the first fix attached
			// the sentinel to EVERY schema failure, which was wider than the
			// proof it claimed.
			if neverPublished(tx) {
				return fmt.Errorf("%w: %v", errNeverPublished, err)
			}
			return err
		}
		var err error
		watches, history, err = loadBoltStateTx(tx)
		return err
	})
	return watches, history, err
}

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

func (s *Store) loadBoltLocked() error {
	db, err := s.openBolt(true)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	watches, history, err := loadBoltState(db)
	if err != nil {
		return err
	}
	s.watches = watches
	s.history = history
	s.historyStale = false
	return nil
}

func resetBucket(tx *bolt.Tx, name []byte) (*bolt.Bucket, error) {
	if tx.Bucket(name) != nil {
		if err := tx.DeleteBucket(name); err != nil {
			return nil, err
		}
	}
	return tx.CreateBucket(name)
}

func (s *Store) persistBoltLocked() error {
	if err := s.ensureDir(); err != nil {
		return err
	}
	legacyPresent := false
	firstPublish := false
	if _, err := os.Stat(s.databasePath()); errors.Is(err, os.ErrNotExist) {
		firstPublish = true
		for _, path := range []string{s.watchesPath(), s.historyPath()} {
			if _, statErr := os.Stat(path); statErr == nil {
				legacyPresent = true
			} else if !errors.Is(statErr, os.ErrNotExist) {
				return fmt.Errorf("inspect legacy watch store: %w", statErr)
			}
		}
		if _, backupErr := s.backupLegacyLocked(); backupErr != nil {
			return fmt.Errorf("back up legacy store: %w", backupErr)
		}
	} else if err != nil {
		return fmt.Errorf("inspect watch database: %w", err)
	}
	if legacyPresent {
		// A legacy store may predate the retention guards (the observed
		// production tail contained 320k points for one watch). Compact only
		// after the untouched JSON files have been backed up, so the first
		// steady-state append does not pay hundreds of thousands of deletes.
		s.compactHistoryLocked()
	}

	if firstPublish {
		// Assemble somewhere else and rename. Once watch.db exists at its real
		// path the legacy JSON is never read again, so a half-written one is
		// indistinguishable from a finished one to every later load.
		return s.publishFirstGenerationLocked()
	}

	db, err := s.openBolt(false)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	err = db.Update(func(tx *bolt.Tx) error {
		return s.writeGenerationTx(tx)
	})
	if err == nil {
		s.historyStale = false
	}
	return err
}

// writeGenerationTx writes the whole in-memory generation into an open
// transaction. Shared by the steady-state rewrite and the first publish so the
// two cannot drift.
func (s *Store) writeGenerationTx(tx *bolt.Tx) error {
	if err := ensureBoltBuckets(tx); err != nil {
		return err
	}
	meta := tx.Bucket(bucketMeta)
	if err := meta.Put(keySchemaVersion, []byte(boltSchemaVersion)); err != nil {
		return err
	}
	if err := putJSON(meta, keyWatches, s.watches); err != nil {
		return fmt.Errorf("encode watches: %w", err)
	}
	for _, name := range [][]byte{bucketWatchHistory, bucketRouteHistory, bucketWatchAll, bucketRouteAll} {
		if _, err := resetBucket(tx, name); err != nil {
			return fmt.Errorf("reset bucket %q: %w", name, err)
		}
	}
	for _, point := range s.history {
		if _, err := appendHistoryPointTx(tx, point); err != nil {
			return err
		}
	}
	return nil
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

func appendHistoryPointTx(tx *bolt.Tx, point PricePoint) ([]byte, error) {
	meta := tx.Bucket(bucketMeta)
	sequence, err := meta.NextSequence()
	if err != nil {
		return nil, err
	}
	key := encodeSequence(sequence)
	parentName, allName, series := bucketRouteHistory, bucketRouteAll, point.RouteKey
	if point.WatchID != "" {
		parentName, allName, series = bucketWatchHistory, bucketWatchAll, point.WatchID
	}
	if series == "" {
		series = "_legacy-unkeyed"
	}
	parent := tx.Bucket(parentName)
	child, err := parent.CreateBucketIfNotExists([]byte(series))
	if err != nil {
		return nil, err
	}
	childCount := historyBucketCount(child)
	all := tx.Bucket(allName)
	allCount := historyBucketCount(all)
	if err := putJSON(child, key, point); err != nil {
		return nil, err
	}
	if err := all.Put(key, []byte(series)); err != nil {
		return nil, err
	}
	if err := child.SetSequence(childCount + 1); err != nil {
		return nil, err
	}
	if err := all.SetSequence(allCount + 1); err != nil {
		return nil, err
	}
	return key, nil
}

func historyBucketCount(bucket *bolt.Bucket) uint64 {
	if bucket == nil {
		return 0
	}
	if count := bucket.Sequence(); count > 0 {
		return count
	}
	count := bucket.Stats().KeyN
	if count <= 0 {
		return 0
	}
	return uint64(count) // #nosec G115 -- bbolt KeyN is a non-negative key count, checked above.
}

func historyBucketCountInt(bucket *bolt.Bucket) (int, error) {
	count := historyBucketCount(bucket)
	if count > uint64(math.MaxInt) {
		return 0, fmt.Errorf("history bucket count %d exceeds platform integer range", count)
	}
	return int(count), nil // #nosec G115 -- count is bounded by math.MaxInt above.
}

func deleteOldestFromSeries(parent, all *bolt.Bucket, series []byte) error {
	child := parent.Bucket(series)
	if child == nil {
		return fmt.Errorf("history index references missing series %q", series)
	}
	childCount := historyBucketCount(child)
	allCount := historyBucketCount(all)
	if childCount == 0 || allCount == 0 {
		return fmt.Errorf("history index references empty series %q", series)
	}
	key, _ := child.Cursor().First()
	if key == nil {
		return fmt.Errorf("history index references empty series %q", series)
	}
	key = bytes.Clone(key)
	if err := child.Delete(key); err != nil {
		return err
	}
	if err := all.Delete(key); err != nil {
		return err
	}
	if err := all.SetSequence(allCount - 1); err != nil {
		return err
	}
	if childCount == 1 {
		return parent.DeleteBucket(series)
	}
	return child.SetSequence(childCount - 1)
}

func pruneSeriesTx(parent, all *bolt.Bucket, series []byte, limit int) error {
	child := parent.Bucket(series)
	if child == nil {
		return nil
	}
	count, err := historyBucketCountInt(child)
	if err != nil {
		return err
	}
	excess := count - limit
	for range excess {
		if err := deleteOldestFromSeries(parent, all, series); err != nil {
			return err
		}
	}
	return nil
}

func pruneGlobalWatchTx(tx *bolt.Tx, limit int) error {
	parent, all := tx.Bucket(bucketWatchHistory), tx.Bucket(bucketWatchAll)
	count, err := historyBucketCountInt(all)
	if err != nil {
		return err
	}
	excess := count - limit
	for range excess {
		key, series := all.Cursor().First()
		if key == nil {
			break
		}
		series = bytes.Clone(series)
		if err := deleteOldestFromSeries(parent, all, series); err != nil {
			return err
		}
	}
	return nil
}

func pruneGlobalRouteTx(tx *bolt.Tx) error {
	parent, all := tx.Bucket(bucketRouteHistory), tx.Bucket(bucketRouteAll)
	count, err := historyBucketCountInt(all)
	if err != nil {
		return err
	}
	excess := count - maxRouteObservations
	for range excess {
		var victim []byte
		maxPoints := 0
		err := parent.ForEach(func(series, value []byte) error {
			if value != nil {
				return nil
			}
			points, err := historyBucketCountInt(parent.Bucket(series))
			if err != nil {
				return err
			}
			if points > maxPoints {
				maxPoints = points
				victim = bytes.Clone(series)
			}
			return nil
		})
		if err != nil {
			return err
		}
		if maxPoints <= 1 {
			_, oldestSeries := all.Cursor().First()
			victim = bytes.Clone(oldestSeries)
		}
		if len(victim) == 0 {
			break
		}
		if err := deleteOldestFromSeries(parent, all, victim); err != nil {
			return err
		}
	}
	return nil
}

func purgeWatchHistoryTx(tx *bolt.Tx, watchID string) error {
	parent, all := tx.Bucket(bucketWatchHistory), tx.Bucket(bucketWatchAll)
	child := parent.Bucket([]byte(watchID))
	if child == nil {
		return nil
	}
	var keys [][]byte
	if err := child.ForEach(func(key, _ []byte) error {
		keys = append(keys, bytes.Clone(key))
		return nil
	}); err != nil {
		return err
	}
	for _, key := range keys {
		if err := all.Delete(key); err != nil {
			return err
		}
	}
	allCount := historyBucketCount(all)
	if uint64(len(keys)) > allCount {
		return fmt.Errorf("watch history count exceeds global index count")
	}
	if err := all.SetSequence(allCount - uint64(len(keys))); err != nil {
		return err
	}
	return parent.DeleteBucket([]byte(watchID))
}

func lastRouteObservationTx(tx *bolt.Tx, routeKey, currency string) (PricePoint, bool, error) {
	child := tx.Bucket(bucketRouteHistory).Bucket([]byte(routeKey))
	if child == nil {
		return PricePoint{}, false, nil
	}
	cursor := child.Cursor()
	for key, raw := cursor.Last(); key != nil; key, raw = cursor.Prev() {
		var point PricePoint
		if err := json.Unmarshal(raw, &point); err != nil {
			return PricePoint{}, false, err
		}
		if stringsEqualFoldTrimmed(point.Currency, currency) {
			return point, true, nil
		}
	}
	return PricePoint{}, false, nil
}

func stringsEqualFoldTrimmed(left, right string) bool {
	return stringsUpperTrim(left) == stringsUpperTrim(right)
}

func stringsUpperTrim(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}
