package selfupdate

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstallMethodString(t *testing.T) {
	t.Parallel()
	cases := []struct {
		m    InstallMethod
		want string
	}{
		{InstallMethodDev, "dev"},
		{InstallMethodBrew, "brew"},
		{InstallMethodGo, "go"},
		{InstallMethodNpm, "npm"},
		{InstallMethodStandalone, "standalone"},
		{InstallMethodUnclassified, "unclassified"},
		{InstallMethod(99), "unclassified"}, // out-of-range falls back
	}
	for _, tc := range cases {
		if got := tc.m.String(); got != tc.want {
			t.Errorf("(%d).String() = %q, want %q", tc.m, got, tc.want)
		}
	}
}

func TestInstallMethodSupportsInPlaceReplace(t *testing.T) {
	t.Parallel()
	cases := []struct {
		m    InstallMethod
		want bool
	}{
		{InstallMethodStandalone, true},
		{InstallMethodBrew, false},
		{InstallMethodGo, false},
		{InstallMethodNpm, false},
		{InstallMethodDev, false},
		{InstallMethodUnclassified, false},
	}
	for _, tc := range cases {
		if got := tc.m.SupportsInPlaceReplace(); got != tc.want {
			t.Errorf("(%s).SupportsInPlaceReplace() = %v, want %v", tc.m, got, tc.want)
		}
	}
}

func TestInstallMethodUpgradeHint(t *testing.T) {
	t.Parallel()
	cases := []struct {
		m       InstallMethod
		wantSub string // substring that must be in the hint; empty means hint must be empty
	}{
		{InstallMethodBrew, "brew upgrade trvl"},
		{InstallMethodGo, "go install"},
		{InstallMethodNpm, "npm install -g trvl-mcp"},
		{InstallMethodStandalone, "trvl self-update"},
		{InstallMethodDev, ""},
		{InstallMethodUnclassified, ""},
	}
	for _, tc := range cases {
		got := tc.m.UpgradeHint()
		if tc.wantSub == "" {
			if got != "" {
				t.Errorf("(%s).UpgradeHint() = %q, want empty", tc.m, got)
			}
			continue
		}
		if !strings.Contains(got, tc.wantSub) {
			t.Errorf("(%s).UpgradeHint() = %q, want substring %q", tc.m, got, tc.wantSub)
		}
	}
}

func TestDetectInstallMethod_Dev(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"", "dev", " dev ", "\t"} {
		if got := DetectInstallMethod(v); got != InstallMethodDev {
			t.Errorf("DetectInstallMethod(%q) = %s, want dev", v, got)
		}
	}
}

func TestIsBrewPath(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"/opt/homebrew/Cellar/trvl/1.1.4/bin/trvl":                  true,
		"/usr/local/Cellar/trvl/1.1.3_1/bin/trvl":                   true,
		"/home/linuxbrew/.linuxbrew/Cellar/trvl/1.0.0/bin/trvl":     true,
		"C:\\Program Files\\Homebrew\\Cellar\\trvl\\1.1.4\\bin.exe": false, // not a real Windows brew path
		"/usr/local/bin/trvl":                                       false,
		"/Users/me/go/bin/trvl":                                     false,
		"":                                                          false,
		"/some/path/with/Cellar/other/1.0/bin/trvl":                 false, // not /Cellar/trvl/
	}
	for p, want := range cases {
		if got := isBrewPath(p); got != want {
			t.Errorf("isBrewPath(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestIsGoBinPath_GOBIN(t *testing.T) {
	// Cannot use t.Parallel with t.Setenv.
	tmp := t.TempDir()
	t.Setenv("GOBIN", tmp)
	t.Setenv("GOPATH", "")

	if got := isGoBinPath(filepath.Join(tmp, "trvl")); !got {
		t.Errorf("isGoBinPath under GOBIN should return true")
	}
	if got := isGoBinPath("/somewhere/else/trvl"); got {
		t.Errorf("isGoBinPath outside GOBIN should return false")
	}
}

func TestIsGoBinPath_GOPATH(t *testing.T) {
	// Cannot use t.Parallel with t.Setenv.
	tmp := t.TempDir()
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", tmp)

	if got := isGoBinPath(filepath.Join(tmp, "bin", "trvl")); !got {
		t.Errorf("isGoBinPath under $GOPATH/bin should return true")
	}
}

func TestIsNpmPath(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"/Users/me/.npm/lib/node_modules/trvl-mcp/bin/trvl": true,
		"/usr/local/lib/node_modules/trvl-mcp/bin/trvl":     true,
		"/proj/node_modules/trvl-mcp/bin/trvl":              true,
		"/proj/node_modules/something-else/bin/trvl":        false,
		"/usr/local/bin/trvl":                               false,
		"/opt/homebrew/Cellar/trvl/1.1.4/bin/trvl":          false,
	}
	for p, want := range cases {
		if got := isNpmPath(p); got != want {
			t.Errorf("isNpmPath(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestIsPlausibleStandalonePath_RejectsTemp(t *testing.T) {
	t.Parallel()
	tmp := os.TempDir()
	if tmp == "" {
		t.Skip("os.TempDir empty")
	}
	tempBin := filepath.Join(tmp, "go-build-cache", "trvl")
	if got := isPlausibleStandalonePath(tempBin); got {
		t.Errorf("isPlausibleStandalonePath(%q under temp) = true, want false", tempBin)
	}
}

func TestIsPlausibleStandalonePath_AcceptsRealPaths(t *testing.T) {
	t.Parallel()
	candidates := []string{
		"/usr/local/bin/trvl",
		"/opt/trvl/bin/trvl",
	}
	if runtime.GOOS == "windows" {
		candidates = []string{
			"C:\\Users\\me\\bin\\trvl.exe",
			"C:\\Tools\\trvl\\trvl.exe",
		}
	}
	for _, p := range candidates {
		if got := isPlausibleStandalonePath(p); !got {
			t.Errorf("isPlausibleStandalonePath(%q) = false, want true", p)
		}
	}
}

func TestIsPlausibleStandalonePath_Empty(t *testing.T) {
	t.Parallel()
	if isPlausibleStandalonePath("") {
		t.Errorf("isPlausibleStandalonePath(\"\") should be false")
	}
}

// TestDetectInstallMethod_RealBinary documents what classification the
// running test binary itself receives. This is informational — the test
// binary lives under $TMPDIR during `go test`, so we expect
// InstallMethodUnclassified (excluded by isPlausibleStandalonePath).
// If the test runner ever changes its temp-dir convention this test
// flags it; the contract is "non-Dev, non-Brew, non-Npm" — anything
// else is acceptable.
func TestDetectInstallMethod_RealBinary(t *testing.T) {
	t.Parallel()
	got := DetectInstallMethod("1.2.3")
	switch got {
	case InstallMethodDev:
		t.Errorf("test binary should not classify as dev when version is non-empty")
	case InstallMethodBrew:
		t.Errorf("test binary should not classify as brew")
	case InstallMethodNpm:
		t.Errorf("test binary should not classify as npm")
	}
	// Standalone, Go, or Unclassified are all acceptable here depending
	// on whether $TMPDIR happens to coincide with $GOBIN on this host.
}
