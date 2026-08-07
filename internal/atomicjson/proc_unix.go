//go:build !windows

package atomicjson

import (
	"errors"
	"os"
	"syscall"
	"time"
)

// processAlive reports whether a process with the given PID currently exists.
// Signal 0 performs the permission and existence checks without delivering a
// signal. EPERM means the process exists but belongs to another user, which
// still counts as live. Any answer other than "definitely gone" is reported as
// live so an ambiguous result protects the temp file.
//
// PID reuse across reboots is handled before this function: new temp names
// carry a boot fingerprint, and FindOrphans treats a different boot as proof
// that the current PID cannot be the writer (#574, TRVL.UNIXORPHAN.1). Missing
// boot data stays ambiguous and therefore live (TRVL.UNIXORPHAN.2). The mutable
// file modification time remains intentionally unused; backdating a live
// writer's file must never make it reclaimable.
func processAlive(pid int, _ time.Time) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return true
	}
	err = p.Signal(syscall.Signal(0))
	switch {
	case err == nil:
		return true
	case errors.Is(err, os.ErrProcessDone), errors.Is(err, syscall.ESRCH):
		return false
	default:
		return true
	}
}
