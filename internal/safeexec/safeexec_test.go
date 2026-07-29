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
//
// The Unix deadline is bracketed on both sides and cannot be picked freely (#533).
// Below it, the descendant is a shell that has to be forked, exec'd and scheduled;
// if the deadline arrives first the process group is signalled before the marker
// exists and the run fails reporting a fixture problem while containment worked.
// Measured on darwin/arm64 with 24 CPU burners and 32 fork/exec loops, load average
// 350: 40 samples of the interval between launch and the marker's mtime ran from
// 298ms to 1.19s, so the one second this used to be sat inside the distribution and
// lost roughly a quarter of the time. Above it, the fixture's descendant writes its
// survivor marker three seconds after it spawns, and a deadline that arrives after
// that would see a legitimately-written survivor and report a containment failure.
// Two seconds is the midpoint: 1.7x the measured worst spawn, and a second clear of
// the survivor write.
//
// This widens the race rather than closing it. Closing it means giving the
// descendant a longer sleep before it writes the survivor, which lives in
// fixture_unix_test.go and its Windows counterpart, not here.
func containmentTiming() (deadline, settle time.Duration) {
	if runtime.GOOS == "windows" {
		return 3 * time.Second, 8 * time.Second
	}
	return 2 * time.Second, 4 * time.Second
}

// startedBeforeCleanup reports whether a descendant whose start marker carries
// markerMod existed before the containment ran, given cleanupDone: the instant
// Output returned, which is after Wait, which is after the process group was
// signalled and the job closed.
//
// The cutoff is the observed instant rather than the nominal deadline the command
// was given. Cancellation is delivered by exec.Cmd's watcher goroutine, so under
// load the signal lands measurably after the deadline nominally expired, and a
// descendant that started in that interval was in the group that got signalled: it
// was contained, and the proof is that it never writes its survivor marker. Judging
// it against the nominal instant calls that run a fixture failure. It happened
// twice in 180 runs under the load described above.
//
// Widening the cutoff to the observed instant cannot pass a descendant that escaped
// containment. A descendant that starts after cleanup is alive after cleanup, so it
// sleeps and writes the survivor marker, and the survivor check that follows is the
// assertion that fails.
func startedBeforeCleanup(markerMod, cleanupDone time.Time) bool {
	return !markerMod.After(cleanupDone)
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
	// Output has returned, so Wait has returned, so the group was signalled and the
	// containment closed. Nothing the helper spawned before this instant is still
	// running unless containment failed.
	cleanupDone := time.Now()
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
	// So the marker is waited for, and then its mtime is checked against the instant
	// cleanup finished. That keeps the strict property, which is that the descendant
	// existed before cleanup rather than merely by the end of the test, without making
	// the schedule decide the verdict. See startedBeforeCleanup for why the cutoff is
	// the observed instant and not the nominal deadline.
	info := waitForMarker(t, started, deadline+3*time.Second)
	if info == nil {
		t.Fatalf("the descendant never started on %s; the run proves nothing about containment, so it is a fixture failure rather than a pass", runtime.GOOS)
	}
	if !startedBeforeCleanup(info.ModTime(), cleanupDone) {
		// Guards Windows, where a descendant genuinely can escape the job, and any
		// platform whose fixture timing drifted so far that the descendant appeared
		// after containment had already run: that run proves nothing and must not
		// pass. The ordering itself is covered by TestStartedBeforeCleanup.
		t.Fatalf("the descendant on %s only started %v after cleanup finished; it was never a candidate for cleanup, so this run proves nothing",
			runtime.GOOS, info.ModTime().Sub(cleanupDone))
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

// TestStartedBeforeCleanup pins the cutoff the containment test judges against.
//
// The case that matters is the third one: a descendant whose marker lands after the
// nominal deadline but before cleanup finished. Cancellation is delivered by a
// goroutine, so that descendant was in the group that got signalled and is contained.
// Judging it against nominalDeadline, which is what this test file did before #533,
// calls it a fixture failure. That is the flake.
func TestStartedBeforeCleanup(t *testing.T) {
	launch := time.Unix(1700000000, 0)
	deadline := 2 * time.Second
	nominalDeadline := launch.Add(deadline)
	// Cleanup lands after the nominal deadline: exec's watcher has to be scheduled
	// before it can signal, and Wait has to return before Output does.
	cleanupDone := nominalDeadline.Add(400 * time.Millisecond)

	for _, tc := range []struct {
		name   string
		marker time.Time
		want   bool
	}{
		{"marker well inside the deadline", launch.Add(300 * time.Millisecond), true},
		{"marker on the nominal deadline", nominalDeadline, true},
		{"marker inside the cancellation lag", nominalDeadline.Add(150 * time.Millisecond), true},
		{"marker on the cleanup instant", cleanupDone, true},
		{"marker after cleanup finished", cleanupDone.Add(time.Millisecond), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := startedBeforeCleanup(tc.marker, cleanupDone); got != tc.want {
				t.Fatalf("startedBeforeCleanup(marker %v after launch) = %v, want %v",
					tc.marker.Sub(launch), got, tc.want)
			}
		})
	}
}

// waitForMarker polls for path until it exists or the budget runs out, returning its
// FileInfo or nil.
//
// Polling rather than a single Stat only covers the gap between the descendant's
// write returning and the file becoming visible here. It does not buy time for a
// shell that has not been scheduled yet: by the time this runs the group has already
// been signalled, so on Unix nothing further can write the marker, and a marker that
// does appear later is rejected by the caller's ordering check anyway. Scheduling
// slack is bought by the deadline in containmentTiming, which is the only place that
// can buy it. The budget here bounds how long a genuine fixture failure takes to
// report; it weakens no assertion, because the caller separately checks that the
// marker's mtime precedes the instant cleanup finished.
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
