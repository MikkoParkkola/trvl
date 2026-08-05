package watch

import (
	"strings"
	"testing"
	"time"
)

// trvl#514 -- the three retention limits are configurable, and an unusable
// value is REFUSED rather than clamped.
//
// The clamping distinction is the whole point of TRVL.RETENTION.4. An operator
// who sets 0 intending "no cap" and silently receives 1000 has a store that
// behaves one way while its configuration says another, and nothing ever tells
// them. Refusing is louder and therefore kinder.

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// TRVL.RETENTION.1 -- the current values are the defaults, and an unset
// environment changes nothing. This ticket is explicitly not about picking
// different numbers.
func TestRetentionDefaultsAreUnchangedWhenUnset(t *testing.T) {
	cfg, err := loadRetention(env(nil))
	if err != nil {
		t.Fatalf("unset environment must be valid: %v", err)
	}
	if cfg.MaxPointsPerWatch != maxObservationsPerWatch {
		t.Errorf("MaxPointsPerWatch = %d, want the compiled default %d", cfg.MaxPointsPerWatch, maxObservationsPerWatch)
	}
	if cfg.MaxPointsTotal != maxWatchObservations {
		t.Errorf("MaxPointsTotal = %d, want the compiled default %d", cfg.MaxPointsTotal, maxWatchObservations)
	}
	if cfg.RouteTTL != routeWatchTTL {
		t.Errorf("RouteTTL = %v, want the compiled default %v", cfg.RouteTTL, routeWatchTTL)
	}
}

// Each variable actually takes effect. A setting that parsed but was ignored
// would be the decorative-guard defect this repo has already fixed twice.
func TestRetentionOverridesTakeEffect(t *testing.T) {
	cfg, err := loadRetention(env(map[string]string{
		EnvMaxPointsPerWatch: "250",
		EnvMaxPointsTotal:    "9000",
		EnvRouteTTLDays:      "30",
	}))
	if err != nil {
		t.Fatalf("valid overrides rejected: %v", err)
	}
	if cfg.MaxPointsPerWatch != 250 {
		t.Errorf("MaxPointsPerWatch = %d, want 250", cfg.MaxPointsPerWatch)
	}
	if cfg.MaxPointsTotal != 9000 {
		t.Errorf("MaxPointsTotal = %d, want 9000", cfg.MaxPointsTotal)
	}
	if cfg.RouteTTL != 30*24*time.Hour {
		t.Errorf("RouteTTL = %v, want 30 days", cfg.RouteTTL)
	}
}

// TRVL.RETENTION.4 -- refused, not clamped, and the error names the variable so
// the operator can find it without reading the source.
func TestRetentionRejectsUnusableValues(t *testing.T) {
	for _, tc := range []struct {
		name string
		vars map[string]string
		want string
	}{
		{"zero", map[string]string{EnvMaxPointsPerWatch: "0"}, EnvMaxPointsPerWatch},
		{"negative", map[string]string{EnvMaxPointsTotal: "-5"}, EnvMaxPointsTotal},
		{"not a number", map[string]string{EnvRouteTTLDays: "ninety"}, EnvRouteTTLDays},
		{"empty-ish whitespace", map[string]string{EnvMaxPointsTotal: " "}, EnvMaxPointsTotal},
		{"absurdly large", map[string]string{EnvMaxPointsPerWatch: "999999999"}, EnvMaxPointsPerWatch},
		{"float", map[string]string{EnvRouteTTLDays: "1.5"}, EnvRouteTTLDays},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := loadRetention(env(tc.vars))
			if err == nil {
				t.Fatalf("%v was accepted, yielding %+v -- an unusable value must be refused, "+
					"because clamping leaves the store behaving differently from its configuration "+
					"with nothing to tell the operator", tc.vars, cfg)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error does not name %s, so an operator cannot find what to change: %v",
					tc.want, err)
			}
		})
	}
}

// A per-watch cap above the global backstop is meaningless rather than
// dangerous -- the backstop evicts first -- but accepting it would let an
// operator believe a setting is active when it can never take effect.
func TestRetentionRejectsAPerWatchCapAboveTheGlobalBackstop(t *testing.T) {
	_, err := loadRetention(env(map[string]string{
		EnvMaxPointsPerWatch: "20000",
		EnvMaxPointsTotal:    "5000",
	}))
	if err == nil {
		t.Fatal("a per-watch cap above the global backstop was accepted; the backstop evicts " +
			"first, so that per-watch value could never take effect and the operator would " +
			"never learn it")
	}
	if !strings.Contains(err.Error(), "never take effect") {
		t.Errorf("the error does not explain why the combination is refused: %v", err)
	}
}

// TRVL.RETENTION.4 at the seam that matters: Load is where every consumer
// already checks for an error, so a bad override must stop the store loading
// rather than surface later during an eviction, or not at all.
func TestLoadRefusesAnInvalidRetentionOverride(t *testing.T) {
	t.Setenv(EnvMaxPointsTotal, "0")

	s := NewStore(t.TempDir())
	err := s.Load()
	if err == nil {
		t.Fatal("Load accepted an invalid retention override; the store would run with limits " +
			"the operator did not set and would never be told")
	}
	if !strings.Contains(err.Error(), EnvMaxPointsTotal) {
		t.Errorf("Load error does not name the offending variable: %v", err)
	}
}

// And the control: a valid override must not stop the store loading.
func TestLoadAcceptsAValidRetentionOverride(t *testing.T) {
	t.Setenv(EnvMaxPointsTotal, "9000")

	s := NewStore(t.TempDir())
	if err := s.Load(); err != nil {
		t.Fatalf("Load rejected a valid override: %v", err)
	}
	if got := s.retentionOrDefault().MaxPointsTotal; got != 9000 {
		t.Errorf("store retention = %d, want the configured 9000 -- a setting that loads but is "+
			"not used is not a setting", got)
	}
}
