package main

import (
	"strings"
	"testing"
)

// The planner attaches no travel-hack saving (#824), so its help must not
// promise one. The help must still describe the ranking it does keep, so
// empty or miswired text cannot pass.
func TestMultimodalPlanHelp_PromisesNoSavingsAnnotation(t *testing.T) {
	long := multimodalPlanCmd().Long
	if !strings.Contains(long, "RANKS by that total") {
		t.Fatalf("trvl multimodal help lost its ranking description:\n%s", long)
	}
	if strings.Contains(long, "ANNOTATES any travel-hack saving") {
		t.Errorf("trvl multimodal help still promises a travel-hack saving:\n%s", long)
	}
}
