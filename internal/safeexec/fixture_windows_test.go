//go:build windows

package safeexec

import (
	"os"
	"path/filepath"
	"testing"
)

// writeHangingSpawner writes a helper that starts a descendant which would touch
// survivorPath after 3s, then hangs. The descendant is a detached cmd.exe, so a
// build that only kills the direct child leaves it running — which is exactly
// what the job object must prevent.
func writeHangingSpawner(t *testing.T, dir, survivorPath string) ([]string, error) {
	t.Helper()
	bat := filepath.Join(dir, "hangs.bat")
	script := "@echo off\r\n" +
		"start \"\" /b cmd /c \"timeout /t 3 /nobreak >nul & echo survived>>\"" + survivorPath + "\"\"\r\n" +
		"timeout /t 30 /nobreak >nul\r\n"
	if err := os.WriteFile(bat, []byte(script), 0o644); err != nil {
		return nil, err
	}
	return []string{"cmd.exe", "/c", bat}, nil
}
