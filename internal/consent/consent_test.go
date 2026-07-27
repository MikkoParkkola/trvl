package consent

import "testing"

// TestCookiesDeclined pins the rule itself, at the one place it now lives.
//
// The wrappers in internal/cookies and internal/nab are checked against each
// other elsewhere; this checks what they both delegate to. The bias is
// deliberate and worth pinning: anything the user typed that is not an explicit
// denial counts as a decline, because being liberal here can only refuse an
// access they gestured at refusing, while being strict would silently ignore
// them -- the failure the opt-out exists to stop.
func TestCookiesDeclined(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"", false}, {"0", false}, {"false", false}, {"FALSE", false},
		{" false ", false}, {"  ", false},
		{"1", true}, {"true", true}, {"yes", true}, {"no", true},
		{"off", true}, {"please", true}, {"2", true},
	} {
		t.Setenv(CookiesEnv, tc.value)
		if got := CookiesDeclined(); got != tc.want {
			t.Errorf("%s=%q: CookiesDeclined() = %v, want %v", CookiesEnv, tc.value, got, tc.want)
		}
	}
}

// TestTier2Declined covers the second variable, which has one extra rule: the
// old opt-in still declines when set to an explicit denial, so flipping the
// default to on does not quietly overrule someone who had turned it off.
func TestTier2Declined(t *testing.T) {
	for _, tc := range []struct {
		name       string
		optOut     string
		legacyOptI string
		want       bool
	}{
		{"neither set", "", "", false},
		{"opt-out set", "1", "", true},
		{"opt-out set to an arbitrary truthy value", "yes", "", true},
		{"opt-out explicitly denied", "0", "", false},
		{"legacy opt-in explicitly zero", "", "0", true},
		{"legacy opt-in explicitly false", "", "false", true},
		{"legacy opt-in explicitly no", "", "no", true},
		{"legacy opt-in enabled", "", "1", false},
		{"opt-out wins over an enabled legacy opt-in", "1", "1", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(Tier2Env, tc.optOut)
			t.Setenv(Tier2LegacyEnv, tc.legacyOptI)
			if got := Tier2Declined(); got != tc.want {
				t.Errorf("%s=%q %s=%q: Tier2Declined() = %v, want %v",
					Tier2Env, tc.optOut, Tier2LegacyEnv, tc.legacyOptI, got, tc.want)
			}
		})
	}
}
