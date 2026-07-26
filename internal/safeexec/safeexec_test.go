package safeexec

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestOutput_ReturnsStdoutAndDiscardsStderr pins the contract callers rely on:
// stdout comes back, stderr does not. Credential helpers echo secret references
// in their diagnostics, so returning stderr would route secrets into whatever
// log the caller writes.
func TestOutput_ReturnsStdoutAndDiscardsStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fixtures are POSIX")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "noisy")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho on-stdout\necho SECRET-ON-STDERR >&2\n"), 0o755); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	cmd, _, cancel := Command(context.Background(), 5*time.Second, bin)
	defer cancel()

	out, err := Output(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := string(out); got != "on-stdout\n" {
		t.Fatalf("expected stdout only, got %q", got)
	}
	if err != nil && len(out) > 0 {
		t.Fatalf("stderr must not reach the caller")
	}
}

// TestOutput_ContainsDescendantsOnTimeout is the portable half of the
// containment guarantee, and the only automated coverage the Windows job-object
// path gets — CI runs the suite on Windows, this machine cannot.
//
// The helper backgrounds a descendant that would write a marker after 3s, then
// hangs. The command is bounded at 1s, so a correctly contained descendant never
// reaches its write: on Unix the process group is signalled, on Windows the job
// object is closed.
func TestOutput_ContainsDescendantsOnTimeout(t *testing.T) {
	dir := t.TempDir()
	survivor := filepath.Join(dir, "descendant.survived")

	bin, err := writeHangingSpawner(t, dir, survivor)
	if err != nil {
		t.Skipf("no fixture for %s: %v", runtime.GOOS, err)
	}

	cmd, _, cancel := Command(context.Background(), time.Second, bin[0], bin[1:]...)
	defer cancel()

	start := time.Now()
	if _, err := Output(cmd); err == nil {
		t.Fatal("expected the hung helper to fail")
	}
	if elapsed := time.Since(start); elapsed > 6*time.Second {
		t.Fatalf("Output took %v; the deadline should have ended it near 1s", elapsed)
	}

	// Outlive the descendant's own sleep: if it were still alive, this is when
	// it would write.
	time.Sleep(4 * time.Second)

	if _, err := os.Stat(survivor); err == nil {
		t.Fatalf("a descendant outlived the timeout on %s; helpers will accumulate exactly as reported in #507", runtime.GOOS)
	}
}
