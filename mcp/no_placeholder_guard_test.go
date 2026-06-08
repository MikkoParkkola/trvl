package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoPlaceholderAssistantBlocks is an enforcement gate (not just a one-off
// fix). Several MCP tool handlers historically emitted a hand-rolled
// assistant-audience content block with a placeholder string (e.g. "Structured
// data attached.") instead of the actual JSON-serialized result. Clients that
// read content blocks rather than structuredContent therefore got no data.
//
// This test fails the build if any non-test handler reintroduces a placeholder
// assistant block, forcing the use of buildAnnotatedContentBlocks. It is the
// "enforced, not documented" guard for that defect class.
func TestNoPlaceholderAssistantBlocks(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}

	// Forbidden placeholder fragments that indicate a hand-rolled assistant block
	// stating data is "attached" without actually embedding it.
	forbidden := []string{
		"data attached.",
	}

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		// This guard file itself names the forbidden strings; skip it.
		if name == "no_placeholder_guard_test.go" {
			continue
		}
		data, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		src := string(data)
		for _, frag := range forbidden {
			if strings.Contains(src, frag) {
				t.Errorf("%s contains placeholder assistant content %q; "+
					"use buildAnnotatedContentBlocks(summary, structuredPayload) so the "+
					"assistant receives real JSON, not a placeholder", name, frag)
			}
		}
	}
}
