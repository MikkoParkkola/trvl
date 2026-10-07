//go:build unix

package batchexec

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// lockRefusalDir takes an exclusive advisory lock so two trvl processes on
// this machine apply a Google refusal one at a time. An empty directory, or
// any failure to create the lock, degrades to no cross-process lock: the
// caller's in-memory mutex still covers goroutines in this process, and a
// lock problem must not fail the search.
func lockRefusalDir(dir string) func() {
	if dir == "" {
		return func() {}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return func() {}
	}
	f, err := os.OpenFile(filepath.Join(dir, "google-refusal.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return func() {}
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		_ = f.Close()
		return func() {}
	}
	return func() {
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
		_ = f.Close()
	}
}
