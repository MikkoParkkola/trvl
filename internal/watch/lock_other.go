//go:build windows

package watch

import "os"

// lockSupported is false here: this build has no advisory *store* lock, so
// withTxn degrades to in-process serialisation only (s.mu). Concurrent
// *processes* on Windows remain last-writer-wins, which is the #512 hazard.
//
// The scheduler lock is unaffected: lock_windows.go implements tryLockFile via
// LockFileEx, so only-one-scheduler is enforced here exactly as on Unix.
//
// Follow-up: the store lock could be implemented the same way. The historical
// reason for the gap was that LockFileEx needs golang.org/x/sys/windows and
// that was a go.mod change out of scope for the original fix; that dependency
// is already present now for the scheduler lock, so the stated blocker no
// longer applies. Left unimplemented deliberately, not forgotten.
const lockSupported = false

func acquireFileLock(string) (*os.File, error) { return nil, nil }

func releaseFileLock(*os.File) {}
