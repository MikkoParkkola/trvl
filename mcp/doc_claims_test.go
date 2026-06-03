package mcp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDocClaimsMatchRegistry is the anti-drift guard for trvl's headline
// marketing claim: "1 advertised smart tool, N compatibility aliases".
//
// The canonical counts are DERIVED FROM THE REGISTRY (NewServer), not
// hand-typed, so docs are checked against ground truth. This exists because
// the tool count has repeatedly drifted across README / npm / CONTRIBUTING /
// docs / CLAUDE.md (see CLAUDE.md anti-pattern note + #41 precedent). When the
// tool surface changes, this test fails until every public surface is updated
// in the same PR.
func TestDocClaimsMatchRegistry(t *testing.T) {
	// Canonical advertised surface (compact/default mode) = exactly 1 tool.
	t.Setenv("TRVL_MCP_TOOL_MODE", "")
	if err := os.Unsetenv("TRVL_MCP_TOOL_MODE"); err != nil {
		t.Fatalf("unset TRVL_MCP_TOOL_MODE: %v", err)
	}
	compact := NewServer()
	if len(compact.tools) != 1 {
		t.Fatalf("canonical compact surface must advertise exactly 1 tool (the travel router), got %d", len(compact.tools))
	}

	// Canonical alias count = the legacy-mode advertised surface.
	t.Setenv("TRVL_MCP_TOOL_MODE", "legacy")
	legacy := NewServer()
	aliasCount := len(legacy.tools)
	if aliasCount == 0 {
		t.Fatalf("legacy surface must advertise the compatibility aliases, got 0")
	}
	aliasStr := fmt.Sprint(aliasCount) // ground truth, currently "64"

	// Every doc surface that states the alias count MUST state the canonical
	// number, and MUST NOT reintroduce any historical stale claim.
	docs := []string{
		"../README.md",
		"../npm/README.md",
		"../npm/package.json",
		"../CONTRIBUTING.md",
		"../CLAUDE.md",
		"../AGENTS.md",
		"../demo.tape",
		"../docs/ARCHITECTURE.md",
		"../docs/MCP-ORCHESTRATION.md",
		"../docs/POSITIONING.md",
		"../docs/COMPARISON.md",
		"../docs/DISTRIBUTION.md",
		"../plugin/README.md",
		"../.claude/skills/trvl.md",
	}

	// Stale tool-count claims that previously drifted. If any reappears, a
	// surface was updated without re-deriving from the registry.
	forbidden := []string{
		"60 tools",
		"41 MCP tools",
		"18 tools",
		"41 tools",
		"62 compatibility aliases",
		"63 compatibility aliases",
		"old 63 tool names",
		"55 commands",
		"50 commands",
		"55 CLI commands",
		"50 CLI commands",
		"57→61 tool bump",
	}

	// Docs that carry the explicit alias count and must state the canonical one.
	mustStateAlias := map[string]bool{
		"../README.md":              true,
		"../CONTRIBUTING.md":        true,
		"../CLAUDE.md":              true,
		"../AGENTS.md":              true,
		"../plugin/README.md":       true,
		"../docs/POSITIONING.md":    true,
		"../.claude/skills/trvl.md": true,
	}

	for _, rel := range docs {
		path := filepath.Clean(rel)
		raw, err := os.ReadFile(path)
		if err != nil {
			// A missing optional doc should not fail the suite; a present one must be clean.
			if os.IsNotExist(err) {
				continue
			}
			t.Fatalf("read %s: %v", path, err)
		}
		body := string(raw)

		for _, bad := range forbidden {
			if strings.Contains(body, bad) {
				t.Errorf("%s contains stale tool-count claim %q — re-derive counts from the registry (canonical: 1 advertised + %s aliases) and update every surface in one PR", path, bad, aliasStr)
			}
		}

		if mustStateAlias[rel] {
			if !strings.Contains(body, aliasStr+" compatibility aliases") &&
				!strings.Contains(body, aliasStr+" aliases") &&
				!strings.Contains(body, aliasStr+" tool") {
				t.Errorf("%s must state the canonical alias count %q (registry ground truth) — found neither %q nor a close variant", path, aliasStr, aliasStr+" compatibility aliases")
			}
		}
	}
}
