package providers

// MIK-3075: forward-migration of provider configs.
//
// Each released schema gets a string version of the form "vMAJOR.MINOR".
// The runtime walks every config through the migration chain at load
// time so legacy configs (no `schema_version` field) self-update to the
// current schema, and configs declaring a future version fail loudly.
//
// We deliberately use a simple comparable string format ("v1.0", "v1.1")
// rather than full semver to avoid pulling a parser in on a hot load
// path. The compare function treats any non-matching value as "older
// than v0", which forces an explicit migration to be authored.

import (
	"fmt"
	"strconv"
	"strings"
)

// CurrentSchemaVersion is the schema version this binary writes when
// saving a fresh provider config. Bump this AND register a migration
// whenever ProviderConfig changes shape in a way that needs touch-up
// of existing on-disk files.
const CurrentSchemaVersion = "v1.0"

// Migration describes a single transformation from one schema version
// to the next. Migrations are applied in registry order; each must
// idempotently leave the config in a valid state for `To`.
type Migration struct {
	From string
	To   string
	Fn   func(*ProviderConfig) error
}

// migrations is the registered chain. Append to this list whenever a
// new CurrentSchemaVersion ships. Order matters — the chain is walked
// front-to-back from each config's current version.
var migrations = []Migration{
	{
		From: "",
		To:   "v1.0",
		Fn:   migrateLegacyToV1_0,
	},
}

// Migrate walks the migration chain and returns the config in its
// current-schema form. Returns an error when:
//   - the config declares a version newer than CurrentSchemaVersion
//   - a registered migration fails
//   - no migration exists from the config's current version
//
// Idempotent: a config already at CurrentSchemaVersion returns nil
// immediately.
func Migrate(cfg *ProviderConfig) error {
	if cfg == nil {
		return fmt.Errorf("providers: nil config")
	}
	cur := cfg.SchemaVersion
	if cur == CurrentSchemaVersion {
		return nil
	}
	if newer, err := isNewerThanCurrent(cur); err == nil && newer {
		return fmt.Errorf("providers: config %q schema_version %q is newer than this binary supports (%s) — upgrade trvl",
			cfg.ID, cur, CurrentSchemaVersion)
	}

	// Apply migrations in chain order until we reach the current version.
	for cfg.SchemaVersion != CurrentSchemaVersion {
		applied := false
		for _, m := range migrations {
			if m.From != cfg.SchemaVersion {
				continue
			}
			if err := m.Fn(cfg); err != nil {
				return fmt.Errorf("providers: migrate %q %s -> %s: %w", cfg.ID, m.From, m.To, err)
			}
			cfg.SchemaVersion = m.To
			applied = true
			break
		}
		if !applied {
			return fmt.Errorf("providers: no migration from %q for config %q", cfg.SchemaVersion, cfg.ID)
		}
	}
	return nil
}

// migrateLegacyToV1_0 is the example migration required by the AC. It
// promotes legacy configs (no schema_version set) to v1.0 by ensuring:
//   - Version (the per-config edit counter) is at least 1
//   - Method defaults to "GET" when empty
//
// Both fixes are conservative: they only touch obvious gaps that the
// pre-MIK-3075 code paths used to mask via zero-value defaults.
func migrateLegacyToV1_0(cfg *ProviderConfig) error {
	if cfg.Version <= 0 {
		cfg.Version = 1
	}
	if strings.TrimSpace(cfg.Method) == "" {
		cfg.Method = "GET"
	}
	return nil
}

// isNewerThanCurrent compares a `vMAJOR.MINOR` string against
// CurrentSchemaVersion. Returns (true, nil) when `cur` is strictly
// greater. Anything that fails to parse is reported as not-newer with
// an error, which the caller may choose to ignore — the migration loop
// will still produce a clear "no migration from X" error downstream.
func isNewerThanCurrent(cur string) (bool, error) {
	cm, cmi, err := parseVersion(cur)
	if err != nil {
		return false, err
	}
	tm, tmi, _ := parseVersion(CurrentSchemaVersion)
	if cm > tm {
		return true, nil
	}
	if cm == tm && cmi > tmi {
		return true, nil
	}
	return false, nil
}

// parseVersion accepts strings of the form "vMAJOR.MINOR" and returns
// (major, minor, nil). Anything else returns an error.
func parseVersion(s string) (int, int, error) {
	if !strings.HasPrefix(s, "v") {
		return 0, 0, fmt.Errorf("missing v prefix")
	}
	rest := s[1:]
	parts := strings.SplitN(rest, ".", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("expected vMAJOR.MINOR")
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("major: %w", err)
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("minor: %w", err)
	}
	return major, minor, nil
}
