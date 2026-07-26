//go:build unix

package safeexec

import (
	"os/exec"
	"syscall"
	"time"
)

// killGrace is how long a signalled helper group has to exit on its own
// before it is killed outright. Short, because nothing is waiting on a graceful
// shutdown; non-zero, because a helper may have started a daemon of its own
// that has state to flush.
const killGrace = 250 * time.Millisecond

// harden detaches a helper process from the terminal and
// from trvl's process group.
//
// Setsid puts the child in a new session with no controlling terminal, so an
// attempt to open /dev/tty fails instead of writing a prompt into whatever
// terminal happens to host the process.
//
// This is the measured mechanism behind #507, not a guess. Probing `op read`
// against an empty account store, with stdin/stdout/stderr piped in both runs
// so only the controlling terminal differs:
//
//	terminal inherited : 836 bytes written to /dev/tty, ending in
//	                     "Do you want to add an account manually now? [Y/n]",
//	                     then blocks indefinitely. cmd.Output() sees none of it.
//	Setsid (no ctty)   : 0 bytes to the terminal, exits non-zero in 310ms.
//
// The 1Password CLI version is not the variable: 2.34.1 and 2.35.0 behave
// identically. A helper reaching the terminal directly is why redirecting the
// child's stdio was never enough, and why denying the terminal is the fix.
//
// It does not interfere with the path that is supposed to work: 1Password
// desktop-app integration and biometric unlock communicate over a local socket,
// not a tty.
//
// Setsid implies a new process group, so Setpgid must NOT also be set — the two
// together are rejected. Because the child leads its own group, Cancel can
// signal the whole group and take down helpers it spawned, instead of killing
// only the direct child and leaving descendants to be reparented.
func harden(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// Negative PID addresses the process group. A pre-existing, user-owned
		// daemon is not in this group and is deliberately left alone: trvl must
		// not disrupt a 1Password agent it did not start.
		pgid := -cmd.Process.Pid

		// Ask first, insist second. `op read` itself has nothing to clean up,
		// but it may have started a 1Password daemon that does, and that daemon
		// is inside the group we are signalling. SIGKILL with no warning could
		// leave its socket or lock behind for the user's next, unrelated `op`
		// invocation to trip over.
		if err := syscall.Kill(pgid, syscall.SIGTERM); err != nil {
			return cmd.Process.Kill()
		}
		time.AfterFunc(killGrace, func() {
			// Ignoring the error is deliberate: by now the group is usually
			// gone, and ESRCH is the success case.
			_ = syscall.Kill(pgid, syscall.SIGKILL)
		})
		return nil
	}
}
