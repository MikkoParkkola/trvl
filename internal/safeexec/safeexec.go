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

	cmd := exec.CommandContext(tctx, name, args...)
	cmd.Stdin = nil // explicit: the child reads from os.DevNull, never ours
	cmd.WaitDelay = waitDelay

	harden(cmd)

	return cmd, tctx, cancel
}

// Output runs cmd and returns its standard output.
//
// It exists because containment is not uniform across platforms. On Unix,
// Setsid is applied at fork and the process is contained before it can do
// anything. On Windows there is no equivalent fork-time hook: a job object can
// only be assigned to a process that already exists, so it has to happen
// between Start and Wait. That leaves a narrow window in which a child spawned
// immediately at startup escapes the job. Go does not expose the thread handle
// needed for the CREATE_SUSPENDED-assign-resume sequence that would close it,
// so the window is accepted and documented rather than hidden.
//
// Standard error is discarded, not captured: the helpers this package runs echo
// secret references in their diagnostics, and an error string tends to end up
// in a log.
func Output(cmd *exec.Cmd) ([]byte, error) {
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		return nil, err
	}
	contain(cmd)
	err := cmd.Wait()
	return stdout.Bytes(), err
}
