package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// deadPID returns a PID that is known to have exited.
//
// It runs a real process and waits for it, rather than picking a large number
// and hoping: an arbitrary number can belong to a live process, which would make
// the test assert the opposite of what it means to. PID reuse could in principle
// hand this number to a new process before the assertion runs, and that would
// make the orphan read as live and be retained -- a false FAILURE, never a false
// pass, which is the direction a test is allowed to be wrong in.
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("/usr/bin/true")
	if err := cmd.Run(); err != nil {
		cmd = exec.Command("/bin/true")
		if err := cmd.Run(); err != nil {
			t.Skipf("no usable /bin/true to source a dead pid: %v", err)
		}
	}
	return cmd.Process.Pid
}

// writeOrphan drops a temp file shaped like one an interrupted atomic write
// leaves behind, owned by pid, aged by age.
func writeOrphan(t *testing.T, dir, target string, pid int, size int, age time.Duration) string {
	t.Helper()
	path := filepath.Join(dir, fmt.Sprintf("%s.tmp-%d-abc123", target, pid))
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), size), 0o600); err != nil {
		t.Fatalf("write orphan: %v", err)
	}
	old := time.Now().Add(-age)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("age orphan: %v", err)
	}
	return path
}

// runTempFiles executes the command the way a user does and returns what they see.
func runTempFiles(t *testing.T, args ...string) string {
	t.Helper()
	cmd := tempFilesCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("tempfiles %v: %v (output: %s)", args, err, out.String())
	}
	return out.String()
}

// TestTempFilesReportsAndDeletesNothingByDefault pins TRVL.TMP.1, TMP.2 and the
// dry-run half of TMP.3.
//
// The assertion that matters is the one on the filesystem: an orphan may be the
// only surviving copy of the file it was replacing, so a default run must leave
// every byte where it is. Asserting only on the printed text would pass against
// a command that reported honestly and deleted anyway.
//
// Sabotage-verified: defaulting the --delete flag to true makes this fail on the
// os.Stat, and defaulting it to true while also skipping the report makes it fail
// on the size line first.
func TestTempFilesReportsAndDeletesNothingByDefault(t *testing.T) {
	dir := t.TempDir()
	dead := writeOrphan(t, dir, "price-history.json", deadPID(t), 4096, 72*time.Hour)
	live := writeOrphan(t, dir, "probe-cache.json", os.Getpid(), 113, 72*time.Hour)

	out := runTempFiles(t, "--dir", dir, "--min-age", "0")

	if !strings.Contains(out, "Orphaned temp files in "+dir+": 2") {
		t.Fatalf("report does not state the count:\n%s", out)
	}
	if !strings.Contains(out, "4.0 KB") || !strings.Contains(out, "113 B") {
		t.Fatalf("report does not state per-file sizes:\n%s", out)
	}
	if !strings.Contains(out, "age 3d") {
		t.Fatalf("report does not state age:\n%s", out)
	}
	if !strings.Contains(out, "Nothing was deleted") {
		t.Fatalf("report does not say it deleted nothing:\n%s", out)
	}
	for _, p := range []string{dead, live} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s was removed by a default run: %v", filepath.Base(p), err)
		}
	}
}

// TestTempFilesDeleteRemovesOnlyProvablyDeadOwners pins TMP.3's confirmed half
// and TMP.5.
//
// Sabotage-verified: making Orphan.Reclaimable ignore OwnerLive makes this fail
// on the live-owner Stat, which is the assertion that protects a running writer.
func TestTempFilesDeleteRemovesOnlyProvablyDeadOwners(t *testing.T) {
	dir := t.TempDir()
	dead := writeOrphan(t, dir, "price-history.json", deadPID(t), 2048, 72*time.Hour)
	live := writeOrphan(t, dir, "probe-cache.json", os.Getpid(), 113, 72*time.Hour)

	out := runTempFiles(t, "--dir", dir, "--min-age", "0", "--delete")

	if _, err := os.Stat(dead); !os.IsNotExist(err) {
		t.Fatalf("orphan with a dead owner survived --delete: %v", err)
	}
	if _, err := os.Stat(live); err != nil {
		t.Fatalf("orphan owned by this live process was deleted: %v", err)
	}
	if !strings.Contains(out, "still running") {
		t.Fatalf("report does not say why the live-owner orphan was kept:\n%s", out)
	}
	if !strings.Contains(out, "Deleted 1 of 2.") {
		t.Fatalf("report does not state what was deleted:\n%s", out)
	}
}

// TestTempFilesKeepsYoungOrphansUnderTheGracePeriod pins the grace period, which
// is the part that protects a writer that died seconds ago from having its only
// copy reclaimed by an operator clearing disk space.
//
// Sabotage-verified: dropping the MinAge comparison from Orphan.Reclaimable makes
// this fail on the Stat.
func TestTempFilesKeepsYoungOrphansUnderTheGracePeriod(t *testing.T) {
	dir := t.TempDir()
	fresh := writeOrphan(t, dir, "date-grids.json", deadPID(t), 5700, time.Minute)

	out := runTempFiles(t, "--dir", dir, "--delete")

	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("orphan younger than the grace period was deleted: %v", err)
	}
	if !strings.Contains(out, "younger than") {
		t.Fatalf("report does not explain the grace period:\n%s", out)
	}
}

// TestTempFilesSaysSoWhenThereAreNone keeps the empty case from printing a
// header with nothing under it, which reads as a truncated report.
func TestTempFilesSaysSoWhenThereAreNone(t *testing.T) {
	dir := t.TempDir()
	if out := runTempFiles(t, "--dir", dir); !strings.Contains(out, "No orphaned temp files") {
		t.Fatalf("empty directory produced %q", out)
	}
}
