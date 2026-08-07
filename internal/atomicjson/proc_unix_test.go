//go:build !windows

package atomicjson

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDifferentBootMakesReusedLivePIDReclaimable(t *testing.T) {
	dir := t.TempDir()
	name := tempNameWithBoot("store.json", os.Getpid(), "previousboot", "aabbccdd")
	path := writeTemp(t, dir, name, time.Hour)

	orphans, err := FindOrphans(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(orphans) != 1 {
		t.Fatalf("orphans = %d, want 1", len(orphans))
	}
	current := currentBootFingerprint()
	if current == "" {
		t.Skip("platform does not expose a boot fingerprint")
	}
	if current == "previousboot" {
		t.Fatal("test boot marker unexpectedly matches current boot")
	}
	if orphans[0].OwnerLive {
		t.Fatal("reused PID from a previous boot reported live")
	}
	if !orphans[0].Reclaimable(time.Now(), time.Minute) {
		t.Fatal("previous-boot temp is not reclaimable")
	}
	if _, err := os.Stat(filepath.Clean(path)); err != nil {
		t.Fatalf("read-only discovery removed the temp: %v", err)
	}
}

func TestSameBootStillProtectsLiveOwner(t *testing.T) {
	current := currentBootFingerprint()
	if current == "" {
		t.Skip("platform does not expose a boot fingerprint")
	}
	dir := t.TempDir()
	name := tempNameWithBoot("store.json", os.Getpid(), current, "aabbccdd")
	path := writeTemp(t, dir, name, 48*time.Hour)

	orphans, err := FindOrphans(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(orphans) != 1 || !orphans[0].OwnerLive {
		t.Fatalf("same-boot live owner was not protected: %+v", orphans)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("live owner's temp was removed: %v", err)
	}
}
