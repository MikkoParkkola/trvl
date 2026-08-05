//go:build windows

package watch

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// lockSupported is true here: Windows takes a real exclusive store lock via
// LockFileEx, the same primitive lock_windows.go already uses for the
// only-one-scheduler lock (trvl#556).
//
// This file previously held no-op stubs, with a comment recording that the
// historical blocker -- LockFileEx needing golang.org/x/sys/windows, a go.mod
// change out of scope at the time -- no longer applied, because the scheduler
// lock had already brought that dependency in. This closes the gap that comment
// describes.
//
// While the stubs were in place, withTxn on Windows retained only s.mu, so two
// concurrent trvl processes -- a scheduler tick and an MCP watch_price call, say
// -- were last-writer-wins. That is the #512 hazard the transaction work closed
// on Unix. The regression test for it (internal/watch/store_crosslock_test.go)
// loses 39 of 40 concurrent writes when the lock is removed on Unix. There was
// never reason to think Windows fared better, only that nobody had measured it.
const lockSupported = true

// acquireFileLock takes an exclusive lock on path, blocking until it is
// available.
//
// LOCKFILE_EXCLUSIVE_LOCK WITHOUT LOCKFILE_FAIL_IMMEDIATELY is the blocking
// form, and that is the difference between this and tryLockFile in
// lock_windows.go. tryLockFile answers "is anyone else holding it?" for the
// scheduler and must not wait. This must wait: a store mutation that gave up on
// contention would cause the data loss it exists to prevent.
//
// TRVL.STORE.WIN.4: Windows releases file locks when the owning handle closes,
// including on abnormal process termination, so a killed writer cannot wedge the
// store and there is no lock-file state to clean up. That is the same property
// flock gives on Unix, by a different mechanism -- the kernel owns it either
// way, rather than a cleanup path this code would have to get right.
//
// The byte range (offset 0, length 1) matches tryLockFile. Locking one byte
// rather than the whole file is deliberate: the range is what excludes, and a
// zero-length file has no byte 0 to contend over, so a fixed one-byte range
// keeps every holder fighting for the same thing regardless of file size.
//
// The lock file is never renamed or replaced -- unlike the data files, which
// atomicjson swaps by rename -- so every holder locks the same file rather than
// each locking whatever happened to sit at the path when it opened.
func acquireFileLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	var overlapped windows.Overlapped
	if err := windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK,
		0, 1, 0, &overlapped,
	); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return f, nil
}

// releaseFileLock unlocks and closes the handle.
//
// Closing alone would release the lock, since Windows drops locks with the
// handle. Unlocking first anyway mirrors the Unix path and keeps the release
// explicit rather than a side effect that a future reordering could remove
// without anyone noticing.
func releaseFileLock(f *os.File) {
	if f == nil {
		return
	}
	var overlapped windows.Overlapped
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &overlapped)
	_ = f.Close()
}
