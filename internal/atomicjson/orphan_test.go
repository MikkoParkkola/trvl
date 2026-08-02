package atomicjson

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// deadPID returns a pid that is very unlikely to be running: a short-lived
// child that has already been reaped.
func deadPID(t *testing.T) int {
	t.Helper()
	// PID 0 and negatives are never valid owners, so use a real exited child.
	p, err := os.StartProcess("/bin/sh", []string{"sh", "-c", "exit 0"}, &os.ProcAttr{})
	if err != nil {
		t.Skipf("cannot spawn a helper process: %v", err)
	}
	if _, err := p.Wait(); err != nil {
		t.Skipf("cannot reap helper process: %v", err)
	}
	return p.Pid
}

func writeTemp(t *testing.T, dir, name string, age time.Duration) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	if age > 0 {
		old := time.Now().Add(-age)
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

// TRVL.TMP.5: a temp whose owner is still running is never removed, even when
// the caller confirms deletion.
func TestCleanRetainsLiveOwner(t *testing.T) {
	dir := t.TempDir()
	p := writeTemp(t, dir, tempName("live.json", os.Getpid(), "aabbccdd"), time.Hour)

	res, err := Clean(dir, CleanOptions{Confirm: true})
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if len(res.Removed) != 0 {
		t.Fatalf("removed %d files owned by a live process, want 0", len(res.Removed))
	}
	if len(res.Retained) != 1 {
		t.Fatalf("Retained = %d, want the live-owner temp reported", len(res.Retained))
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("live owner's temp was deleted: %v", err)
	}
}

// A name written before PID stamping has no provable owner, so it is reported
// but never deleted — it may be the only surviving copy of its target.
func TestCleanRetainsUnknownOwner(t *testing.T) {
	dir := t.TempDir()
	p := writeTemp(t, dir, "legacy.json.tmp-0123456789abcdef", 48*time.Hour)

	orphans, err := FindOrphans(dir)
	if err != nil {
		t.Fatalf("FindOrphans: %v", err)
	}
	if len(orphans) != 1 || orphans[0].PID != 0 {
		t.Fatalf("legacy temp must be reported with PID 0, got %+v", orphans)
	}
	if orphans[0].Target != filepath.Join(dir, "legacy.json") {
		t.Errorf("Target = %q, want legacy.json", orphans[0].Target)
	}

	res, err := Clean(dir, CleanOptions{Confirm: true})
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if len(res.Removed) != 0 {
		t.Fatalf("removed %d unowned files, want 0", len(res.Removed))
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("unowned temp was deleted: %v", err)
	}
}

// TRVL.TMP.2 / TRVL.TMP.3: the default is a dry run. Eligible files are
// reported and left on disk until the caller explicitly confirms.
func TestCleanDryRunDeletesNothing(t *testing.T) {
	dir := t.TempDir()
	pid := deadPID(t)
	p := writeTemp(t, dir, tempName("gone.json", pid, "deadbeefdeadbeef"), time.Hour)

	res, err := Clean(dir, CleanOptions{})
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if len(res.Eligible) != 1 {
		t.Fatalf("Eligible = %d, want 1", len(res.Eligible))
	}
	if len(res.Removed) != 0 {
		t.Fatalf("dry run removed %d files, want 0", len(res.Removed))
	}
	if len(res.Retained) != 1 {
		t.Fatalf("Retained = %d, want the eligible file still on disk", len(res.Retained))
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("dry run deleted the file: %v", err)
	}
}

// Confirmed cleanup removes only the temp whose owner is provably gone.
func TestCleanConfirmRemovesOnlyDeadOwner(t *testing.T) {
	dir := t.TempDir()
	pid := deadPID(t)
	dead := writeTemp(t, dir, tempName("gone.json", pid, "deadbeefdeadbeef"), time.Hour)
	live := writeTemp(t, dir, tempName("live.json", os.Getpid(), "aabbccddaabbccdd"), time.Hour)

	res, err := Clean(dir, CleanOptions{Confirm: true})
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if len(res.Removed) != 1 || res.Removed[0].Path != dead {
		t.Fatalf("Removed = %+v, want only %q", res.Removed, dead)
	}
	if _, err := os.Stat(dead); !os.IsNotExist(err) {
		t.Fatalf("dead owner's temp still present: %v", err)
	}
	if _, err := os.Stat(live); err != nil {
		t.Fatalf("live owner's temp was removed: %v", err)
	}
}

// A grace period keeps a just-orphaned temp out of reach, so a cleanup racing
// a writer that died seconds ago still leaves the data alone.
func TestCleanRespectsMinAge(t *testing.T) {
	dir := t.TempDir()
	pid := deadPID(t)
	p := writeTemp(t, dir, tempName("fresh.json", pid, "deadbeefdeadbeef"), 0)

	res, err := Clean(dir, CleanOptions{Confirm: true, MinAge: time.Hour})
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if len(res.Eligible) != 0 || len(res.Removed) != 0 {
		t.Fatalf("removed a file inside the grace period: %+v", res)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("fresh temp was deleted: %v", err)
	}
}

// Clean must not touch anything that is not one of our temp files.
func TestCleanIgnoresNonTempFiles(t *testing.T) {
	dir := t.TempDir()
	keep := writeTemp(t, dir, "store.json", 48*time.Hour)
	if err := os.Mkdir(filepath.Join(dir, "sub.json.tmp-1-aa"), 0o700); err != nil {
		t.Fatal(err)
	}

	orphans, err := FindOrphans(dir)
	if err != nil {
		t.Fatalf("FindOrphans: %v", err)
	}
	if len(orphans) != 0 {
		t.Fatalf("FindOrphans reported non-temp entries: %+v", orphans)
	}
	res, err := Clean(dir, CleanOptions{Confirm: true})
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if len(res.Removed) != 0 {
		t.Fatalf("Removed = %+v, want nothing", res.Removed)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("regular store file was deleted: %v", err)
	}
}

func TestFindOrphansMissingDir(t *testing.T) {
	orphans, err := FindOrphans(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("missing dir must not be an error: %v", err)
	}
	if len(orphans) != 0 {
		t.Fatalf("got %d orphans from a missing dir", len(orphans))
	}
}

// A successful write must leave no orphan behind (TRVL.TMP.4's happy path).
func TestFindOrphansAfterSuccessfulWrite(t *testing.T) {
	dir := t.TempDir()
	if err := Write(filepath.Join(dir, "ok.json"), sample{Name: "a"}); err != nil {
		t.Fatal(err)
	}
	orphans, err := FindOrphans(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(orphans) != 0 {
		t.Fatalf("successful write left orphans: %+v", orphans)
	}
}
