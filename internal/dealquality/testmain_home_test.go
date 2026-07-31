package dealquality

import (
	"os"
	"testing"
)

// TestMain redirects HOME at a temp directory for the whole package.
//
// watch.DefaultStore() resolves ~/.trvl from os.UserHomeDir(), which reads HOME.
// Tests in this package reach it, so an unguarded `go test ./...` READ AND WROTE
// the developer's real watch store — observed 2026-07-26, when a test run
// stamped a new field onto all 386 of a maintainer's live watches. Test runs
// must never touch real user data.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "trvl-test-home-")
	if err != nil {
		panic("create test HOME: " + err.Error())
	}
	defer func() { _ = os.RemoveAll(dir) }()
	_ = os.Setenv("HOME", dir)
	_ = os.Setenv("USERPROFILE", dir) // windows
	os.Exit(m.Run())
}
