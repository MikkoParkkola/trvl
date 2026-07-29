package watch

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// MigrationReport describes what Migrate changed. All counts are zero on a store
// that is already healthy, which makes the command safe to run repeatedly.
type MigrationReport struct {
	BackupPath        string
	WatchesBefore     int
	WatchesAfter      int
	DuplicatesRemoved int
	RenewalsStamped   int
	HistoryBefore     int
	HistoryAfter      int
	HistoryCompacted  int
}

// Changed reports whether the migration modified anything.
func (r MigrationReport) Changed() bool {
	return r.DuplicatesRemoved > 0 || r.RenewalsStamped > 0 || r.HistoryCompacted > 0
}

// Summary renders a one-paragraph human-readable result.
func (r MigrationReport) Summary() string {
	if !r.Changed() {
		return "store is already clean: nothing to migrate"
	}
	return fmt.Sprintf(
		"watches %d -> %d (%d duplicates collapsed, %d renewal stamps added); "+
			"price history %d -> %d points (%d compacted); backup: %s",
		r.WatchesBefore, r.WatchesAfter, r.DuplicatesRemoved, r.RenewalsStamped,
		r.HistoryBefore, r.HistoryAfter, r.HistoryCompacted, r.BackupPath,
	)
}

// Migrate brings an existing store up to current invariants, in one explicit,
// reviewable pass. It is idempotent and takes a backup before writing anything.
//
// This is deliberately a COMMAND rather than something Load does implicitly.
// Migrating inside Load meant every process rewrote the whole store at startup,
// which is the last-writer-wins hazard the store cannot currently survive. Doing
// it once, on demand, keeps readers read-only.
//
// Three jobs:
//
//   - Collapse duplicate watches. Add only became idempotent recently, so
//     existing stores carry whatever accumulated before that. Deduping on write
//     alone leaves every already-affected store untouched — one real store held
//     468 route watches over 4 targets plus 380 copies of a single room watch.
//     Collapsing covers dated and dateless watches alike; the earlier ad-hoc
//     cleanup only handled dateless ones.
//
//   - Compact price history to the retention caps. The caps otherwise apply only
//     to new writes, so an existing 39MB / 320,000-point file stays exactly that
//     size and keeps costing ~686MB resident per process. Compaction is what
//     actually recovers the memory.
//
//   - Stamp RenewedAt on legacy watches, granting a full TTL from migration
//     rather than back-dating to creation, so upgrading cannot silently expire
//     watches still in use.
//
// The surviving record of a duplicate group is the richest one: real
// observations first, then most recently checked, then oldest — preserving the
// longest history rather than the newest empty copy.
func (s *Store) Migrate() (MigrationReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rep := MigrationReport{
		WatchesBefore: len(s.watches),
		HistoryBefore: len(s.history),
	}

	backup, err := s.backupLocked()
	if err != nil {
		return rep, fmt.Errorf("back up store before migrating: %w", err)
	}
	rep.BackupPath = backup

	removed, idMap := s.collapseDuplicatesLocked()
	rep.DuplicatesRemoved = removed
	s.reassignHistoryLocked(idMap)

	now := time.Now()
	for i := range s.watches {
		if s.watches[i].RenewedAt.IsZero() {
			s.watches[i].RenewedAt = now
			rep.RenewalsStamped++
		}
	}

	s.compactHistoryLocked()

	rep.WatchesAfter = len(s.watches)
	rep.HistoryAfter = len(s.history)
	rep.HistoryCompacted = rep.HistoryBefore - rep.HistoryAfter

	if !rep.Changed() {
		return rep, nil
	}
	if err := s.persistLocked(); err != nil {
		return rep, fmt.Errorf("persist migrated store: %w", err)
	}
	slog.Info("watch: store migrated",
		"duplicates_removed", rep.DuplicatesRemoved,
		"renewals_stamped", rep.RenewalsStamped,
		"history_compacted", rep.HistoryCompacted,
		"backup", rep.BackupPath)
	return rep, nil
}

