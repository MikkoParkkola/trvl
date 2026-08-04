//go:build !windows

package atomicjson

import (
	"errors"
	"os"
	"syscall"
)

// processAlive reports whether a process with the given PID currently exists.
// Signal 0 performs the permission and existence checks without delivering a
// signal. EPERM means the process exists but belongs to another user, which
// still counts as live. Any answer other than "definitely gone" is reported as
// live so an ambiguous result protects the temp file.
func processAlive(pid int) bool {
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
