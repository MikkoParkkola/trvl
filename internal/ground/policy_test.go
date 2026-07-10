package ground

import (
	"os"
	"strings"
	"testing"
)

// TestGroundSearchPolicyParity is the anti-regression source guard for
// CLI↔MCP parity on ground transport mode filtering.
//
// The bug class: MCP used to seed opts.Type from profile.GroundHints
// (PreferredType) when caller omitted `type`, causing filterGroundRoutes
// to hard-drop every route whose type != the user's historical dominant
// mode. The CLI (cmd/trvl/ground.go) never did this, so identical calls
// returned different result sets.
//
// Decision (#453 class): profile-derived preferred mode must NOT be
// applied as a hard filter. Explicit `type` still works; soft ranking
// by preference is future work.
//
// This test's source-guard half reads the real source files and fails if
// the seeding pattern (or equivalent) is reintroduced on either surface.
func TestGroundSearchPolicyParity(t *testing.T) {
	t.Run("neither surface seeds opts.Type from profile GroundHints (hard filter)", func(t *testing.T) {
		surfaces := map[string]string{
			"CLI": "../../cmd/trvl/ground.go",
			"MCP": "../../mcp/tools_ground.go",
		}
		// Direct guards against reintroducing the exact seeding bug that
		// only the MCP surface used to do (and that silently truncated
		// results by transport mode, exactly as #453 did for MaxPrice).
		forbidden := []string{
			"opts.Type = hints.PreferredType",
			"opts.Type = profile.GroundHints",
			"Type: hints.PreferredType",
			"Type = hints.PreferredType",
		}
		for name, path := range surfaces {
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%s: read %s: %v", name, path, err)
			}
			src := string(b)
			for _, pat := range forbidden {
				if strings.Contains(src, pat) {
					t.Errorf("%s (%s) contains %q — profile GroundHints must NOT feed opts.Type as a hard filter (parity with CLI; explicit type only; see #453 silent-truncation class)", name, path, pat)
				}
			}
			// Catch the call site pattern that used to load and apply it.
			// The explanatory comment in MCP contains the bare word but not the
			// call form "GroundHints(" that the buggy code used.
			if strings.Contains(src, "GroundHints(") {
				t.Errorf("%s (%s) calls GroundHints — profile mode hints must not be used to set Type (would reintroduce hard filter parity divergence with CLI)", name, path)
			}
		}
	})
}