// collapseDuplicatesLocked merges watches monitoring the same target, keeping
// the richest record of each group. Returns how many were removed and a map
// from every removed watch's ID to the ID of the record that survives its
// group, so callers can reassign (not discard) that watch's price history.
// Caller holds s.mu.
//
// Found by adversarial review, 2026-07-29: richer() only chose which record's
// OTHER fields (currency, thresholds, LastCheck-derived identity) survive. It
// never compared the two records' actual LowestPrice values, so a duplicate
// group where both rows already had observations silently kept whichever had
// the more recent LastCheck and discarded the other's LowestPrice outright --
// e.g. a €50 low recorded by the older, less-recently-checked duplicate was
// lost in favor of a newer duplicate's €100 low. The group's true lowest
// price must survive regardless of which record wins on recency.
func (s *Store) collapseDuplicatesLocked() (int, map[string]string) {
	if len(s.watches) < 2 {
		return 0, nil
	}
	kept := make([]Watch, 0, len(s.watches))
	// idMap tracks every collapsed watch's ID to the ID of whichever record
	// ultimately survives its duplicate group (groups can be more than 2
	// deep, so an ID already mapped to a now-superseded survivor is
	// re-pointed at the new one).
	idMap := make(map[string]string)
	for _, w := range s.watches {
		idx := -1
		for i := range kept {
			if kept[i].SameTarget(w) {
				idx = i
				break
			}
		}
		if idx < 0 {
			kept = append(kept, w)
			continue
		}
		survivor := kept[idx]
		mergedLowest := lowerPositive(survivor.LowestPrice, w.LowestPrice)
		var loserID string
		if richer(w, survivor) {
			// Keep the incoming record but do not lose an earlier creation date:
			// the group's history is older than any single surviving row.
			if !survivor.CreatedAt.IsZero() &&
				(w.CreatedAt.IsZero() || survivor.CreatedAt.Before(w.CreatedAt)) {
				w.CreatedAt = survivor.CreatedAt
			}
			w.LowestPrice = mergedLowest
			kept[idx] = w
			loserID = survivor.ID
		} else {
			if !w.CreatedAt.IsZero() &&
				(survivor.CreatedAt.IsZero() || w.CreatedAt.Before(survivor.CreatedAt)) {
				survivor.CreatedAt = w.CreatedAt
			}
			survivor.LowestPrice = mergedLowest
			kept[idx] = survivor
			loserID = w.ID
		}
		survivorID := kept[idx].ID
		if loserID != survivorID {
			idMap[loserID] = survivorID
		}
		for old, cur := range idMap {
			if cur == loserID {
				idMap[old] = survivorID
			}
		}
	}
	removed := len(s.watches) - len(kept)
	s.watches = kept
	return removed, idMap
}

// lowerPositive returns the lower of two prices, treating a non-positive
// value (unset) as absent rather than as a real zero-price low.
func lowerPositive(a, b float64) float64 {
	switch {
	case a <= 0:
		return b
	case b <= 0:
		return a
	case a < b:
		return a
	default:
		return b
	}
}

// reassignHistoryLocked repoints price-history points belonging to watches
// collapsed by collapseDuplicatesLocked onto the survivor's ID, so
// compactHistoryLocked's live-watch filter keeps them instead of dropping
// them as orphans of a now-removed duplicate. Caller holds s.mu.
func (s *Store) reassignHistoryLocked(idMap map[string]string) {
	if len(idMap) == 0 {
		return
	}
	for i := range s.history {
		if s.history[i].WatchID == "" {
			continue
		}
		if survivor, ok := idMap[s.history[i].WatchID]; ok {
			s.history[i].WatchID = survivor
		}
	}
}

