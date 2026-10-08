package batchexec

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// MIK-8095: a client built by NewClient during go test must not share the
// machine-wide cooldown file. Packages run in parallel on one runner with one
// home folder, so one test's refusal turned unrelated tests in other packages
// into "google refused further requests" failures, and tests polluted the
// developer's real ~/.trvl/cache.
func TestNewClientIgnoresRealCooldownFileUnderTest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".trvl", "cache")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour).Format(time.RFC3339Nano)
	if err := os.WriteFile(filepath.Join(dir, googleRefusalFilename), []byte(future), 0o600); err != nil {
		t.Fatal(err)
	}

	c := NewClient()
	if c.refusalDir != "" {
		t.Fatalf("NewClient under go test must not use a shared cooldown directory, got %q", c.refusalDir)
	}
	if _, ok := c.readRefusalDeadline(); ok {
		t.Fatal("a cooldown file in the home folder must not reach a client built during tests")
	}
}
