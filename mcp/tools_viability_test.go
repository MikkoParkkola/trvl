package mcp

import (
	"testing"
)

func TestAssessTripTool_Definition(t *testing.T) {
	t.Parallel()
	tool := assessTripTool()
	if tool.Name != "assess_trip" {
		t.Errorf("Name = %q, want assess_trip", tool.Name)
	}
	if len(tool.InputSchema.Required) == 0 {
		t.Error("expected required params")
	}
}

// handleAssessTrip does not validate missing params at the handler level —
// it passes empty strings to the external API. We only test definition.
