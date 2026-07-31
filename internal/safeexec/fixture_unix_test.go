//go:build unix

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
// It exits non-zero of its own accord, which looks redundant next to a deadline that
// kills it, and is not. A build that lost its deadline runs this to completion; if
// completion were a clean exit, the guard's first assertion — that Output reports a
// failure — would fire, and the elapsed-time bound that is the actual #507 property
// would never be evaluated. Failing late instead of failing clean puts the verdict on
// the bound. Verified by sabotage: with the timeout removed from Command, the run fails
// on the elapsed bound rather than on the error check.
func hangingArgv() []string {
	return []string{"sh", "-c", "sleep 30; exit 1"}
}

// writeHangingSpawner writes a helper that backgrounds a descendant and then hangs.
// It returns the argv to run.
//
// The descendant appends to startedPath the moment it runs, then waits for
// releasePath to appear, and only then appends to survivorPath. The caller creates
// releasePath after containment has finished, so an absent survivor means the
// descendant was killed rather than merely slow: a live descendant is polling, and
// writes within a poll interval of being released.
//
// That rendezvous replaces a fixed sleep, and the replacement is the point. The
// descendant used to sleep three seconds and then write, which made the test a race
// between that sleep and the kill, arbitrated by a deadline picked to land between
// them. Under load the deadline lost, and the run failed reporting a fixture problem
// while containment had worked perfectly (#533). Nothing here is timed now: the
// descendant cannot write early, because it is waiting on a file only the test
// creates, and the test cannot check too early, because it does not look for the
// survivor until after it has released it.
//
// The loop also gives up if dir stops existing, and that second condition is a
// correctness fix rather than tidiness. Every t.Fatal between the start-marker wait
// and the release write leaves the release file unwritten, so a descendant that
// escaped containment would poll at 20Hz forever, outliving the test binary — the leak
// happening precisely on the runs where containment is broken, which is #507 again.
//
// The directory is the guard rather than a t.Cleanup release write, and the reason is
// measured rather than argued. A cleanup write does run before t.TempDir's RemoveAll —
// cleanups are LIFO and the RemoveAll was registered first — but the file it creates is
// deleted microseconds later, while this loop samples every 50ms, so the descendant
// almost never sees it. With containment sabotaged and an early fatal forced, an
// orphaned descendant was still polling 41 seconds after the test binary exited with a
// cleanup write in place, and exited cleanly with this directory check instead. So the
// cleanup write was dropped: it cost a line and changed nothing. One case is left
// uncovered by design — a RemoveAll that fails leaves the directory in place, so this
// condition never trips — and it is not worth a second mechanism, because a temp-dir
// removal that fails on this platform is already a broken runner.
//
// The fractional sleep has an integer fallback for the same reason the Windows
// fixture avoids `timeout`: a fixture that fails open here would spin instead of
// waiting, which is a CPU hog on a shared runner. Every mainstream /bin/sleep takes
// fractions; the fallback costs one term and removes the question.
//
// The descendant spawns straight away, with no wait ahead of it. The Windows fixture
// has to delay, because its containment starts at job assignment and the assignment
// follows Start (#526). A process group is set through SysProcAttr before the process
// exists, so here there is no window to spawn into and the strict version of the test
// is the correct one.
func writeHangingSpawner(t *testing.T, dir, startedPath, survivorPath, releasePath string) ([]string, error) {
	t.Helper()
	bin := filepath.Join(dir, "hangs")
	script := "#!/bin/sh\n" +
		"( echo started >> \"" + startedPath + "\"\n" +
		"  while [ ! -f \"" + releasePath + "\" ] && [ -d \"" + dir + "\" ]; do sleep 0.05 2>/dev/null || sleep 1; done\n" +
		"  [ -f \"" + releasePath + "\" ] && echo survived >> \"" + survivorPath + "\" ) &\n" +
		"sleep 30\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		return nil, err
	}
	return []string{bin}, nil
}
