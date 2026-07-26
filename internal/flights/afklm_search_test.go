package flights

import (
	"context"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/flights/afklm"
)

// TestSearchAFKLM_UnconfiguredReturnsClearError proves that an explicit
// `--provider afklm` request surfaces an actionable error (not an empty result)
// when no credential is configured. Unlike the silently-skipped composition
// sources, an explicit provider request must tell the user how to fix it.
//
// The credential chain (env, Keychain, 1Password) is environment-dependent, so
// this skips when a credential happens to be resolvable (e.g. a dev machine
// signed in to 1Password). It is deterministic, never flaky: it asserts only
// when the environment is genuinely unconfigured (the CI default).
func TestSearchAFKLM_UnconfiguredReturnsClearError(t *testing.T) {
	t.Setenv("AFKLM_KEY", "")
	if _, err := afklm.ResolveCredential(context.Background(), afklm.PolicyExternal); err == nil {
		t.Skip("an AF-KLM credential is resolvable in this environment; unconfigured-path test is CI-only")
	}

	_, err := SearchAFKLM(context.Background(), "AMS", "BCN", "2026-05-15", SearchOptions{ReturnDate: "2026-05-22"})
	if err == nil {
		t.Fatal("expected an error when AF-KLM is unconfigured, got nil")
	}
	for _, want := range []string{"afklm", "AFKLM_KEY"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error message should mention %q to be actionable; got: %v", want, err)
		}
	}
}
