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

// safetyValveTimeout is the deadline the containment test hands its helper.
//
// Nothing in that test is meant to be triggered by it. Containment is triggered by
// cancelling the parent context, and only after the descendant has been observed to
// exist, which is what removed the test's dependency on how fast this machine forks a
// shell (#533). The timeout is here so that a bug in that sequence fails the package
// in half a minute instead of hanging until go test's own limit. It is longer than the
// fixture's own hang, so on a passing run it is never what ends the helper.
const safetyValveTimeout = 30 * time.Second

// survivorPollBudget bounds how long the containment test waits for an escaped
// descendant to give itself away.
//
// Absence is the assertion, so this is the one number in the test that still buys
// margin — and what it buys is small and derived rather than tuned. A descendant that
// survived containment is already running and polling for the release file, so it
// writes its marker within one poll interval of that file appearing. Unix polls at
// 50ms. Windows polls at about a second, because its shortest dependable sleep is a
// ping. Both budgets are several intervals of slack.
//
// This is not the old settle wait renamed. That one raced a sleep the descendant had
// already begun, so a slow machine and a contained descendant looked identical. This
// one starts counting only after the test has released a descendant that either
// exists or does not.
func survivorPollBudget() time.Duration {
	if runtime.GOOS == "windows" {
		return 8 * time.Second
	}
	return 3 * time.Second
}

// startedBeforeCleanup reports whether a descendant whose start marker carries
// markerMod existed before the containment ran, given cleanupDone: the instant
// Output returned, which is after Wait, which is after the process group was
// signalled and the job closed.
//
// The cutoff is the observed instant rather than the nominal deadline the command was
// given. Cancellation is delivered by exec.Cmd's watcher goroutine, so the signal
// lands measurably after cancellation was requested, and a descendant that started in
// that interval was in the group that got signalled: it was contained, and the proof
// is that it never writes its survivor marker. Judging it against the requested
// instant calls that run a fixture failure. Under the load described in #533 that
// happened twice in 180 runs of the version of this test that did so.
//
// Since the caller now waits for the start marker before it cancels anything, an
// ordering violation should be unreachable, and this is deliberately kept anyway. It
// is what fails if someone moves the cancellation back ahead of the marker wait, which
// is precisely the arrangement that produced the flake; a comment asking them not to
// would not fail anything. It is cheap, it cannot produce a false red, and the
// property it states is the one the containment claim rests on.
//
// Widening the cutoff to the observed instant cannot pass a descendant that escaped
// containment. A descendant that starts after cleanup is alive after cleanup, so once
// the test releases it, it writes the survivor marker, and the survivor check that
// follows is the assertion that fails.
func startedBeforeCleanup(markerMod, cleanupDone time.Time) bool {
	return !markerMod.After(cleanupDone)
}

