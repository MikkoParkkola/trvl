package trip

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain points the home directory this package's tests resolve at a
// throwaway location, so no test can read or mutate the developer's real
// ~/.trvl. Trip planning fans out to hotel and ground providers that consult
// the cookie cache (internal/providers/cookie_cache.go:53 resolves
// ~/.trvl/cookies from os.UserHomeDir and creates it), and that has no
// injection point other than the environment -- so the environment IS the seam.
//
// Doing it once here rather than per test with t.Setenv is deliberate: planning
// runs provider lookups on goroutines that can outlive the test that started
// them, and t.Setenv is also unusable from a test that has called t.Parallel.
// A process-wide redirect has neither problem, and it also covers tests added
// later, which a per-test call cannot.
//
// os.UserHomeDir reads HOME on unix and USERPROFILE on Windows, and
// os.UserConfigDir reads XDG_CONFIG_HOME on unix, so all three move together.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "trvl-test-home-")
	if err != nil {
		panic("test home: " + err.Error())
	}
	_ = os.Setenv("HOME", dir)
	_ = os.Setenv("USERPROFILE", dir)
	_ = os.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
