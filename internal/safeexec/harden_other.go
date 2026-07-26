//go:build !unix && !windows

package safeexec

import (
	"os"
	"os/exec"
)

// harden is a no-op on platforms without process sessions.
// The context deadline and WaitDelay set in Command still bound the
// call, so a hung helper cannot stall a search; only the terminal-detachment
// guarantee is unavailable here.
func harden(_ *exec.Cmd) {}

// containment is a no-op where there is no process-group primitive to use.
type containment struct{}

func newContainment() *containment        { return &containment{} }
func (c *containment) hold(_ *os.Process) {}
func (c *containment) close()             {}
