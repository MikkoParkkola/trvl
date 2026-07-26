//go:build unix

package afklm

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHardenCredentialCommand_SetsSession(t *testing.T) {
	cmd, _, cancel := credentialCommand(context.Background(), "true")
	defer cancel()

	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
		t.Fatal("credential helpers must run with Setsid so they have no controlling terminal and cannot open /dev/tty to prompt (#507)")
	}
	if cmd.SysProcAttr.Setpgid {
		t.Fatal("Setpgid must not be combined with Setsid; Setsid already creates a new process group and the pair is rejected")
	}
}

// TestOpLookup_KillsDescendantsOnTimeout proves the guarantee the reporter's
// symptom actually needs. They found stalled `op read` processes with helpers
// reparented to `op daemon`, which is what happens when only the direct child is
// signalled. Because Setsid makes the helper a process-group leader, cancelling
// signals the whole group, so anything it spawned dies with it.
//
// The fake helper backgrounds a descendant that would write a marker after 3s,
// then hangs for 30s. The lookup is bounded at externalLookupTimeout (2s), so a
// correctly group-killed descendant never reaches its write.
func TestOpLookup_KillsDescendantsOnTimeout(t *testing.T) {
	dir := t.TempDir()
	survivor := filepath.Join(dir, "descendant.survived")
	fakeBin(t, dir, "op", "( sleep 3; echo survived >> \""+survivor+"\" ) &\nsleep 30")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	resetExternalCache()

	start := time.Now()
	_, err := opLookup(context.Background(), "op://Private/AF-KLM/credential")
	if err == nil {
		t.Fatal("expected the hung helper to fail the lookup")
	}
	if elapsed := time.Since(start); elapsed > externalLookupTimeout+3*time.Second {
		t.Fatalf("lookup took %v; expected it bounded near %v", elapsed, externalLookupTimeout)
	}

	// Outlive the descendant's own sleep: if it were still alive, this is when
	// it would write.
	time.Sleep(4 * time.Second)

	if _, err := os.Stat(survivor); err == nil {
		t.Fatal("a descendant of the credential helper outlived the timeout; the process group is not being signalled, so helpers will accumulate exactly as reported in #507")
	}
}
