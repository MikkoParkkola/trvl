//go:build windows

package atomicjson

import (
	"os"
	"testing"
	"time"
)

// Windows-only, because processAlive is the one function in this package with a
// genuinely different implementation per platform and the Windows one was a
// stub. These run on windows-latest in CI, which is the only place they can run
// (trvl#568, TRVL.WINORPHAN.3).
//
// The stub returned true unconditionally, so Orphan.Reclaimable was never true
// and `trvl tempfiles --delete` deleted nothing on Windows, ever. Every
// assertion below fails against that stub, which is what makes them a
// regression test rather than a description.

// TRVL.WINORPHAN.1 -- a provably-gone process reports gone.
//
// A PID above the practical maximum cannot name a live process. Windows PIDs are
// multiples of 4 and bounded well below this, so OpenProcess answers
// ERROR_INVALID_PARAMETER -- the one result that means "no such process".
func TestProcessAliveReportsGoneForAnImpossiblePID(t *testing.T) {
	if processAlive(0x7FFFFFF0, time.Time{}) {
		t.Error("an impossible PID reported live; nothing on Windows would ever be reclaimable, " +
			"which is the defect this fix exists to close")
	}
}

// TRVL.WINORPHAN.2 -- the safety property is kept, not traded away.
// This process is unambiguously alive, and its own temp file must be protected.
func TestProcessAliveReportsLiveForThisProcess(t *testing.T) {
	// A file timestamp AFTER this process started, i.e. the ordinary case for a
	// temp file a running writer is in the middle of publishing.
	if !processAlive(os.Getpid(), time.Now()) {
		t.Error("the running test process reported gone; a live writer's temp file would be deleted " +
			"underneath it, which is strictly worse than the leak being fixed")
	}
}

// A non-positive PID means the name predates PID stamping and ownership cannot
// be established. Unknown must read as live.
func TestProcessAliveTreatsUnknownOwnerAsLive(t *testing.T) {
	for _, pid := range []int{0, -1} {
		if !processAlive(pid, time.Now()) {
			t.Errorf("pid %d reported gone; an unestablished owner must protect the file", pid)
		}
	}
}

// TRVL.WINORPHAN.4 -- PID reuse is NOT detected, and this test pins that as a
// deliberate limit rather than leaving it as a silent gap.
//
// The first attempt compared the process creation time against the file's
// modification time: a process that started after the file was written cannot be
// its writer. windows-latest rejected it. TestCleanRetainsLiveOwner creates a
// temp file owned by THIS process, backdates it an hour, and requires it to be
// retained -- and under that comparison it was deleted instead. A live process's
// file removed underneath it is the one failure this function must never have.
//
// The two cases are indistinguishable through a modification time: "old file,
// live owner" and "reused PID" are both a live PID against a file older than the
// process, and mtime is not evidence of when the file was written.
//
// So this asserts the SAFE behaviour, and will fail if someone reintroduces the
// comparison without also solving the ambiguity.
func TestProcessAliveKeepsAnOldFileOwnedByALiveProcess(t *testing.T) {
	ancient := time.Unix(0, 0)
	if !processAlive(os.Getpid(), ancient) {
		t.Error("a file older than this process was treated as PID reuse and its owner reported " +
			"gone; an old file with a live owner must be retained (TRVL.TMP.5), and mtime cannot " +
			"distinguish that case from a reused PID")
	}
}

// The comparison must not be so eager that it discards a file written moments
// before the process that is still writing it -- the slack exists because file
// timestamps and process creation times come from different clocks.
func TestProcessAliveKeepsAFileWrittenJustBeforeNow(t *testing.T) {
	if !processAlive(os.Getpid(), time.Now().Add(-time.Millisecond)) {
		t.Error("a file written a millisecond ago was treated as predating this process; clock " +
			"granularity must not be read as PID reuse")
	}
}
