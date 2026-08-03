//go:build !windows

package watch

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// tryLockFile takes a non-blocking exclusive flock. Returns false (not an error)
// when another process holds it. flock is released by the kernel on process
// exit, so a crashed holder cannot wedge the lock.
func tryLockFile(f *os.File) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return false, nil
	}
	return false, err
}

func unlockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}

// lockSupported reports whether cross-process locking is enforced on this
// platform. See lock_other.go for the degraded fallback on platforms without
// an implementation here.
const lockSupported = true

// acquireFileLock takes an exclusive advisory lock on path, blocking until it
// is available. Used to serialise the whole read-modify-write cycle of a store
// transaction across processes (see withTxn in store.go), as distinct from
// tryLockFile's non-blocking try used by the scheduler singleton.
//
// flock is held by the open file description, so two *Store values inside one
// process (each with its own descriptor) exclude each other exactly as two
// processes do.
//
// The kernel drops a flock when the holding descriptor is closed, including on
// abnormal process death, so a killed writer cannot wedge the store for anyone
// else.
func acquireFileLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if err == nil {
			return f, nil
		}
		if err == syscall.EINTR {
			continue
		}
		_ = f.Close()
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
}

func releaseFileLock(f *os.File) {
	if f == nil {
		return
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	_ = f.Close()
}
