package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestDigestCommand_DryRunRendersDigest(t *testing.T) {
	cmd := digestCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--dry-run"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("digest --dry-run: %v", err)
	}
	if !strings.Contains(out.String(), "trvl deal-radar") {
		t.Errorf("dry-run output missing digest header: %q", out.String())
	}
}

func TestDigestCommand_Use(t *testing.T) {
	cmd := digestCmd()
	if cmd.Use != "digest" {
		t.Errorf("Use = %q, want digest", cmd.Use)
	}
}

// TestScheduler_FunctionsExist is a cross-platform smoke test: installScheduler
// and uninstallScheduler must be linked on every GOOS (each platform provides
// an implementation). We do not invoke them here because the real installers
// touch the filesystem / service manager; per-platform behavior is covered by
// the tagged test files.
func TestScheduler_ManualCronLine(t *testing.T) {
	line := manualCronLine("/opt/trvl/bin/trvl")
	// 08:00 daily, invoking `<binary> digest`.
	if !strings.Contains(line, "0 8 * * *") {
		t.Errorf("cron line missing 08:00 schedule: %q", line)
	}
	if !strings.Contains(line, "/opt/trvl/bin/trvl digest") {
		t.Errorf("cron line missing command: %q", line)
	}
}

func TestScheduler_ResolveBinaryPathNonEmpty(t *testing.T) {
	if resolveBinaryPath() == "" {
		t.Error("resolveBinaryPath returned empty string")
	}
}
