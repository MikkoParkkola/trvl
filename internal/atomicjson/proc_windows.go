//go:build windows

package atomicjson

import (
	"errors"
	"time"

	"golang.org/x/sys/windows"
)

// processAlive reports whether the process that wrote a temp file at fileModTime
// is still running (trvl#568).
//
// This used to return true unconditionally, with a sound reason: os.FindProcess
// on Windows opens a process handle and fails for reasons other than "no such
// process", so an access-denied result on another user's process would read as
// "gone" and could delete a live writer's temp file. Reporting live was the safe
// direction.
//
// The cost of that safety was total. Orphan.Reclaimable requires the owner to be
// provably gone and Clean only deletes what is Reclaimable, so
// `trvl tempfiles --delete` deleted NOTHING on Windows, ever. Every interrupted
// write leaked a full copy of the target file with no supported way to clear it.
//
// The fix keeps the safe direction and stops paying everything for it.
// OpenProcess distinguishes the cases os.FindProcess conflates:
//
//	success                 -> a process with this PID exists
//	ERROR_INVALID_PARAMETER -> no such process; the only "provably gone" answer
//	ERROR_ACCESS_DENIED     -> exists, belongs to someone else: LIVE
//	anything else           -> unknown: LIVE
//
// TRVL.WINORPHAN.2: only ERROR_INVALID_PARAMETER returns false. Every other
// outcome, including every unanticipated one, still protects the file.
//
// PROCESS_QUERY_LIMITED_INFORMATION rather than PROCESS_QUERY_INFORMATION: it is
// the least privilege that answers the question, and it succeeds across
// integrity levels where the wider right is refused. Asking for more access than
// the question needs would turn answerable cases into ERROR_ACCESS_DENIED --
// that is, back into permanent leaks.
func processAlive(pid int, fileModTime time.Time) bool {
	if pid <= 0 {
		return true
	}

	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return false
		}
		return true
	}
	defer func() { _ = windows.CloseHandle(h) }()

	// A handle can outlive the process it names, so an open handle alone does
	// not mean running. STILL_ACTIVE separates a running process from an exited
	// one whose handle is still held somewhere.
	//
	// Spelled as windows.STATUS_PENDING rather than a bare 259: the Win32 name
	// STILL_ACTIVE and the NTSTATUS name STATUS_PENDING are the same value
	// (0x103), and x/sys/windows exports only the latter. Naming it beats a
	// magic number, and beats inventing a local constant that would drift.
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return true
	}
	if code != uint32(windows.STATUS_PENDING) {
		return false
	}

	// TRVL.WINORPHAN.4: PID reuse. A live process holding the owning PID is not
	// necessarily the process that wrote this file -- Windows reassigns PIDs, and
	// across a reboot it certainly has. If the process started AFTER the file was
	// last written, it cannot be the writer, so the real owner is gone.
	//
	// Ambiguity still protects the file: an unreadable creation time, or a zero
	// fileModTime, falls through to live.
	if !fileModTime.IsZero() {
		var creation, exit, kernel, user windows.Filetime
		if err := windows.GetProcessTimes(h, &creation, &exit, &kernel, &user); err == nil {
			started := time.Unix(0, creation.Nanoseconds())
			// A second of slack: file timestamps and process creation times come
			// from different clocks at different granularity, and the failure this
			// guards against is deleting a live writer's file. Err toward keeping.
			if started.After(fileModTime.Add(time.Second)) {
				return false
			}
		}
	}

	return true
}
