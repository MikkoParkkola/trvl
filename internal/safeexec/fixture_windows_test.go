//go:build windows

package safeexec

import (
	"os"
	"path/filepath"
	"testing"
)

// hangingArgv returns a command that blocks for far longer than any deadline a test
// gives it, and spawns nothing. It is the helper for the #507 deadline guard, which
// wants only "this would run forever if nothing stopped it".
//
// A ping rather than `timeout`, for the reason set out on writeHangingSpawner: stdin
// is the null device here, and `timeout` refuses to run with stdin redirected. It
// would exit at once and the test would assert nothing.
//
// The trailing non-zero exit is deliberate, for the reason given on the Unix twin: a
// build that lost its deadline must fail the guard's elapsed-time bound rather than its
// error check, or the #507 property is not the thing being tested.
func hangingArgv() []string {
	return []string{"cmd.exe", "/c", "ping -n 31 127.0.0.1 & exit /b 1"}
}

// writeHangingSpawner writes a helper that spawns a detached descendant and then
// hangs. It returns the argv to run.
//
// The descendant appends to startedPath the moment it runs, then polls for
// releasePath, and only then appends to survivorPath. The caller creates releasePath
// after containment has finished, so an absent survivor means the descendant was
// terminated rather than merely slow. This replaces a fixed sleep that had to be
// timed to land after the job handle closed, which made the test a race the machine
// arbitrated (#533); the rendezvous removes the timing from both sides.
//
// The descendant is a detached cmd.exe, so a build that only kills the direct child
// leaves it running, which is exactly what the job object must prevent.
//
// The leading ping is a one-second sleep, and it is the whole reason this fixture
// differs from the Unix one. A job object cannot be assigned to a process that does
// not exist yet, so the assignment necessarily follows Start (safeexec.go, then
// harden_windows.go). Spawning the descendant as the batch file's first statement
// lands inside that window and escapes containment by design, which is #526 and not
// what this test is for. Waiting first puts the descendant on the far side of the
// assignment, where containment is a guarantee the implementation actually makes.
//
// startedPath is what stops that delay from turning a real failure into a pass. If
// the process were killed before the descendant spawned, nothing would have been
// contained and the survivor check alone would still be satisfied. The caller waits
// for startedPath before it cancels anything, so a fixture that mistimes itself
// cannot go quietly green.
//
// Every wait is a ping rather than `timeout`, and that is not a style choice.
// safeexec sets cmd.Stdin to nil (safeexec.go:52), which on Windows is the NUL
// device, and `timeout` treats any redirected stdin as an error: it prints
// "ERROR: Input redirection is not supported" and exits immediately. A fixture built
// on it would collapse every wait at once, so the helper would not hang and the
// descendant would write its survivor marker straight away. The earlier version of
// this fixture used `timeout`, and the CI failure it produced looks identical either
// way, which is why the defect survived a green-to-red reading. `ping` does not
// consult stdin.
//
// The poll also gives up if dir stops existing, for the reason set out on the Unix
// twin: an early t.Fatal never writes the release file, so an escaped descendant would
// otherwise poll forever and outlive the test binary. That check is weaker here than on
// Unix, and the gap is stated rather than papered over: RemoveAll can fail while the
// helper is still mapped, and a directory that never disappears never trips this
// condition. The Unix measurement showed a t.Cleanup release write does not close that
// gap — the file it creates is deleted before a polling descendant samples for it — so
// no second mechanism is attempted. If this leaks, it leaks on the windows CI job, and
// the symptom is a temp directory that survives the run.
//
// The descendant is its own batch file rather than a chain of `&`-separated commands
// inside `start "" /b cmd /c "…"`. The poll loop needs a label and a conditional, and
// nesting that in an already-quoted argument means quoting three levels deep for
// paths that come from t.TempDir. A separate file spends one write to remove that
// class of bug entirely.
func writeHangingSpawner(t *testing.T, dir, startedPath, survivorPath, releasePath string) ([]string, error) {
	t.Helper()

	descendant := filepath.Join(dir, "descendant.bat")
	descendantScript := "@echo off\r\n" +
		"echo started>>\"" + startedPath + "\"\r\n" +
		":wait\r\n" +
		"if exist \"" + releasePath + "\" goto release\r\n" +
		"if not exist \"" + dir + "\" goto done\r\n" +
		"ping -n 2 127.0.0.1 >nul\r\n" +
		"goto wait\r\n" +
		":release\r\n" +
		"echo survived>>\"" + survivorPath + "\"\r\n" +
		":done\r\n"
	if err := os.WriteFile(descendant, []byte(descendantScript), 0o644); err != nil {
		return nil, err
	}

	bat := filepath.Join(dir, "hangs.bat")
	script := "@echo off\r\n" +
		"ping -n 2 127.0.0.1 >nul\r\n" +
		"start \"\" /b cmd /c \"" + descendant + "\"\r\n" +
		"ping -n 31 127.0.0.1 >nul\r\n"
	if err := os.WriteFile(bat, []byte(script), 0o644); err != nil {
		return nil, err
	}
	return []string{"cmd.exe", "/c", bat}, nil
}
