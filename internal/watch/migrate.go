package watch

import (
	"errors"
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
	var rep MigrationReport
	err := s.withTxn(func() error {
		return s.migrateLocked(&rep)
	})
	if err != nil {
		return rep, err
	}
	if rep.Changed() {
		slog.Info("watch: store migrated",
			"duplicates_removed", rep.DuplicatesRemoved,
			"renewals_stamped", rep.RenewalsStamped,
			"history_compacted", rep.HistoryCompacted,
			"backup", rep.BackupPath)
	}
	return rep, nil
}

// migrateLocked is Migrate's body, running inside a store transaction: the
// cross-process lock is held and committed state has already been reloaded.
//
// Both matter, and neither used to hold. Migrate took only s.mu, so it read
// whatever this process happened to have in memory and published it over the
// files -- a concurrent writer's watch, added between this process's last load
// and the migration, was silently deleted. It is the one operation that
// rewrites the ENTIRE store, so it had the widest blast radius of any writer
// and the weakest guarantee. Found by round-2 gpt-review and filed as trvl#562.
//
// The backup is taken INSIDE the transaction too, so it reflects the same
// committed state the migration is about to rewrite. Taken outside, it could
// snapshot a generation that the migration never saw.
func (s *Store) migrateLocked(rep *MigrationReport) error {
	rep.WatchesBefore = len(s.watches)
	rep.HistoryBefore = len(s.history)

	backup, err := s.backupLocked()
	if err != nil {
		return fmt.Errorf("back up store before migrating: %w", err)
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
		// Nothing to write. errTxnNoop unwinds without saving rather than
		// republishing this process's whole snapshot for a no-op.
		return errTxnNoop
	}
	return nil
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
	// Materialize whole duplicate groups before deciding anything. Earlier
	// versions merged pairwise as watches were scanned, folding each new
	// arrival's LowestPrice into a single running scalar. That is lossy: once
	// a cross-currency merge reset the scalar to 0 (unset), a LATER
	// same-currency duplicate's own merge could only compare against that 0,
	// so it always "won" and silently discarded a real, still-valid
	// same-currency low recorded by an earlier group member -- e.g. EUR 80 ->
	// JPY 5,000 (reset to 0 on the currency mismatch) -> JPY 10,000 (merged
	// against the 0, producing LowestPrice 10,000 and losing the true JPY low
	// of 5,000). Found by adversarial review, 2026-07-29 (round 3). Grouping
	// first and recomputing LowestPrice/CheapestDate from every group
	// member's ORIGINAL value -- filtered to the survivor's own currency --
	// removes the lossy intermediate scalar entirely.
	// Grouped by dedupeKey, the SAME identity Store.Add uses. The two must agree
	// on what "the same watch" means, or `trvl watch migrate` silently undoes
	// what Add just did. Found by GPT second-opinion review, 2026-08-02, when
	// this grouped by SameTarget while Add keyed on something else.
	//
	// dedupeKey excludes Currency, so a route re-watched in a different currency
	// groups with its predecessor and the currency-aware LowestPrice
	// recomputation below still applies.
	//
	// Note for anyone migrating a store written before 2026-08-02: identity was
	// briefly threshold-aware (#509), so such a store can hold several watches
	// per target. Migration collapses them, keeping the richest. That is
	// intended -- the operator reversed that design -- but it IS lossy for the
	// superseded thresholds, which is why it lives in an explicit command that
	// backs up first rather than in Load.
	groups := make([][]Watch, 0, len(s.watches))
	groupKeys := make([]string, 0, len(s.watches))
	for _, w := range s.watches {
		key := w.dedupeKey()
		gi := -1
		for i := range groups {
			if groupKeys[i] == key {
				gi = i
				break
			}
		}
		if gi < 0 {
			groups = append(groups, []Watch{w})
			groupKeys = append(groupKeys, key)
			continue
		}
		groups[gi] = append(groups[gi], w)
	}

	kept := make([]Watch, 0, len(groups))
	idMap := make(map[string]string)
	for _, group := range groups {
		if len(group) == 1 {
			kept = append(kept, group[0])
			continue
		}

		// Found by adversarial review, 2026-07-29: richer() only chose which
		// record's OTHER fields (currency, thresholds, LastCheck-derived
		// identity) survive. It never compared the group's actual LowestPrice
		// values itself, so picking a survivor by recency alone would discard
		// a real low recorded by a less-recently-checked duplicate.
		survivor := group[0]
		for _, w := range group[1:] {
			if richer(w, survivor) {
				if !survivor.CreatedAt.IsZero() &&
					(w.CreatedAt.IsZero() || survivor.CreatedAt.Before(w.CreatedAt)) {
					w.CreatedAt = survivor.CreatedAt
				}
				survivor = w
			} else if !w.CreatedAt.IsZero() &&
				(survivor.CreatedAt.IsZero() || w.CreatedAt.Before(survivor.CreatedAt)) {
				survivor.CreatedAt = w.CreatedAt
			}
		}

		// SameTarget deliberately ignores Currency (a route/room can be
		// re-watched in a different currency), so a duplicate group can
		// legitimately span two currencies. A group's LowestPrice/CheapestDate
		// can only ever reflect ONE currency, so recompute both from scratch
		// across every member that shares the survivor's final currency --
		// never from a member in a different (or blank, which carries no
		// known currency either) one. This mirrors Store.Add's applyIntent,
		// which already treats a currency change as invalidating the
		// previously observed price.
		var lowest float64
		var cheapestDate string
		for _, w := range group {
			if w.Currency != survivor.Currency || w.LowestPrice <= 0 {
				continue
			}
			if lowest <= 0 || w.LowestPrice < lowest {
				lowest = w.LowestPrice
				cheapestDate = w.CheapestDate
			}
		}
		survivor.LowestPrice = lowest
		survivor.CheapestDate = cheapestDate

		// Merge the rest of the running state across the group, rather than
		// letting richer() hand over one record's fields wholesale.
		//
		// richer() decides which record's OTHER fields survive, on
		// LowestPrice-presence then LastCheck then CreatedAt. LowestPrice,
		// CheapestDate and CreatedAt are already merged explicitly above and
		// below; these three were not, so a recently-renewed duplicate could
		// lose to an older-but-more-recently-checked one and have its state
		// discarded outright (trvl#563).
		//
		//   - RenewedAt: latest wins. It records "the user expressed interest",
		//     and losing the newest stamp leaves the survivor eligible for TTL
		//     expiry even though a group member was renewed moments ago.
		//   - BaselinePrice: the alert high-water reference, so the group's
		//     highest is the true peak observed for this target. A lower one
		//     understates every subsequent drop.
		//   - LastAlertedPrice: the dedup floor. Evaluate stays silent while
		//     current >= LastAlertedAt, so the HIGHEST value suppresses most,
		//     and losing it re-alerts for a drop already reported.
		//
		// The two price fields are currency-denominated, so they merge only
		// within the survivor's currency -- same rule as LowestPrice above.
		for _, w := range group {
			if w.RenewedAt.After(survivor.RenewedAt) {
				survivor.RenewedAt = w.RenewedAt
			}
			if w.Currency != survivor.Currency {
				continue
			}
			if w.BaselinePrice > survivor.BaselinePrice {
				survivor.BaselinePrice = w.BaselinePrice
			}
			if w.LastAlertedPrice > survivor.LastAlertedPrice {
				survivor.LastAlertedPrice = w.LastAlertedPrice
			}
		}

		kept = append(kept, survivor)

		// Every other member of the group maps directly onto the final
		// survivor in one shot -- no pairwise chain to re-point, and no risk
		// of an intermediate ID leaking through a stale mapping. A member
		// whose currency does not match the survivor's is excluded from
		// idMap on purpose: its own price-history points belong to a
		// currency the survivor no longer claims, so retagging them onto the
		// survivor would still mix currencies into one Sparkline/TrendArrow
		// series even though the scalar LowestPrice above is now correct.
		// Leaving it out of idMap lets compactHistoryLocked's live-watch
		// filter drop that history as an orphan instead, same as a direct
		// two-watch currency mismatch.
		for _, w := range group {
			if w.ID == survivor.ID {
				continue
			}
			if w.Currency == survivor.Currency {
				idMap[w.ID] = survivor.ID
			}
		}
	}
	removed := len(s.watches) - len(kept)
	s.watches = kept
	return removed, idMap
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
	// Drop points belonging to watches that no longer exist, including the
	// duplicates just collapsed. Orphaned history is dead weight that the caps
	// would otherwise let linger behind live watches.
	//
	// Also drop points whose CURRENCY no longer matches the live watch's
	// current currency. History written by the pre-round-14/15 poller (or by
	// any future currency change) can leave points denominated in the watch's
	// OLD currency sitting beside points in its current currency under the
	// same WatchID -- reassignHistoryLocked above repoints history by ID only,
	// with no currency check, and the live-watch filter below only asks "does
	// this ID still exist," not "is this point in the ID's current currency."
	// Mixed-currency history under one ID corrupts any consumer that averages
	// or charts a watch's series without checking each point's Currency field
	// individually. A point with no Currency recorded (legacy data predating
	// that field) is kept, since there is nothing to compare against.
	// Found by adversarial review, 2026-07-30 (round 15).
	//
	// Round 18: this filter used to run AFTER the retention-cap pruning
	// below. Provider currency is IP/market-driven and can flip poll-to-poll
	// (round 14/15/16), so a stale-currency point is NOT guaranteed to be
	// chronologically older than every live-currency point under the same
	// WatchID. evictOldestLocked only looks at recency, not currency: capping
	// first could keep a doomed stale-currency point (because it happened to
	// be the newer of the two) at the direct cost of evicting an older but
	// currency-VALID point -- which then never got a chance to survive to
	// this filter, because it was already gone. Filtering orphaned/
	// mismatched-currency points out FIRST guarantees the caps below only
	// ever spend their eviction budget on history that is actually staying.
	// Found by adversarial review, 2026-07-30 (round 18).
	watchCurrency := make(map[string]string, len(s.watches))
	for _, w := range s.watches {
		watchCurrency[w.ID] = w.Currency
	}
	live := make(map[string]bool, len(s.watches))
	for _, w := range s.watches {
		live[w.ID] = true
	}
	// Compact in place (write index n <= read index i, always) instead of
	// allocating a second backing array via `s.history[:0:0]`. This filter
	// now runs before the retention caps below (round 18), so it sees the
	// full pre-cap history -- on a large legacy store that is exactly the
	// case where forcing an extra full-size copy costs the most transient
	// memory for no benefit, since every surviving point gets written
	// exactly once either way. Found by GPT second-opinion review,
	// 2026-07-30 (round 18).
	n := 0
	for _, p := range s.history {
		if p.WatchID != "" && !live[p.WatchID] {
			continue
		}
		if p.WatchID != "" && p.Currency != "" {
			if cur := watchCurrency[p.WatchID]; cur != "" && cur != p.Currency {
				continue
			}
		}
		s.history[n] = p
		n++
	}
	s.history = s.history[:n]

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
}

