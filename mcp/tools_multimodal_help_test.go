package mcp

import (
	"strings"
	"testing"
)

// The planner attaches no travel-hack saving (#824), so the tool description
// an agent reads must not promise one.
func TestPlanMultimodalTool_PromisesNoSavingsAnnotation(t *testing.T) {
	if d := strings.ToLower(planMultimodalTool().Description); strings.Contains(d, "annotates") || strings.Contains(d, "travel-hack saving") {
		t.Errorf("plan_multimodal description still promises a travel-hack saving: %s", planMultimodalTool().Description)
	}
}
