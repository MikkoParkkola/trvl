//go:build !unix

package afklm

import "os/exec"

// hardenCredentialCommand is a no-op on platforms without process sessions.
// The context deadline and WaitDelay set in credentialCommand still bound the
// call, so a hung helper cannot stall a search; only the terminal-detachment
// guarantee is unavailable here.
func hardenCredentialCommand(_ *exec.Cmd) {}
