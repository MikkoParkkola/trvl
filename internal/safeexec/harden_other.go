//go:build !unix && !windows

package safeexec

import "os/exec"

// harden is a no-op on platforms without process sessions.
// The context deadline and WaitDelay set in Command still bound the
// call, so a hung helper cannot stall a search; only the terminal-detachment
// guarantee is unavailable here.
func harden(_ *exec.Cmd) {}

// contain is a no-op where there is no process-group primitive to use.
func contain(_ *exec.Cmd) {}
