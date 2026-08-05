package distributionmetrics

import (
	"path/filepath"
	"strings"
	"testing"
)

// trvl#532 medium triage. The gosec findings on this file's 0755 directory and
// 0644 dashboard write were annotated as CORRECT rather than tightened, on the
// grounds that DashboardPath is a repository file meant to be committed and read
// by anyone with the checkout -- not user data under $HOME.
//
// That reasoning is only sound while it stays true. If the default ever moved
// under the user's home, those permissions would become a real finding and the
// comment defending them would quietly become wrong. So the justification is
// asserted rather than merely written down.
func TestDashboardPathIsARepositoryFileNotUserData(t *testing.T) {
	cfg := DefaultConfig()

	if filepath.IsAbs(cfg.DashboardPath) {
		t.Errorf("DashboardPath = %q is absolute; the 0644/0755 permissions on this path are "+
			"justified by it being a repo-relative artefact meant to be shared. An absolute path "+
			"suggests it moved somewhere user-specific, where those permissions would be a real "+
			"finding", cfg.DashboardPath)
	}
	if strings.HasPrefix(cfg.DashboardPath, "~") || strings.Contains(cfg.DashboardPath, ".trvl") {
		t.Errorf("DashboardPath = %q points into user state; it is written 0644 in a directory "+
			"created 0755, which is correct for a committed doc and wrong for anything under "+
			"$HOME", cfg.DashboardPath)
	}
	if !strings.HasPrefix(cfg.DashboardPath, "docs/") {
		t.Errorf("DashboardPath = %q is no longer under docs/; re-check whether its permissions "+
			"are still justified before assuming so", cfg.DashboardPath)
	}
}
