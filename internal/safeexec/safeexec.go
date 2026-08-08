// Package safeexec runs external helper programs that may block, prompt, or
// spawn children of their own.
//
// It exists because of issue #507: trvl shelled out to the 1Password CLI on
// every flight search with no deadline and no isolation. The helper opened
// /dev/tty directly and asked the user a question, which appeared in the
// terminal hosting an MCP session even though the child's stdin, stdout and
// stderr were all redirected. It then waited forever for an answer nobody was
// there to give, and each subsequent search started another one.
//
// Three properties defeat that whole class, and any helper trvl invokes on a
// path the user did not explicitly request should have all three:
//
//   - bounded, so a wedged helper cannot stall a search;
//   - detached from the controlling terminal, so it cannot prompt;
//   - killed by process group, so what it spawned dies with it.
//
// Measured on `op read` with an empty account store, stdio piped in both runs
// so only the terminal differs: with a controlling terminal it wrote 836 bytes
// to /dev/tty ending in "Do you want to add an account manually now? [Y/n]" and
// then blocked indefinitely; detached, it wrote nothing to any terminal and
// exited non-zero in 310ms.
package safeexec

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"time"
)

// waitDelay caps how long Wait blocks after cancellation, so a helper that
// ignores its death signal cannot pin the calling goroutine.
const waitDelay = time.Second

// Command builds a bounded, terminal-detached command.
//
// The returned context reports why the command failed, letting a caller tell
// "this helper timed out" apart from "the caller went away" — two conditions
// that need different responses. The returned CancelFunc must be called.
//
// Run the result with Output rather than cmd.Output: some containment can only
// be installed once the process exists.
//
// Callers should treat a non-nil error from the command as untrusted for
// logging: a credential helper's stderr routinely echoes secret references.
func Command(ctx context.Context, timeout time.Duration, name string, args ...string) (*exec.Cmd, context.Context, context.CancelFunc) {
	tctx, cancel := context.WithTimeout(ctx, timeout)

	// #nosec G204 -- this package deliberately accepts an argv vector, never a
	// shell command; callers select bounded helper executables and arguments are
	// passed without shell interpretation.
	cmd := exec.CommandContext(tctx, name, args...)
	cmd.Stdin = nil // explicit: the child reads from os.DevNull, never ours
	cmd.WaitDelay = waitDelay

	harden(cmd)

	return cmd, tctx, cancel
}

// Output runs cmd and returns its standard output.
//
// It exists because containment is not uniform across platforms. On Unix,
// Setsid is applied at fork, so the process is contained before it can do
// anything and this adds nothing. On Windows there is no fork-time hook: a job
// object can only be assigned to a process that already exists, so it has to
// happen between Start and Wait. That leaves a narrow window in which a child
// spawned in that interval escapes the job.
//
// That window is left open deliberately. Closing it means starting the process
// suspended, finding its initial thread through a toolhelp snapshot and resuming
// it by hand, because Go does not expose the thread handle. If any step of that
// dance fails the helper never runs at all, which turns a narrow resource risk
// into a certain loss of function on a platform this code cannot be exercised on
// outside CI. The helpers in question — a credential read, a cookie export —
// parse arguments and read config before they spawn anything, so the interval
// they could exploit is microseconds. Reassess if a helper ever forks that
// early.
//
// The containment is closed unconditionally, on success as well as failure: on
// Windows the job holds a kernel handle, and a long-lived MCP server that leaked
// one per credential lookup would accumulate them for the life of the process.
// Closing also kills anything the helper left running.
//
// Nothing here mutates cmd after Start. exec.Cmd's own cancellation watcher
// reads cmd.Cancel from another goroutine once the process is running, so
// assigning to it post-Start is a data race.
//
// Standard error is discarded, not captured: the helpers this package runs echo
// secret references in their diagnostics, and an error string tends to end up
// in a log.
func Output(cmd *exec.Cmd) ([]byte, error) {
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = io.Discard

	c := newContainment()
	defer c.close()

	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c.hold(cmd.Process)

	err := cmd.Wait()
	return stdout.Bytes(), err
}
