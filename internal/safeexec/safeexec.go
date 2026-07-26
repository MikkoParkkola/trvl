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
	"context"
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
