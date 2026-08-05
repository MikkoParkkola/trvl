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
// fileModTime is accepted and deliberately UNUSED here. The Windows
// implementation uses it to detect PID reuse: a process that started after the
// file was written cannot be its writer, so the real owner is gone (trvl#568,
// TRVL.WINORPHAN.4).
//
// Unix has the same hazard and does not close it. A PID reused after a reboot
// makes a stale temp file look owned by a live process forever, which keeps a
// leaked file rather than deleting a live one -- the safe direction, and the
// reason this is a documented gap rather than a bug being shipped. Closing it
// needs a per-process start time, and that is platform-specific work per Unix
// (`/proc/<pid>/stat` field 22 on Linux, `sysctl KERN_PROC_PID` on macOS) with
// no portable route. Filed separately rather than half-done here, so this
// signature carries the parameter and the reason it is ignored, instead of the
// next reader wondering whether the omission was considered.
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
