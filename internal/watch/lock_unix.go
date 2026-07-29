//go:build darwin || linux || freebsd || netbsd || openbsd || dragonfly

package watch

import (
	"fmt"
	"os"
	"syscall"
)

// lockSupported reports whether cross-process locking is enforced on this
// platform. False on platforms without an implementation here (see
// lock_other.go), where withTxn still reloads and saves under s.mu but offers no
// exclusion between processes; the concurrency tests skip there rather than
// pass vacuously.
const lockSupported = true

// acquireFileLock takes an exclusive advisory lock on path, blocking until it
// is available.
//
// flock is held by the open file description, so two *Store values inside one
// process (each with its own descriptor) exclude each other exactly as two
// processes do — which is what makes the same-process concurrency tests a
// faithful model of the multi-process hazard in #512.
//
// TRVL.STORE.TXN.5: the kernel drops a flock when the holding descriptor is
// closed, including on abnormal process death. A killed writer therefore cannot
// wedge the store for anyone else; there is no lock file state to clean up. The
// lock file itself is never renamed or replaced (unlike the data files, which
// atomicjson swaps by rename), so the inode every holder locks stays identical.
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
