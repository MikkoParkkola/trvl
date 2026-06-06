package mcp

import "testing"

// TestAllToolsCarryAnnotations enforces MCP 2025-11-25 tool-behavior coverage:
// every advertised tool must carry a ToolAnnotations block with a human-readable
// Title, plus a non-empty Description. Behavior hints (readOnly/destructive/
// idempotent/openWorld) are booleans defaulting false; presence of the
// Annotations block is what we assert. Runs against the full legacy surface so
// every registered tool is checked, not just the smart `travel` router. MIK-2984.
func TestAllToolsCarryAnnotations(t *testing.T) {
	t.Setenv(smartToolModeEnv, "legacy")
	s := NewServer()
	if len(s.tools) < 60 {
		t.Fatalf("expected full legacy tool surface, got %d", len(s.tools))
	}
	for _, tool := range s.tools {
		if tool.Annotations == nil {
			t.Errorf("tool %q missing Annotations block", tool.Name)
			continue
		}
		if tool.Annotations.Title == "" {
			t.Errorf("tool %q missing Annotations.Title", tool.Name)
		}
		if tool.Title == "" {
			t.Errorf("tool %q missing top-level Title", tool.Name)
		}
		if tool.Description == "" {
			t.Errorf("tool %q missing Description", tool.Name)
		}
	}
}
