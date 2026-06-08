package mcp

import (
	"testing"
)

func TestDetectTravelHacksTool_Definition(t *testing.T) {
	t.Parallel()
	tool := detectTravelHacksTool()
	if tool.Name != "detect_travel_hacks" {
		t.Errorf("Name = %q, want detect_travel_hacks", tool.Name)
	}
	if len(tool.InputSchema.Required) == 0 {
		t.Error("expected required params")
	}
}

func TestDetectAccommodationHacksTool_Definition(t *testing.T) {
	t.Parallel()
	tool := detectAccommodationHacksTool()
	if tool.Name != "detect_accommodation_hacks" {
		t.Errorf("Name = %q, want detect_accommodation_hacks", tool.Name)
	}
	if len(tool.InputSchema.Required) == 0 {
		t.Error("expected required params")
	}
}
