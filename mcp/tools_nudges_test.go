package mcp

import (
	"encoding/json"
	"runtime"
	"testing"
)

// TestTravelNudgesRegistered asserts the travel_nudges handler and tool
// definition are wired into the server, mirroring how plan_journey/plan_natural
// are registered but kept out of the advertised legacy surface.
func TestTravelNudgesRegistered(t *testing.T) {
	s := NewServer()

	if _, ok := s.handlers["travel_nudges"]; !ok {
		t.Fatal("travel_nudges handler not registered")
	}
	if _, ok := s.toolDefs["travel_nudges"]; !ok {
		t.Fatal("travel_nudges tool definition not registered")
	}
}

// TestHandleTravelNudgesEmptyStores verifies the handler returns valid,
// non-panicking JSON for empty stores. A fresh HOME makes the watch/trip/prefs
// stores empty, which must degrade to zero nudges rather than erroring.
func TestHandleTravelNudgesEmptyStores(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", dir)
	}

	content, structured, err := handleTravelNudges(t.Context(), nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content) == 0 {
		t.Error("expected content blocks")
	}
	if structured == nil {
		t.Fatal("expected structured output")
	}

	// Structured output must be valid JSON with a nudges array and a count.
	data, err := json.Marshal(structured)
	if err != nil {
		t.Fatalf("marshal structured: %v", err)
	}
	var result struct {
		Nudges []map[string]any `json:"nudges"`
		Count  int              `json:"count"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal structured: %v", err)
	}
	if result.Count != len(result.Nudges) {
		t.Errorf("count = %d, want %d", result.Count, len(result.Nudges))
	}
	if result.Count != 0 {
		t.Errorf("expected 0 nudges for empty stores, got %d", result.Count)
	}
}

// TestTravelNudgesSmartIntent verifies the smart router resolves the "nudges"
// intent alias to the travel_nudges tool.
func TestTravelNudgesSmartIntent(t *testing.T) {
	target, ok := resolveSmartIntentAlias("nudges")
	if !ok {
		t.Fatal("smart intent alias 'nudges' did not resolve")
	}
	if target != "travel_nudges" {
		t.Errorf("alias 'nudges' resolved to %q, want travel_nudges", target)
	}
}

// TestTravelNudgesToolDef checks the tool definition has the required fields.
func TestTravelNudgesToolDef(t *testing.T) {
	tool := travelNudgesTool()
	if tool.Name != "travel_nudges" {
		t.Errorf("Name = %q, want travel_nudges", tool.Name)
	}
	if tool.Description == "" {
		t.Error("Description is empty")
	}
	if tool.InputSchema.Type != "object" {
		t.Errorf("InputSchema.Type = %q, want object", tool.InputSchema.Type)
	}
}
