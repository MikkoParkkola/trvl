package testutil

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// probeGuardExempt lists live probes that classify provider errors with their
// own typed checks instead of the shared transient helpers. Each entry needs a
// reason; keep it short.
var probeGuardExempt = map[string]string{
	// Asserts on typed Wizz error classes (ErrWizzVersionRotated,
	// ErrWizzBlocked, ErrWizzMarketRejected) and their fix-hint codes.
	"internal/flights/wizzair_probe_test.go": "typed Wizz error classes",
}

// probeGuardMarkers are the shared helpers that route a live-probe error
// through the transient classification.
var probeGuardMarkers = []string{
	"SkipIfTransient(",
	"IsTransientProbeErr(",
	"IsTransientMsg(",
	"RetryTransient(",
	"probeBaseline(", // internal/flights probe helper built on the above
	"throttle.check(",
}

// TestLiveProbesRouteErrorsThroughTransientGate fails when a *_probe_test.go
// file runs a live probe (RequireLiveProbe) but never routes provider errors
// through the shared transient classification. Without it, a throttled night
// (e.g. Google quota refusals on CI runner IPs, trvl#720) reds the Live Probes
// workflow and is reported as wire-format drift.
func TestLiveProbesRouteErrorsThroughTransientGate(t *testing.T) {
	root := moduleRoot(t)
	checked := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_probe_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		s := string(src)
		if !strings.Contains(s, "RequireLiveProbe(") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		checked++
		if _, ok := probeGuardExempt[rel]; ok {
			return nil
		}
		for _, m := range probeGuardMarkers {
			if strings.Contains(s, m) {
				return nil
			}
		}
		t.Errorf("%s runs a live probe but never routes errors through testutil.SkipIfTransient / IsTransientProbeErr; "+
			"a throttled run would be reported as drift (add the helper, or an exemption with a reason)", rel)
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if checked == 0 {
		t.Fatal("found no live probe files; guard is not scanning the module")
	}
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
