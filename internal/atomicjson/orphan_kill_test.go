package atomicjson

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
)

const (
	helperDirEnv   = "ATOMICJSON_TEST_HELPER_DIR"
	helperReadyEnv = "ATOMICJSON_TEST_HELPER_READY"
)

// TestHelperKilledMidWrite is not a test: it is the child process re-executed
// by TestInterruptedWriteIsReportedNotLost. It starts a real atomic write,
// parks at the instant before the publishing rename, and waits to be killed.
func TestHelperKilledMidWrite(t *testing.T) {
	dir := os.Getenv(helperDirEnv)
	if dir == "" {
		t.Skip("helper process only")
	}
	ready := os.Getenv(helperReadyEnv)
	testHookBeforeRename = func(string) {
		if err := os.WriteFile(ready, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			panic(err)
		}
		select {} // block until SIGKILL
	}
	_ = WriteBytes(filepath.Join(dir, "store.json"), []byte(`{"payload":"abcdefgh"}`))
}

// TestInterruptedWriteIsReportedNotLost is TRVL.TMP.6: a writer is killed
// mid-write, the temp file must survive the kill, and FindOrphans must report
// it with size, age and a dead owner. Before the fix the temp file carried no
// PID and no scanner existed, so the orphan was invisible forever.
func TestInterruptedWriteIsReportedNotLost(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(t.TempDir(), "ready")

	cmd := exec.Command(os.Args[0], "-test.run=TestHelperKilledMidWrite", "-test.timeout=60s")
	cmd.Env = append(os.Environ(), helperDirEnv+"="+dir, helperReadyEnv+"="+ready)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}

	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			t.Fatal("helper never reached the pre-rename point")
		}
		time.Sleep(2 * time.Millisecond)
	}

	pid := cmd.Process.Pid
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill helper: %v", err)
	}
	_ = cmd.Wait()

	if _, err := os.Stat(filepath.Join(dir, "store.json")); err == nil {
		t.Fatal("target must not exist: the helper was killed before the rename")
	}

	orphans, err := FindOrphans(dir)
	if err != nil {
		t.Fatalf("FindOrphans: %v", err)
	}
	if len(orphans) != 1 {
		t.Fatalf("want exactly 1 orphan reported, got %d", len(orphans))
	}
	o := orphans[0]
	if _, err := os.Stat(o.Path); err != nil {
		t.Fatalf("orphan must survive the kill: %v", err)
	}
	if o.Target != filepath.Join(dir, "store.json") {
		t.Errorf("Target = %q, want the intended store path", o.Target)
	}
	if o.Size == 0 {
		t.Error("Size = 0, want the bytes that were written before the kill")
	}
	if o.Age(time.Now()) < 0 {
		t.Errorf("Age = %v, want a non-negative duration", o.Age(time.Now()))
	}
	if o.PID != pid {
		t.Errorf("PID = %d, want the killed writer's pid %d", o.PID, pid)
	}
	if runtime.GOOS != "windows" && o.OwnerLive {
		t.Error("OwnerLive = true for a reaped process, want false")
	}
}
