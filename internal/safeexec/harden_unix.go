//go:build unix

package safeexec

import (
	"os/exec"
	"syscall"
)

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
		// not disrupt an agent it did not start.
		//
		// This kills outright rather than escalating SIGTERM to SIGKILL. An
		// earlier version did escalate, on the theory that a daemon inside the
		// group might have state to flush, but the grace period needed a timer
		// that outlived the process — and a PGID is reusable, so a timer firing
		// after the group had already exited could signal an unrelated one.
		// Everything in this group is something we started and can restart, so
		// the risk was bought for nothing.
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
}

// contain is a no-op on Unix: Setsid is applied at fork, so the process is
// already a group leader before it can spawn anything. Nothing is left to do
// once it is running.
func contain(_ *exec.Cmd) {}
