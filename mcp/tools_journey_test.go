package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

func TestHandleJourney_Success(t *testing.T) {
	args := map[string]any{
		"airport_code":    "HEL",
		"date":            "2026-07-18",
		"departure_time":  "09:40",
		"international":   true,
		"ground_minutes":  float64(30),
		"ground_mode":     "train",
		"origin_walk_min": float64(5),
		"ground_label":    "Train I to Helsinki Airport",
	}
	content, structured, err := handleJourney(context.Background(), args, nil, nil, nil)
	if err != nil {
		t.Fatalf("handleJourney error: %v", err)
	}
	if len(content) == 0 {
		t.Fatal("expected user-facing content")
	}

	data, _ := json.Marshal(structured)
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal schedule: %v", err)
	}
	if got["leave_home_by"] != "06:45" {
		t.Errorf("leave_home_by = %v, want 06:45 (HEL intl 120 buffer + 30 ground + 5 var + 5 walk + 15 safety)", got["leave_home_by"])
	}
	if got["confidence"] != "high" {
		t.Errorf("confidence = %v, want high", got["confidence"])
	}
}

func TestHandleJourney_RequiresGroundChoice(t *testing.T) {
	args := map[string]any{
		"airport_code":   "HEL",
		"date":           "2026-07-18",
		"departure_time": "09:40",
		// missing ground_minutes/ground_mode
	}
	if _, _, err := handleJourney(context.Background(), args, nil, nil, nil); err == nil {
		t.Fatal("expected error when ground option is missing")
	}
}

func TestHandleJourney_BadDate(t *testing.T) {
	args := map[string]any{
		"airport_code":   "HEL",
		"date":           "July 18",
		"departure_time": "09:40",
		"ground_minutes": float64(30),
		"ground_mode":    "train",
	}
	if _, _, err := handleJourney(context.Background(), args, nil, nil, nil); err == nil {
		t.Fatal("expected error on unparseable date")
	}
}

// TestPlanJourney_CallableViaIntent_NotAdvertised verifies plan_journey is a
// reachable capability (registered handler) but not part of the advertised
// legacy compatibility-alias surface in either mode.
func TestPlanJourney_CallableViaIntent_NotAdvertised(t *testing.T) {
	s := NewServer()
	if _, ok := s.handlers["plan_journey"]; !ok {
		t.Fatal("plan_journey handler must be registered (callable via travel intent)")
	}
	if toolRegistered(s.tools, "plan_journey") {
		t.Error("plan_journey must NOT be advertised in the default compact surface")
	}

	t.Setenv("TRVL_MCP_TOOL_MODE", "legacy")
	legacy := NewServer()
	if toolRegistered(legacy.tools, "plan_journey") {
		t.Error("plan_journey must NOT be advertised among the legacy compatibility aliases")
	}
}
