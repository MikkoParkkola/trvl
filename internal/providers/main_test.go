package providers

import (
	"os"
	"path/filepath"
	"testing"
)

// redirectHomeForTests points the home directory this package's tests resolve
// at a throwaway location, so no test can read or mutate the developer's real
// ~/.trvl. Several helpers here (cookieCacheDir, health-log and registry paths)
// resolve their storage from os.UserHomeDir with no other injection point, so
// the environment IS the seam.
//
// All three variables move together: os.UserHomeDir reads HOME on unix and
// USERPROFILE on Windows, and os.UserConfigDir reads XDG_CONFIG_HOME on unix.
//
// This is done once in TestMain rather than per test with t.Setenv because
// t.Setenv panics inside a test that has called t.Parallel, and because a
// package-wide floor also covers tests written later -- a per-test call only
// protects the tests someone remembered to annotate. Tests that want their own
// isolated home on top of this floor can still call t.Setenv/t.TempDir.
func redirectHomeForTests() (cleanup func()) {
	dir, err := os.MkdirTemp("", "trvl-test-home-")
	if err != nil {
		panic("test home: " + err.Error())
	}
	os.Setenv("HOME", dir)
	os.Setenv("USERPROFILE", dir)
	os.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
	return func() { os.RemoveAll(dir) }
}

// TestMain opts this package's tests in to local destinations.
//
// The destination policy in destination.go refuses loopback addresses by
// default, and almost every test in this package points a provider config at
// an httptest server on 127.0.0.1. Setting the opt-in here is the same thing a
// developer running against a local mock does, which is exactly the case the
// opt-in exists for.
//
// It does mean the suite as a whole exercises the permissive path, so the
// tests that pin the DEFAULT clear this variable for their own duration with
// t.Setenv (see destination_test.go). Any future test that cares about the
// refusal has to do the same -- inheriting the opt-in silently is the failure
// mode to watch for here.
func TestMain(m *testing.M) {
	cleanup := redirectHomeForTests()
	os.Setenv(AllowLocalEnv, "1")
	code := m.Run()
	cleanup()
	os.Exit(code)
}
