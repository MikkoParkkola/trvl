package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain points the home directory this package's tests resolve at a
// throwaway location, so no test can read or mutate the developer's real
// ~/.trvl. CLI commands here persist preferences, trips, watches, alerts,
// price history, last-search state and provider state under os.UserHomeDir,
// and the scheduler/install commands write launch agents and MCP client
// configs there too -- none of which has an injection point other than the
// environment, so the environment IS the seam.
//
// This is not hypothetical: an unguarded `go test ./...` read AND wrote a
// maintainer's real watch store on 2026-07-26, stamping a new field onto all
// 386 live watches. Test runs must never touch real user data.
//
// All three variables move together: os.UserHomeDir reads HOME on unix and
// USERPROFILE on Windows, and os.UserConfigDir reads XDG_CONFIG_HOME on unix.
//
// This is done once here rather than per test with t.Setenv because t.Setenv
// panics inside a test that has called t.Parallel, and this package uses
// t.Parallel widely. A package-wide floor also covers tests written later,
// which a per-test call cannot. Tests wanting their own isolated home on top
// of this floor still call t.Setenv/t.TempDir.
//
// Cleanup runs BEFORE os.Exit, deliberately: os.Exit does not run deferred
// functions, so `defer os.RemoveAll(dir)` here would leak a temp directory on
// every run while looking correct.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "trvl-test-home-")
	if err != nil {
		panic("test home: " + err.Error())
	}
	os.Setenv("HOME", dir)
	os.Setenv("USERPROFILE", dir)
	os.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
