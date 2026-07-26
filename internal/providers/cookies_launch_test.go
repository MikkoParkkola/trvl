package providers

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestDefaultOpenURL_DoesNotWaitForTheBrowser pins the reason these launches use
// Start rather than Run.
//
// Waiting was a latent hang on a search path: `xdg-open` can block for as long
// as the browser it launched stays open, and the whole point of the call is that
// the browser stays open long enough for a human to solve a challenge. Bounding
// it the way trvl bounds credential helpers would be worse than the defect,
// because on Linux the browser is a child of the launcher and killing the group
// would shut the window in the user's face.
//
// The fake launcher hangs for 30s. A build that waits on it fails here.
func TestDefaultOpenURL_DoesNotWaitForTheBrowser(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fixtures are POSIX")
	}

	launcher := "open"
	if runtime.GOOS == "linux" {
		launcher = "xdg-open"
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, launcher), []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatalf("write fake %s: %v", launcher, err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	start := time.Now()
	err := defaultOpenURL(runtime.GOOS, "", "https://example.invalid/challenge")
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error launching the browser: %v", err)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("defaultOpenURL waited %v for the launcher; it must not block a search on a window the user is meant to keep open", elapsed)
	}
}

// TestDefaultOpenURL_ReportsAMissingLauncher confirms the failure that actually
// matters still surfaces. Start does not report exit status, but it does report
// that there was nothing to run.
func TestDefaultOpenURL_ReportsAMissingLauncher(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH emptying is not a reliable way to hide cmd.exe")
	}
	t.Setenv("PATH", t.TempDir())

	if err := defaultOpenURL(runtime.GOOS, "", "https://example.invalid/challenge"); err == nil {
		t.Fatal("expected an error when the platform launcher is absent")
	}
}