// backupLocked copies the current on-disk store beside itself, timestamped.
// Missing files are not an error: a fresh store has nothing to back up.
// Caller holds s.mu.
func (s *Store) backupLocked() (string, error) {
	return s.backupPathsLocked([]string{s.databasePath(), s.watchesPath(), s.historyPath()})
}

// backupLegacyLocked is used only for the first JSON-to-bbolt conversion. The
// source files are preserved in place as well; these timestamped copies make
// the exact pre-migration generation explicit and immune to later tooling.
func (s *Store) backupLegacyLocked() (string, error) {
	return s.backupPathsLocked([]string{s.watchesPath(), s.historyPath()})
}

func (s *Store) backupPathsLocked(paths []string) (string, error) {
	stamp := time.Now().Format("20060102-150405")
	var made []string
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", err
		}
		// O_EXCL: never overwrite an existing backup.
		//
		// The stamp is second-resolution, so two migrations in the same second
		// produced the same filename and the second silently destroyed the
		// first's rollback point -- the one file whose entire purpose is to
		// survive a bad migration. A migration that eats its own escape hatch
		// is worse than one that refuses to start. Found by round-2 gpt-review,
		// filed as trvl#562.
		//
		// A suffix is appended rather than failing outright: refusing to migrate
		// because a backup exists would be its own footgun on a retry.
		dst, err := writeNewBackup(path, stamp, data)
		if err != nil {
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
	if err := s.refreshHistoryLocked(); err != nil {
		s.mu.Unlock()
		return MigrationReport{}, fmt.Errorf("refresh history for dry run: %w", err)
	}
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

// writeNewBackup writes data to <path>.bak-<stamp>, or to the first free
// -2, -3, ... suffix if that name is taken. It never truncates an existing
// file: a backup is a rollback point, and silently replacing one loses the
// state a user would actually want back.
//
// Bounded rather than looping forever: 64 collisions in one second means
// something is wrong that another suffix will not fix.
func writeNewBackup(path, stamp string, data []byte) (string, error) {
	base := fmt.Sprintf("%s.bak-%s", path, stamp)
	for i := 0; i < 64; i++ {
		dst := base
		if i > 0 {
			dst = fmt.Sprintf("%s-%d", base, i+1)
		}
		f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if _, err := f.Write(data); err != nil {
			_ = f.Close()
			return "", err
		}
		if err := f.Close(); err != nil {
			return "", err
		}
		return dst, nil
	}
	return "", fmt.Errorf("backup %s: 64 names already taken in the same second", base)
}
