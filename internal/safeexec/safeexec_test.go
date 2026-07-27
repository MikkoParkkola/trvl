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

// TestOutput_ContainsDescendantsOnTimeout covers the containment guarantee on the
// platforms where it holds.
//
// The helper backgrounds a descendant that would write a marker after 3s, then
// hangs. The command is bounded at 1s, so a contained descendant never reaches its
// write: on Unix the process group is signalled.
//
// It does not run on Windows, and the reason is a real limit rather than an
// inconvenience. A job object can only be assigned to a process that already
// exists, so the assignment necessarily follows Start (safeexec.go:99, then
// harden_windows.go:88). That leaves a window of microseconds in which a child is
// not yet a job member, and this test's Windows fixture creates its descendant with
// `start "" /b` as the batch file's very first statement, so it lands inside that
// window every time. The test would be asserting a guarantee the implementation
// deliberately does not make.
//
// What Windows does still get is the bound: the helper is killed at its deadline and
// Output returns, which is the hang from #507. Only an immediately-spawned
// grandchild can outlive it. Closing the window needs a suspended start with the
// assignment before the resume, which was declined because any failure in that
// sequence leaves the helper never running at all. Tracked with the full reasoning
// in #526.
func TestOutput_ContainsDescendantsOnTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("descendant containment is not guaranteed on Windows: the job assignment necessarily follows Start, and this fixture spawns inside that window (#526)")
	}

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
