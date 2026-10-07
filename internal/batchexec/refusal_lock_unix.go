//go:build unix

package batchexec

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// tryLockFile takes an exclusive advisory lock without blocking. false with a
// nil error means another process holds it.
func tryLockFile(f *os.File) (bool, error) {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, unix.EWOULDBLOCK) {
		return false, nil
	}
	return false, err
}

func unlockFile(f *os.File) {
	_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
