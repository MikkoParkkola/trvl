package main

import (
	"strings"
	"testing"
)

// The planner attaches no travel-hack saving (#824), so its help must not
// promise one.
func TestMultimodalPlanHelp_PromisesNoSavingsAnnotation(t *testing.T) {
	if long := strings.ToLower(multimodalPlanCmd().Long); strings.Contains(long, "annotates") || strings.Contains(long, "travel-hack saving") {
		t.Errorf("trvl multimodal help still promises a travel-hack saving:\n%s", multimodalPlanCmd().Long)
	}
}
