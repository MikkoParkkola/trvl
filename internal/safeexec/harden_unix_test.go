//go:build unix

package safeexec

import (
	"context"
	"testing"
	"time"
)

func TestHarden_SetsSession(t *testing.T) {
	cmd, _, cancel := Command(context.Background(), 2*time.Second, "true")
	defer cancel()

	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
		t.Fatal("helpers must run with Setsid so they have no controlling terminal and cannot open /dev/tty to prompt (#507)")
	}
	if cmd.SysProcAttr.Setpgid {
		t.Fatal("Setpgid must not be combined with Setsid; Setsid already creates a new process group and the pair is rejected")
	}
	if cmd.WaitDelay == 0 {
		t.Fatal("helpers must set WaitDelay so one ignoring its kill signal cannot pin the caller")
	}
	if cmd.Cancel == nil {
		t.Fatal("helpers must set Cancel so the process group is signalled, not just the direct child")
	}
}
