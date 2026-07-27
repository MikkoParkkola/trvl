package safeexec

import (
	"context"
	"errors"
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

// containmentTiming returns the command deadline and the wait that follows it.
//
// Windows gets larger numbers because its fixture spends its first second waiting,
// so that the descendant is created after the job assignment rather than inside
// the window described in #526. The deadline has to outlast that wait or the
// descendant would never exist, and the wait afterwards has to outlast the
// descendant's own seven-second sleep measured from when it spawned.
func containmentTiming() (deadline, settle time.Duration) {
	if runtime.GOOS == "windows" {
		return 3 * time.Second, 8 * time.Second
	}
	return time.Second, 4 * time.Second
}

// TestOutput_ContainsDescendantsOnTimeout pins the containment guarantee the
// implementation actually makes: a descendant that exists by the time the deadline
// arrives does not outlive it. On Unix that is every descendant, because the
// process group is set through SysProcAttr before the process exists. On Windows it
// is every descendant created after the job assignment, which necessarily follows
// Start, leaving a window of microseconds that is #526 and stays open deliberately.
// Closing it needs a suspended start with the assignment before the resume, and
// that was declined because any failure in the sequence leaves the helper never
// running at all.
//
// This is the only automated coverage the Windows job-object path has, so the
// fixture is arranged to sit on the far side of that window rather than skipping
// the platform. Two markers keep the arrangement honest. The descendant writes one
// the moment it starts and another after a 3s sleep. The first must exist, which
// proves the deadline did not simply arrive before there was anything to contain;
// without it a mistimed fixture would pass while asserting nothing. The second must
// not, which is the containment itself.
func TestOutput_ContainsDescendantsOnTimeout(t *testing.T) {
	dir := t.TempDir()
	started := filepath.Join(dir, "descendant.started")
	survivor := filepath.Join(dir, "descendant.survived")

	bin, err := writeHangingSpawner(t, dir, started, survivor)
	if err != nil {
		t.Fatalf("write fixture for %s: %v", runtime.GOOS, err)
	}

	deadline, settle := containmentTiming()
	cmd, _, cancel := Command(context.Background(), deadline, bin[0], bin[1:]...)
	defer cancel()

	start := time.Now()
	if _, err := Output(cmd); err == nil {
		t.Fatal("expected the hung helper to fail")
	}
	// This bound is the #507 regression guard: it is what fails if the deadline
	// stops ending the helper at all. Kept as tight as the platform allows, three
	// seconds over the deadline, because slack here is hang time that passes
	// silently. Wait can run one second past cancellation by WaitDelay, so the
	// remaining margin is two seconds of scheduling.
	if elapsed := time.Since(start); elapsed > deadline+3*time.Second {
		t.Fatalf("Output took %v; the deadline should have ended it near %v", elapsed, deadline)
	}

	// The descendant has to have existed before cleanup ran, or the run proves
	// nothing: a survivor check alone is satisfied just as well by a descendant that
	// never spawned.
	//
	// Asserting that with a bare Stat here, immediately after the deadline, ties the
	// test to how promptly the machine scheduled a shell. It failed exactly that way
	// once, inside a 98-package parallel coverage run, reporting "never started" while
	// containment was working perfectly. Failing safe is the right direction for this
	// guard, but a test that goes red under load is one people learn to rerun rather
	// than read.
	//
	// So the marker is waited for, and then its mtime is checked against the moment
	// the deadline passed. That keeps the strict property, which is that the
	// descendant existed before cleanup rather than merely by the end of the test,
	// without making the schedule decide the verdict.
	deadlinePassed := time.Now()
	info := waitForMarker(t, started, deadline+3*time.Second)
	if info == nil {
		t.Fatalf("the descendant never started on %s; the run proves nothing about containment, so it is a fixture failure rather than a pass", runtime.GOOS)
	}
	if info.ModTime().After(deadlinePassed) {
		// Defensive, and honestly untestable here. On Unix a descendant cannot start
		// after the deadline and survive it, because the process group is signalled,
		// so this branch is unreachable on the platform that can run it. It guards
		// Windows, where a descendant genuinely can escape, and where a fixture whose
		// timing drifted past the deadline would otherwise pass while proving nothing.
		t.Fatalf("the descendant on %s only started %v after the deadline passed; it was never a candidate for cleanup, so this run proves nothing",
			runtime.GOOS, info.ModTime().Sub(deadlinePassed))
	}

	// Outlive the descendant's own sleep: if it were still alive, this is when
	// it would write.
	time.Sleep(settle)

	switch _, err := os.Stat(survivor); {
	case err == nil:
		t.Fatalf("a descendant outlived the timeout on %s; helpers will accumulate exactly as reported in #507", runtime.GOOS)
	case !errors.Is(err, os.ErrNotExist):
		// Absence is the assertion, so only a definite absence can pass. A
		// permission or I/O error is not evidence of containment.
		t.Fatalf("cannot tell whether a descendant survived on %s: %v", runtime.GOOS, err)
	}
}

// waitForMarker polls for path until it exists or the budget runs out, returning its
// FileInfo or nil.
//
// Polling rather than a single Stat because the thing being waited for is a shell
// getting scheduled, which under load takes as long as it takes. The budget only
// bounds how long a genuine fixture failure takes to report; it does not weaken any
// assertion, because the caller separately checks that the marker's mtime precedes
// the moment cleanup ran.
func waitForMarker(t *testing.T, path string, budget time.Duration) os.FileInfo {
	t.Helper()

	limit := time.Now().Add(budget)
	for {
		info, err := os.Stat(path)
		if err == nil {
			return info
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("cannot stat %s: %v", path, err)
		}
		if !time.Now().Before(limit) {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
}
