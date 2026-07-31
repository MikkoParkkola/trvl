package providers

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// withLauncherWindows substitutes the two observation windows startAndReap consults
// and restores them when the test ends.
//
// It exists to keep the host's scheduling out of the verdict. Both windows are
// production tradeoffs sized in the low hundreds of milliseconds, and a test that
// races them is measuring how promptly this machine forked a shell rather than what
// the code did with the result. Two tests in this file were written that way and went
// red under load (#533).
//
// Both are set together, because the interesting regressions are about WHICH window a
// path consults. A test can make them far enough apart that the answer is legible
// from the elapsed time without a tight bound.
func withLauncherWindows(t *testing.T, startup, failure time.Duration) {
	t.Helper()
	priorStartup, priorFailure := launcherStartupWindow, launcherFailureWindow
	t.Cleanup(func() {
		launcherStartupWindow, launcherFailureWindow = priorStartup, priorFailure
	})
	launcherStartupWindow, launcherFailureWindow = startup, failure
}

// TestLauncherWindows_ShippedDefaults pins the two window values the product actually
// ships.
//
// Every other launcher test in this file calls withLauncherWindows, which is right for
// them — they assert which window a path consults, and racing the real values under
// load is what made two of them flake (#533). The cost is that nothing was left
// checking the values themselves: both could drift to five seconds and the whole file
// would stay green while a cookie export blocked a search for ten. This test is the one
// place that reads them unsubstituted.
//
// The numbers are tradeoffs, not invariants, so a deliberate change is meant to edit
// this test. What it stops is an accidental change, and a bound would not: 150ms and
// 500ms differ by more than 3x on purpose, and any bound loose enough to hold both is
// loose enough to admit the drift this guards against.
func TestLauncherWindows_ShippedDefaults(t *testing.T) {
	if got, want := launcherStartupWindow, 150*time.Millisecond; got != want {
		t.Errorf("launcherStartupWindow = %v, want %v; a launcher with no fallback spends this window on every success, so raising it slows every cookie export", got, want)
	}
	if got, want := launcherFailureWindow, 500*time.Millisecond; got != want {
		t.Errorf("launcherFailureWindow = %v, want %v; the preferred-browser attempt spends this only when it fails, which is why it can afford to be the longer of the two", got, want)
	}
}

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

	// The regression to catch is a build that routes a plain launcher through the
	// failure window, which a launcher that keeps running spends in full. That was an
	// earlier attempt at the launcher-failure fix, and on Linux a persistent launcher
	// is the normal case, so the cost would be paid on every challenge.
	//
	// This used to be caught by a 300ms bound against the 150ms production window: a
	// 2x margin, decided by scheduling under load, and it flaked (#533). Separating
	// the windows by 500x instead makes the elapsed time say which one was consulted,
	// and no amount of load can make 20ms look like ten seconds.
	withLauncherWindows(t, 20*time.Millisecond, 10*time.Second)

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
	if elapsed > 2*time.Second {
		t.Fatalf("defaultOpenURL waited %v for a launcher that keeps running; that is the normal case for xdg-open and must not be paid on every challenge", elapsed)
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

// TestStartAndReapWithin_ReportsAProcessThatStartsThenFails pins the case the
// earlier version got wrong. A launcher that exists, starts, and then exits
// non-zero was reported as success, because only Start's error was consulted.
//
// The consequence was not cosmetic. On macOS `open -a <browser>` starts fine
// whatever the browser argument is, since `open` itself exists, and exits non-zero
// when the browser does not resolve. Reporting that as success meant defaultOpenURL
// skipped its plain `open` fallback, so the user got no window, never saw the
// challenge they were being asked to solve, and the search failed for a reason
// nothing explained.
func TestStartAndReapWithin_ReportsAProcessThatStartsThenFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh is not available")
	}

	err := startAndReapWithin(exec.Command("sh", "-c", "exit 3"), 2*time.Second)
	if err == nil {
		t.Fatal("a launcher that exited non-zero was reported as success; a caller cannot fall back on that")
	}
}

// TestStartAndReapWithin_AcceptsACleanQuickExit guards the normal path. `open` and
// `xdg-open` hand the URL off and exit immediately with zero, so a fast clean exit
// must read as success rather than as anything to fall back from.
func TestStartAndReapWithin_AcceptsACleanQuickExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh is not available")
	}

	if err := startAndReapWithin(exec.Command("sh", "-c", "exit 0"), 2*time.Second); err != nil {
		t.Fatalf("a launcher that exited cleanly was reported as a failure: %v", err)
	}
}

// TestStartAndReapWithin_DoesNotWaitForAProcessThatKeepsRunning is the property the
// whole helper exists to preserve. A browser started directly stays alive for as long
// as the user keeps the window open, and a search must not block on that.
func TestStartAndReapWithin_DoesNotWaitForAProcessThatKeepsRunning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh is not available")
	}

	start := time.Now()
	err := startAndReapWithin(exec.Command("sh", "-c", "sleep 30"), 200*time.Millisecond)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("a still-running launcher was reported as a failure: %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("waited %v for a process that keeps running; the window is meant to bound this", elapsed)
	}
}

// TestDefaultOpenURL_ReportsAPlainLauncherThatStartsThenFails covers the case that
// survived two attempts at this fix.
//
// The first attempt watched no launcher's exit status. The third watched only the
// preferred-browser attempt, reasoning that the others had no fallback behind them
// and so lost nothing by being unwatched. That reasoning ignored the caller:
// mcp/tools_providers.go reports "Opened X in browser to warm cookies. Future
// searches will use these cookies automatically" whenever this returns nil. A
// swallowed failure is therefore not a silent inefficiency, it is a confident false
// statement to the user, followed by a wait for cookies that cannot arrive.
//
// The realistic shape of this is a headless Linux box where `xdg-open` is installed
// but has no usable handler: present, starts, exits non-zero at once.
func TestDefaultOpenURL_ReportsAPlainLauncherThatStartsThenFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fixtures are POSIX")
	}

	// A window the fixture cannot lose to, rather than the production 150ms, and the
	// reason is worth recording because it cost a confusing failure. The FIRST
	// execution of a script written moments ago can take longer than 150ms on macOS,
	// so its exit is not observed inside the window and the launcher is classified as
	// still-running, which counts as success. That is the safe direction in
	// production, where a launcher is already in the page cache, but in a test it lets
	// the fixture's own cold start decide the verdict.
	//
	// The previous answer here was one throwaway execution to warm the cache first,
	// which narrowed that race without closing it and left the test with a wall-clock
	// dependency it has no business having (#533). Five seconds closes it: a cold exec
	// of a two-line shell script does not lose to that, and the property under test is
	// whether a non-zero exit reaches the caller, not how quickly it arrives.
	withLauncherWindows(t, 5*time.Second, 5*time.Second)

	launcher := "open"
	if runtime.GOOS == "linux" {
		launcher = "xdg-open"
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, launcher), []byte("#!/bin/sh\nexit 4\n"), 0o755); err != nil {
		t.Fatalf("write failing %s: %v", launcher, err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	// No browser preference, so this exercises the plain launcher rather than the
	// `open -a` attempt that already had its own fallback.
	if err := defaultOpenURL(runtime.GOOS, "", "https://example.invalid/challenge"); err == nil {
		t.Fatal("a launcher that started and exited non-zero was reported as success; the caller would tell the user a browser opened when none did")
	}
}
