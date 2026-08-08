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
	if uint64(pid) > uint64(^uint32(0)) {
		// Windows process IDs are DWORDs. An out-of-range persisted owner cannot
		// name a process, but retain the file because this function fails safe.
		return true
	}

	// #nosec G115 -- pid is positive and bounded to a Windows DWORD above.
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

	// TRVL.WINORPHAN.4 (PID reuse) is NOT met here, deliberately, and the reason
	// is worth the space because the obvious fix is wrong.
	//
	// The attempt was: GetProcessTimes gives the process creation time, so a
	// process that started after the file was last written cannot be its writer,
	// and a live PID that post-dates the file means the real owner is gone.
	//
	// windows-latest rejected it, and correctly. TestCleanRetainsLiveOwner
	// creates a temp file owned by THIS process and backdates its modification
	// time by an hour, asserting that an old file with a live owner is retained
	// (TRVL.TMP.5). Under the creation-time comparison that file read as
	// PID-reused and was deleted -- a live process's temp file removed underneath
	// it, which is the one direction this function must never fail in.
	//
	// The flaw is structural rather than a detail. Through a modification time,
	// "an old file whose owner is still running" and "a reused PID" are
	// indistinguishable: both are a live PID against a file older than the
	// process. And mtime is not evidence of when the file was written -- anything
	// can change it, which is exactly what that test does.
	//
	// Boot time does not rescue it either. A file whose mtime predates the last
	// boot cannot belong to any live PID, but the mtime is still the untrusted
	// input, and a CI runner that booted recently would misjudge the same
	// fixture.
	//
	// So a live PID means retain, full stop. Detecting PID reuse needs a handle
	// or identifier that ties the file to the process that made it -- recorded at
	// write time rather than inferred afterwards -- which is a change to the temp
	// file format, not to this function. Left to #568 to decide with that
	// framing.
	//
	// fileModTime is accepted and unused on both platforms for now. Kept in the
	// signature so the parameter and this reasoning stay together; removing it
	// would delete the record of why the obvious approach does not work.
	_ = fileModTime

	return true
}
