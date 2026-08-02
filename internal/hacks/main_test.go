package hacks

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain points the home directory this package's tests resolve at a
// throwaway location, so no test can read or mutate the developer's real
// ~/.trvl. Detectors here reach hotel and ground providers that consult the
// cookie cache (internal/providers/cookie_cache.go:53 resolves ~/.trvl/cookies
// from os.UserHomeDir and creates it), and that has no injection point other
// than the environment -- so the environment IS the seam.
//
// Doing it once here rather than per test with t.Setenv is deliberate: this
// package fans detectors out across goroutines, and t.Setenv is not usable from
// a test that has called t.Parallel, nor safe for work that outlives the test
// that started it. A process-wide redirect has neither problem, and it also
// covers tests added later, which a per-test call cannot.
//
// os.UserHomeDir reads HOME on unix and USERPROFILE on Windows, and
// os.UserConfigDir reads XDG_CONFIG_HOME on unix, so all three move together.
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
