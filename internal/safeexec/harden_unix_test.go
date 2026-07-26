//go:build unix

package safeexec

import (
	"context"
	"os"
	"path/filepath"
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

// TestCommand_KillsDescendantsOnTimeout proves the guarantee the reported
// symptom actually needs. #507 found stalled helpers with children reparented to
// a daemon, which is what happens when only the direct child is signalled.
// Because Setsid makes the helper a process-group leader, cancelling signals the
// whole group, so anything it spawned dies with it.
//
// The fake backgrounds a descendant that would write a marker after 3s, then
// hangs for 30s. The command is bounded at 1s, so a correctly group-killed
// descendant never reaches its write.
func TestCommand_KillsDescendantsOnTimeout(t *testing.T) {
	dir := t.TempDir()
	survivor := filepath.Join(dir, "descendant.survived")
	script := "#!/bin/sh\n( sleep 3; echo survived >> \"" + survivor + "\" ) &\nsleep 30\n"
	bin := filepath.Join(dir, "hangs")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake helper: %v", err)
	}

	cmd, _, cancel := Command(context.Background(), time.Second, bin)
	defer cancel()

	start := time.Now()
	if err := cmd.Run(); err == nil {
		t.Fatal("expected the hung helper to fail")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Run took %v; the deadline should have ended it near 1s", elapsed)
	}

	// Outlive the descendant's own sleep: if it were still alive, this is when
	// it would write.
	time.Sleep(4 * time.Second)

	if _, err := os.Stat(survivor); err == nil {
		t.Fatal("a descendant outlived the timeout; the process group is not being signalled, so helpers will accumulate exactly as reported in #507")
	}
}
