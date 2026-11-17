package mcp

import (
	"strings"
	"testing"
)

// The planner attaches no travel-hack saving (#824), so the tool description
// an agent reads must not promise one. It must still describe the ranking it
// does keep, so empty or miswired text cannot pass.
func TestPlanMultimodalTool_PromisesNoSavingsAnnotation(t *testing.T) {
	d := planMultimodalTool().Description
	if !strings.Contains(d, "ranks by that total") {
		t.Fatalf("plan_multimodal description lost its ranking description: %s", d)
	}
	if strings.Contains(d, "annotates travel-hack savings") {
		t.Errorf("plan_multimodal description still promises a travel-hack saving: %s", d)
	}
}
