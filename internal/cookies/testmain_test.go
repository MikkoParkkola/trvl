package cookies

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestMain removes developer-installed nab executables from the package test
// process's PATH. Tests that exercise the helper contract put their own
// controlled fake first; every other test must remain offline and must never
// reach a real browser cookie store or macOS Keychain.
func TestMain(m *testing.M) {
	originalPath := os.Getenv("PATH")
	if err := os.Setenv("PATH", pathWithoutNab(originalPath)); err != nil {
		_, _ = os.Stderr.WriteString("internal/cookies: isolate nab from PATH: " + err.Error() + "\n")
		os.Exit(1)
	}

	code := m.Run()
	_ = os.Setenv("PATH", originalPath)
	os.Exit(code)
}

// pathWithoutNab preserves normal system tools while removing any PATH entry
// that could resolve the real nab helper. This is safer than replacing PATH
// with an empty directory because several controlled fake-helper tests use
// shell utilities such as sleep.
func pathWithoutNab(pathValue string) string {
	entries := filepath.SplitList(pathValue)
	kept := entries[:0]
	for _, entry := range entries {
		if entry == "" || !directoryContainsNab(entry) {
			kept = append(kept, entry)
		}
	}
	return strings.Join(kept, string(os.PathListSeparator))
}

func directoryContainsNab(dir string) bool {
	candidates := []string{"nab"}
	if runtime.GOOS == "windows" {
		candidates = append(candidates, "nab.exe", "nab.com", "nab.bat", "nab.cmd")
	}
	for _, name := range candidates {
		if info, err := os.Stat(filepath.Join(dir, name)); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

func TestPathWithoutNabRemovesHelperDirectoryAndPreservesSystemTools(t *testing.T) {
	withNab := t.TempDir()
	withoutNab := t.TempDir()
	name := "nab"
	if runtime.GOOS == "windows" {
		name = "nab.exe"
	}
	if err := os.WriteFile(filepath.Join(withNab, name), []byte("controlled test helper"), 0o600); err != nil {
		t.Fatalf("write controlled nab marker: %v", err)
	}

	got := filepath.SplitList(pathWithoutNab(strings.Join(
		[]string{withoutNab, withNab},
		string(os.PathListSeparator),
	)))
	if len(got) != 1 || got[0] != withoutNab {
		t.Fatalf("sanitized PATH entries = %q, want only %q", got, withoutNab)
	}
}
