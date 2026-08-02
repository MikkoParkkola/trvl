package watch

import (
	"errors"
	"fmt"
	"path/filepath"
	"time"
)

// errTxnNoop is returned by a withTxn callback that decided, after seeing
// committed state, that there is nothing to write. The transaction unwinds
// without saving and withTxn reports success. It is never surfaced to callers.
var errTxnNoop = errors.New("watch: transaction is a no-op")

// lockPath is the dedicated advisory-lock file for this store directory.
//
// It is deliberately NOT one of the data files. atomicjson.Write persists by
// writing a temporary file and renaming it over the target, which swaps the
// inode; a lock held on watches.json would be silently dropped by the very save
// it was meant to guard. The lock file is created once and never replaced.
func (s *Store) lockPath() string {
	return filepath.Join(s.dir, ".lock")
}

// txnStage names an observable point inside a store transaction. Tests use
// txnHook to park a writer at one of these points and assert from *inside* the
// window rather than after it has closed.
type txnStage string

const (
	// stageAfterReload fires once the transaction has reloaded committed state
	// from disk and is about to apply the caller's mutation.
	stageAfterReload txnStage = "after-reload"
	// stageBeforeSave fires once the mutation has been applied in memory and is
	// about to be written back.
	stageBeforeSave txnStage = "before-save"
)

// txnHook, when non-nil, is invoked at each txnStage while the store holds both
// s.mu and the cross-process file lock. Test-only; nil in production. It is set
// and cleared by tests on a single Store before any concurrency starts, so it
// needs no synchronisation of its own.
var txnHook func(s *Store, stage txnStage)

func (s *Store) fireTxnHook(stage txnStage) {
	if txnHook != nil {
		txnHook(s, stage)
	}
}

// withTxn runs apply as a single atomic read-modify-write against the on-disk
// store: acquire the process mutex, acquire the cross-process advisory lock,
// reload committed state, apply, save, release.
//
// This is the fix for #512. Previously every mutation applied to whatever
// snapshot the process happened to be holding and then rewrote both files
// wholesale, so a check and the mutation it guarded were not atomic and two
// processes silently clobbered each other. Here the check and the mutation live
// under one lock, and the state being checked is the state on disk, not a stale
// in-memory copy.
//
// TRVL.STORE.TXN.4: apply must never perform network I/O — the lock is held for
// its whole duration. Every caller in this package computes provider results
// first and only then opens a transaction to persist them, so the lock is held
// for a file reload and two writes, never for a provider round trip. The
// callback is unexported precisely so no out-of-package caller can widen it.
func (s *Store) withTxn(apply func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.withTxnLocked(apply)
}

// withTxnLocked is withTxn for callers that already hold s.mu.
func (s *Store) withTxnLocked(apply func() error) error {
	if err := s.ensureDir(); err != nil {
		return fmt.Errorf("create storage dir: %w", err)
	}
	lock, err := acquireFileLock(s.lockPath())
	if err != nil {
		return err
	}
	defer releaseFileLock(lock)

	if err := s.loadLocked(); err != nil {
		return err
	}
	s.fireTxnHook(stageAfterReload)

	if err := apply(); err != nil {
		if errors.Is(err, errTxnNoop) {
			return nil // nothing changed; skip the write entirely
		}
		return err
	}
	s.fireTxnHook(stageBeforeSave)

	return s.saveLocked()
}

// Mutate applies a field-level edit to the watch with the given ID inside a
// single store transaction, and returns the resulting record.
//
// This is the part of #512 that a lock alone does not solve. Reloading fixes
// interference between *different* watches; it does nothing about two writers
// racing on *the same* record, because UpdateWatch replaces the whole struct and
// therefore reverts every field the other writer touched. Mutate hands the
// caller a pointer to the freshly reloaded record so it can write only the
// fields it owns, leaving concurrent edits to other fields intact
// (TRVL.STORE.TXN.2).
//
// apply runs under both locks: it must be pure bookkeeping, no I/O.
func (s *Store) Mutate(id string, apply func(*Watch)) (Watch, error) {
	var out Watch
	err := s.withTxn(func() error {
		for i := range s.watches {
			if s.watches[i].ID == id {
				apply(&s.watches[i])
				out = s.watches[i]
				return nil
			}
		}
		return fmt.Errorf("watch %s not found", id)
	})
	return out, err
}

// MutateAndRecordPrice applies a field-level edit to the watch with the given
// ID, optionally purges that watch's prior-currency price history, and appends
// a new price point -- all inside ONE store transaction.
//
// It exists because two independently-developed fixes each solved half of the
// same problem and neither subsumes the other:
//
//   - UpdateWatchAndRecordPrice made the purge, the append and the watch update
//     share a single save, closing a crash-between-writes window that could
//     leave a new-currency watch beside old-currency history (round 11,
//     2026-07-29). But it wrote back a whole detached Watch, so a concurrent
//     edit to any field was reverted.
//   - Mutate re-read the committed record and wrote only caller-owned fields,
//     so concurrent edits survive (#512, TRVL.STORE.TXN.2). But it left the
//     history append as a separate RecordPrice call, reopening the very window
//     the first fix closed.
//
// Combining them is not a merge of two code paths but a single one that keeps
// both guarantees: apply runs against freshly reloaded state (Mutate's
// property) and the resulting history mutation is written by the same save
// (UpdateWatchAndRecordPrice's property).
//
// purgeHistory is decided BY the callback, from the reloaded record, rather
// than passed in by the caller. That distinction is the whole point: a caller
// computing "the currency changed" from the snapshot it took before its
// provider round trip can be wrong by the time the lock is acquired. Another
// process may have already performed that migration -- re-applying it here
// would purge the history the other process just wrote in the NEW currency and
// zero the threshold it just re-set. The decision has to be made against the
// state being written, not against a copy from before the network call.
//
// TRVL.STORE.TXN.4: apply must never perform network I/O; the lock is held for
// its whole duration.
func (s *Store) MutateAndRecordPrice(id string, price float64, currency string, apply func(cur *Watch) (purgeHistory bool)) (Watch, error) {
	var out Watch
	err := s.withTxn(func() error {
		idx := -1
		for i := range s.watches {
			if s.watches[i].ID == id {
				idx = i
				break
			}
		}
		if idx < 0 {
			return fmt.Errorf("watch %s not found", id)
		}
		purgeHistory := apply(&s.watches[idx])
		out = s.watches[idx]

		if purgeHistory {
			s.purgeHistoryLocked(id)
		}
		s.history = append(s.history, PricePoint{
			WatchID:   id,
			Price:     price,
			Currency:  currency,
			Timestamp: time.Now(),
		})
		s.pruneWatchLocked(id)
		s.pruneGlobalWatchLocked()
		return nil
	})
	return out, err
}