// TestOutput_ContainsDescendantsOnTimeout pins the containment guarantee the
// implementation actually makes: a descendant that exists when containment runs does
// not outlive it. On Unix that is every descendant, because the process group is set
// through SysProcAttr before the process exists. On Windows it is every descendant
// created after the job assignment, which necessarily follows Start, leaving a window
// of microseconds that is #526 and stays open deliberately. Closing it needs a
// suspended start with the assignment before the resume, and that was declined because
// any failure in the sequence leaves the helper never running at all.
//
// This is the only automated coverage the Windows job-object path has, so the
// fixture is arranged to sit on the far side of that window rather than skipping
// the platform. Two markers keep the arrangement honest. The descendant writes one
// the moment it starts, and another only once the test releases it. The first must
// exist, which proves containment had something to contain; without it a mistimed
// fixture would pass while asserting nothing. The second must not, which is the
// containment itself.
//
// The order of operations is the whole fix for #533, so it is worth stating plainly.
// The helper is given a deadline it is never expected to reach; the test waits for the
// descendant to exist; only then does it cancel the parent context, which is what
// exercises containment. The production path is unchanged by that choice, because the
// timeout context derives from the parent one: cancelling either fires the same
// cmd.Cancel and the same group kill (harden_unix.go). What changes is that no
// assertion depends on whether this machine forked a shell inside a fixed window.
//
// The elapsed-time claim that used to live here — that the deadline ends a helper that
// ignores it — moved to TestOutput_EndsAHelperThatIgnoresItsDeadline. It was the
// reason this test needed a real deadline, and keeping the two together forced one
// number to serve two assertions that want opposite things from the clock.
func TestOutput_ContainsDescendantsOnTimeout(t *testing.T) {
	dir := t.TempDir()
	started := filepath.Join(dir, "descendant.started")
	survivor := filepath.Join(dir, "descendant.survived")
	release := filepath.Join(dir, "descendant.release")

	bin, err := writeHangingSpawner(t, dir, started, survivor, release)
	if err != nil {
		t.Fatalf("write fixture for %s: %v", runtime.GOOS, err)
	}

	ctx, kill := context.WithCancel(context.Background())
	defer kill()
	cmd, _, cancel := Command(ctx, safetyValveTimeout, bin[0], bin[1:]...)
	defer cancel()

	type outcome struct {
		err  error
		done time.Time
	}
	finished := make(chan outcome, 1)
	go func() {
		_, err := Output(cmd)
		// Output's deferred close has run by the time this reads the clock, so this
		// instant is after the group was signalled and the job handle closed.
		finished <- outcome{err: err, done: time.Now()}
	}()

	// Wait for the descendant before cancelling anything. A bare Stat after a fixed
	// deadline was what failed inside a 98-package parallel coverage run, reporting
	// "never started" while containment was working perfectly.
	info := waitForMarker(t, started, safetyValveTimeout)
	if info == nil {
		t.Fatalf("the descendant never started on %s within %v; the run proves nothing about containment, so it is a fixture failure rather than a pass", runtime.GOOS, safetyValveTimeout)
	}

	kill()

	got := <-finished
	if got.err == nil {
		t.Fatal("expected the cancelled helper to fail")
	}
	if !startedBeforeCleanup(info.ModTime(), got.done) {
		// Unreachable unless the sequence above is reordered; see startedBeforeCleanup
		// for why it is kept anyway. The ordering itself is covered by
		// TestStartedBeforeCleanup.
		t.Fatalf("the descendant on %s only started %v after cleanup finished; it was never a candidate for cleanup, so this run proves nothing",
			runtime.GOOS, info.ModTime().Sub(got.done))
	}

	// Release the descendant. Containment has already run, so a contained descendant
	// is not there to notice this file; one that escaped is polling for it and writes
	// its marker within a poll interval. Creating it only now is what makes an absent
	// survivor mean "killed" rather than "not finished sleeping yet".
	if err := os.WriteFile(release, nil, 0o644); err != nil {
		t.Fatalf("release the descendant: %v", err)
	}

	// waitForMarker fails the test outright on any stat error that is not "does not
	// exist", which matters here because absence is the assertion: a permission or
	// I/O error is not evidence of containment.
	if waitForMarker(t, survivor, survivorPollBudget()) != nil {
		t.Fatalf("a descendant outlived the timeout on %s; helpers will accumulate exactly as reported in #507", runtime.GOOS)
	}
}

// TestOutput_EndsAHelperThatIgnoresItsDeadline is the #507 regression guard: a helper
// that would otherwise run forever is ended by the deadline it was given.
//
// It was an elapsed-time bound inside TestOutput_ContainsDescendantsOnTimeout, which
// is what forced that test to be driven by a real deadline and made sizing the
// deadline a race against the fixture (#533). Split out, it needs no descendant and no
// fixture timing at all: a helper that would run for thirty seconds, a two-second
// deadline, and the assertion that Output returns nowhere near thirty. The margin is
// 15x, so load cannot decide it.
func TestOutput_EndsAHelperThatIgnoresItsDeadline(t *testing.T) {
	deadline := 2 * time.Second

	argv := hangingArgv()
	cmd, _, cancel := Command(context.Background(), deadline, argv[0], argv[1:]...)
	defer cancel()

	start := time.Now()
	if _, err := Output(cmd); err == nil {
		t.Fatal("expected the hung helper to fail")
	}
	elapsed := time.Since(start)

	// Kept as tight as the platform allows, because slack here is hang time that
	// passes silently. Wait can run one second past cancellation by WaitDelay
	// (safeexec.go), so the remaining margin is two seconds of scheduling.
	if elapsed > deadline+3*time.Second {
		t.Fatalf("Output took %v for a helper that hangs for 30s; the deadline should have ended it near %v", elapsed, deadline)
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
// FileInfo or nil. Any stat error other than "does not exist" fails the test, so a
// caller asserting absence cannot be satisfied by an I/O or permission error.
//
// The budget means different things at the two call sites, and both are legitimate
// now. Waiting for the start marker, it is scheduling slack for a live process — the
// caller has not cancelled anything yet, so time spent here is time the descendant has
// to be forked and scheduled, and the budget only bounds how long a genuine fixture
// failure takes to report. Waiting for the survivor marker, it is the interval an
// escaped descendant needs to notice the release file, which is survivorPollBudget.
//
// This is the opposite of what the comment here used to say, and the difference is
// #533. It used to run after containment, when nothing further could write the marker
// on Unix, so polling bought nothing and the scheduling slack had to come out of the
// command deadline. Waiting first is what moved the slack somewhere it costs nothing.
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
