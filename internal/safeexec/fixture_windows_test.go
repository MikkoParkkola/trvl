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
func writeHangingSpawner(t *testing.T, dir, startedPath, survivorPath string) ([]string, error) {
	t.Helper()
	bat := filepath.Join(dir, "hangs.bat")
	script := "@echo off\r\n" +
		"ping -n 2 127.0.0.1 >nul\r\n" +
		"start \"\" /b cmd /c \"echo started>>\"" + startedPath + "\" & timeout /t 3 /nobreak >nul & echo survived>>\"" + survivorPath + "\"\"\r\n" +
		"timeout /t 30 /nobreak >nul\r\n"
	if err := os.WriteFile(bat, []byte(script), 0o644); err != nil {
		return nil, err
	}
	return []string{"cmd.exe", "/c", bat}, nil
}
