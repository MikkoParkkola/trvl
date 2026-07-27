//go:build windows

package safeexec

import (
	"os"
	"path/filepath"
	"testing"
)

// writeHangingSpawner writes a helper that starts a descendant which touches
// startedPath immediately, would touch survivorPath 3s later, and then hangs.
// The descendant is a detached cmd.exe, so a build that only kills the direct
// child leaves it running, which is exactly what the job object must prevent.
//
// The leading ping is a one-second sleep, and it is the whole reason this fixture
// differs from the Unix one. A job object cannot be assigned to a process that
// does not exist yet, so the assignment necessarily follows Start
// (safeexec.go, then harden_windows.go). Spawning the descendant as the batch
// file's first statement lands inside that window and escapes containment by
// design, which is #526 and not what this test is for. Waiting first puts the
// descendant on the far side of the assignment, where containment is a guarantee
// the implementation actually makes.
//
// startedPath is what stops the delay from turning a real failure into a pass.
// If the deadline were to arrive before the descendant spawned, nothing would
// have been contained and the survivor check alone would still be satisfied. The
// caller asserts that startedPath exists, so a fixture that mistimes itself
// fails loudly instead of going quietly green.
// Both waits are pings rather than `timeout`, and that is not a style choice.
// safeexec sets cmd.Stdin to nil (safeexec.go:52), which on Windows is the NUL
// device, and `timeout` treats any redirected stdin as an error: it prints
// "ERROR: Input redirection is not supported" and exits immediately. A fixture
// built on it would collapse both waits at once, so the helper would not hang and
// the descendant would write its survivor marker straight away. The earlier
// version of this fixture used `timeout`, and the CI failure it produced looks
// identical either way, which is why the defect survived a green-to-red reading.
// `ping` does not consult stdin.
func writeHangingSpawner(t *testing.T, dir, startedPath, survivorPath string) ([]string, error) {
	t.Helper()
	bat := filepath.Join(dir, "hangs.bat")
	script := "@echo off\r\n" +
		"ping -n 2 127.0.0.1 >nul\r\n" +
		"start \"\" /b cmd /c \"echo started>>\"" + startedPath + "\" & ping -n 4 127.0.0.1 >nul & echo survived>>\"" + survivorPath + "\"\"\r\n" +
		"ping -n 31 127.0.0.1 >nul\r\n"
	if err := os.WriteFile(bat, []byte(script), 0o644); err != nil {
		return nil, err
	}
	return []string{"cmd.exe", "/c", bat}, nil
}
