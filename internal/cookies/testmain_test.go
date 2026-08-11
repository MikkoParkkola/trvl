package cookies

import (
	"errors"
	"os"
	"os/exec"
	"testing"
)

var originalTestProcessPATH = os.Getenv("PATH")

// TestMain keeps the package offline by default without changing PATH. A
// developer-installed nab may share its directory with unrelated tools, so
// removing that directory would make otherwise-valid tests environment
// dependent. Tests that exercise the helper contract opt into their exact fake
// executable with useNabPath.
func TestMain(m *testing.M) {
	lookupNabPath = func() (string, error) { return "", exec.ErrNotFound }
	os.Exit(m.Run())
}

func useNabPath(t *testing.T, path string) {
	t.Helper()
	previous := lookupNabPath
	lookupNabPath = func() (string, error) { return path, nil }
	t.Cleanup(func() { lookupNabPath = previous })
}

func hideNabAtLookupSeam(t *testing.T) {
	t.Helper()
	previous := lookupNabPath
	lookupNabPath = func() (string, error) { return "", errors.New("controlled nab lookup failure") }
	t.Cleanup(func() { lookupNabPath = previous })
}

func TestPackageIsolationPreservesPATH(t *testing.T) {
	if originalTestProcessPATH == "" {
		t.Skip("PATH is empty in this test environment")
	}
	if got := os.Getenv("PATH"); got != originalTestProcessPATH {
		t.Fatalf("package isolation changed PATH from %q to %q", originalTestProcessPATH, got)
	}
}
