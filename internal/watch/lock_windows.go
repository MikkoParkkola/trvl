//go:build windows

package watch

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// tryLockFile takes a non-blocking exclusive byte-range lock. Returns false
// (not an error) when another process holds it. Windows releases file locks when
// the owning handle closes, including on abnormal process termination, so a
// crashed holder cannot wedge the lock.
func tryLockFile(f *os.File) (bool, error) {
	var overlapped windows.Overlapped
	err := windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, &overlapped,
	)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
		return false, nil
	}
	return false, err
}

func unlockFile(f *os.File) error {
	var overlapped windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &overlapped)
}

// acquireFileLock takes an exclusive byte-range lock on path, blocking until
// it is available. Used to serialise the whole read-modify-write cycle of a
// store transaction across processes (see withTxn in store.go), as distinct
// from tryLockFile's non-blocking try used by the scheduler singleton.
//
// Windows releases file locks when the owning handle closes, including on
// abnormal process termination, so a killed writer cannot wedge the store for
// anyone else.
func acquireFileLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	var overlapped windows.Overlapped
	err = windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK,
		0, 1, 0, &overlapped,
	)
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return f, nil
}

func releaseFileLock(f *os.File) {
	if f == nil {
		return
	}
	var overlapped windows.Overlapped
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &overlapped)
	_ = f.Close()
}
