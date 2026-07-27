//go:build unix

package watch

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestDesktopNotifyDispatch_IsBounded is the regression test for the sibling of
// #507 found in this package.
//
// Notifications are sent from inside the price-watch daemon. An unbounded
// helper there does not slow a search the user is waiting on — it silently stops
// the watch loop from checking anything again. On macOS `osascript` can block on
// a notification-permission dialog or a locked display, and nothing in a daemon
// is there to answer it.
//
// The fake helper hangs for 30s. A build that does not bound it fails here.
func TestDesktopNotifyDispatch_IsBounded(t *testing.T) {
	helper := "osascript"
	if runtime.GOOS != "darwin" {
		helper = "notify-send"
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, helper), []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatalf("write fake %s: %v", helper, err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	start := time.Now()
	desktopNotifyDispatch(runtime.GOOS, "title", "message")
	elapsed := time.Since(start)

	if elapsed > notifyTimeout+3*time.Second {
		t.Fatalf("a notification took %v; a wedged helper must not pause the watch loop (bound is %v)", elapsed, notifyTimeout)
	}
}

// TestDesktopNotifyDispatch_LeavesNoDescendants confirms the containment applies
// here too: a helper that starts something of its own must not leave it running
// after the deadline.
func TestDesktopNotifyDispatch_LeavesNoDescendants(t *testing.T) {
	helper := "osascript"
	if runtime.GOOS != "darwin" {
		helper = "notify-send"
	}

	dir := t.TempDir()
	survivor := filepath.Join(dir, "descendant.survived")
	// The descendant must outlive the deadline, or it would finish legitimately
	// and the test would prove nothing about containment.
	script := "#!/bin/sh\n( sleep 7; echo survived >> \"" + survivor + "\" ) &\nsleep 30\n"
	if err := os.WriteFile(filepath.Join(dir, helper), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake %s: %v", helper, err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	desktopNotifyDispatch(runtime.GOOS, "title", "message")

	// Dispatch returns at the deadline (5s); wait past the descendant's own
	// 7s sleep, which is when it would write if it were still alive.
	time.Sleep(4 * time.Second)

	if _, err := os.Stat(survivor); err == nil {
		t.Fatal("a descendant of the notification helper outlived the deadline; the watch daemon would accumulate them")
	}
}