// richer reports whether a should win over b as a duplicate group's survivor.
func richer(a, b Watch) bool {
	if (a.LowestPrice > 0) != (b.LowestPrice > 0) {
		return a.LowestPrice > 0
	}
	if !a.LastCheck.Equal(b.LastCheck) {
		return a.LastCheck.After(b.LastCheck)
	}
	if a.CreatedAt.IsZero() != b.CreatedAt.IsZero() {
		return !a.CreatedAt.IsZero()
	}
	return a.CreatedAt.Before(b.CreatedAt)
}

// compactHistoryLocked applies the retention caps to history that already
// exists, which the per-write pruning never touches. Caller holds s.mu.
func (s *Store) compactHistoryLocked() {
	perWatch := map[string]int{}
	for _, p := range s.history {
		if p.WatchID != "" {
			perWatch[p.WatchID]++
		}
	}
	for id, n := range perWatch {
		if n > maxObservationsPerWatch {
			s.pruneWatchLocked(id)
		}
	}
	s.pruneGlobalWatchLocked()

	perRoute := map[string]int{}
	for _, p := range s.history {
		if p.RouteKey != "" && p.WatchID == "" {
			perRoute[p.RouteKey]++
		}
	}
	for key, n := range perRoute {
		if n > maxObservationsPerRoute {
			s.pruneRouteLocked(key)
		}
	}
	s.pruneGlobalRouteLocked()

	// Drop points belonging to watches that no longer exist, including the
	// duplicates just collapsed. Orphaned history is dead weight that the caps
	// would otherwise let linger behind live watches.
	live := make(map[string]bool, len(s.watches))
	for _, w := range s.watches {
		live[w.ID] = true
	}
	kept := s.history[:0:0]
	for _, p := range s.history {
		if p.WatchID != "" && !live[p.WatchID] {
			continue
		}
		kept = append(kept, p)
	}
	s.history = kept
}

// backupLocked copies the current on-disk store beside itself, timestamped.
// Missing files are not an error: a fresh store has nothing to back up.
// Caller holds s.mu.
func (s *Store) backupLocked() (string, error) {
	stamp := time.Now().Format("20060102-150405")
	var made []string
	for _, path := range []string{s.watchesPath(), s.historyPath()} {
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", err
		}
		dst := fmt.Sprintf("%s.bak-%s", path, stamp)
		if err := os.WriteFile(dst, data, 0o600); err != nil {
			return "", err
		}
		made = append(made, filepath.Base(dst))
	}
	sort.Strings(made)
	if len(made) == 0 {
		return "(nothing to back up)", nil
	}
	return filepath.Join(s.dir, made[0]) + fmt.Sprintf(" (+%d more)", len(made)-1), nil
}

// MigrateDryRun reports what Migrate would change without writing anything.
// Records that a destructive command must be previewable before it runs.
func (s *Store) MigrateDryRun() (MigrationReport, error) {
	s.mu.Lock()
	watches := append([]Watch(nil), s.watches...)
	history := append([]PricePoint(nil), s.history...)
	s.mu.Unlock()

	shadow := &Store{dir: s.dir, watches: watches, history: history}
	shadow.mu.Lock()
	defer shadow.mu.Unlock()

	rep := MigrationReport{
		BackupPath:    "(dry run: nothing written)",
		WatchesBefore: len(shadow.watches),
		HistoryBefore: len(shadow.history),
	}
	removed, idMap := shadow.collapseDuplicatesLocked()
	rep.DuplicatesRemoved = removed
	shadow.reassignHistoryLocked(idMap)
	for i := range shadow.watches {
		if shadow.watches[i].RenewedAt.IsZero() {
			rep.RenewalsStamped++
		}
	}
	shadow.compactHistoryLocked()
	rep.WatchesAfter = len(shadow.watches)
	rep.HistoryAfter = len(shadow.history)
	rep.HistoryCompacted = rep.HistoryBefore - rep.HistoryAfter
	return rep, nil
}
