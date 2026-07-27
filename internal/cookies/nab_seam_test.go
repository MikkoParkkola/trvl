package cookies

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestExtractViaNabRefusesWhenDeclined covers the seam a fourth adversarial
// review found on #521.
//
// extractViaNab builds `nab cookies export <domain> --cookies <browser>` itself
// rather than going through nab.Client.Fetch, so the gate added to that client
// does not cover it. Its own caller checks Disabled() first, which is why this
// was not a live bypass -- but a gate that lives on the caller is exactly what
// the three previous rounds each found missing on a caller nobody had checked.
//
// The assertion is at the command seam and not on the return value: a fake nab
// on PATH writes a marker file when it runs, so the test fails if the process
// starts, rather than passing because extractViaNab returned "" for one of the
// several other reasons it can.
func TestExtractViaNabRefusesWhenDeclined(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake nab is a shell script")
	}

	dir := t.TempDir()
	marker := filepath.Join(dir, "nab-ran")
	// `: > file` and not `touch`: the fake nab inherits the PATH set below, which
	// holds only this directory, so it cannot call any external program.
	script := "#!/bin/sh\n: > " + marker + "\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "nab"), []byte(script), 0o755); err != nil {
		t.Fatalf("writing the fake nab: %v", err)
	}
	t.Setenv("PATH", dir)

	ran := func() bool {
		_, err := os.Stat(marker)
		return err == nil
	}

	t.Setenv(DisableEnv, "1")
	if got := extractViaNab(context.Background(), "auto", "thetrainline.com"); got != "" {
		t.Errorf("extractViaNab returned %q despite an explicit decline", got)
	}
	if ran() {
		t.Fatal("nab was started despite an explicit decline")
	}

	// The other half: the gate must refuse a decline and only a decline, or the
	// check above would pass on a function that never runs nab at all.
	t.Setenv(DisableEnv, "")
	resetCookieCache()
	_ = extractViaNab(context.Background(), "auto", "thetrainline.com")
	if !ran() {
		t.Fatal("nab was not started without a decline; the gate refuses more than it should")
	}
}
