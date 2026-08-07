package providers

import (
	"strings"
	"testing"
)

// TRVL.FIXHINT.1 -- a fix hint must not prescribe a tool that always fails.
//
// #538 made provider definitions source-only, so configure_provider now returns
// an error unconditionally. Three FixHints still told the operator (and, more
// often, an agent acting on ProviderStatus.FixHint) to call it: on a WAF block,
// on an expired cookie, and on a changed response shape.
//
// That is worse than a stale doc. These strings are returned on the failure
// path the trust-boundary change made MORE common, and an agent that treats
// them as remediation gets a guaranteed error loop with nothing to learn from.
//
// Found by adversarial review of the release delta, after the CHANGELOG,
// LEGAL.md, DESIGN.md and the MCP setup prompt had all been corrected. The
// runtime hints were a layer below the docs I went looking for.
func TestFixHintsDoNotPrescribeConfigureProvider(t *testing.T) {
	// Messages chosen to hit each branch of classifyProviderError that used to
	// name the tool, plus the preflight branch next to them.
	for _, probe := range []string{
		"waf: 403 forbidden",
		"access denied by akamai",
		"cookie rejected: 401 unauthorized",
		"csrf token mismatch",
		"results_path produced no match",
		"response shape changed: unexpected end of json",
		"preflight failed",
	} {
		_, hint := classifyProviderError(errString(probe))
		if strings.Contains(hint, "configure_provider") {
			t.Errorf("the fix hint for %q tells the caller to use configure_provider, which "+
				"returns an error unconditionally since #538. An agent following it loops on a "+
				"refusal it cannot fix.\n  hint: %s", probe, hint)
		}
	}
}

type errString string

func (e errString) Error() string { return string(e) }
