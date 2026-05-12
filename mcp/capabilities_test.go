package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/testutil"
)

// --- Progress notification tests ---

// TestProgressNotifications_SearchRoute verifies that search_route emits
// notifications/progress notifications during a (mocked) run.
// In short mode we just verify the progress func mechanics without network I/O.
func TestProgressNotifications_SendProgress(t *testing.T) {
	t.Parallel()
	s := NewServer()

	// Set up a notification writer so makeProgressFunc returns non-nil.
	var buf bytes.Buffer
	s.notifyWriter = &buf

	token := "test-token-1"
	progress := s.makeProgressFunc(token)
	if progress == nil {
		t.Fatal("expected non-nil ProgressFunc when notifyWriter is set")
	}

	// Call progress and verify JSON is written.
	progress(50, 100, "halfway there")

	output := buf.String()
	if output == "" {
		t.Fatal("expected notification output, got empty")
	}

	var notif map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &notif); err != nil {
		t.Fatalf("unmarshal notification: %v — raw: %q", err, output)
	}
	if notif["method"] != "notifications/progress" {
		t.Errorf("method: got %q, want notifications/progress", notif["method"])
	}
	params, _ := notif["params"].(map[string]interface{})
	if params == nil {
		t.Fatal("expected params object")
	}
	if params["progressToken"] != token {
		t.Errorf("progressToken: got %v, want %q", params["progressToken"], token)
	}
	if params["progress"] != float64(50) {
		t.Errorf("progress: got %v, want 50", params["progress"])
	}
	if params["message"] != "halfway there" {
		t.Errorf("message: got %v, want \"halfway there\"", params["message"])
	}
}

// TestProgressNotifications_NilSafe verifies that sendProgress with nil func does not panic.
func TestProgressNotifications_NilSafe(t *testing.T) {
	t.Parallel()
	// Must not panic.
	sendProgress(nil, 10, 100, "test")
}

// TestProgressNotifications_NoWriterReturnsNil verifies makeProgressFunc returns nil
// when no notifyWriter is set (HTTP transport simulation).
func TestProgressNotifications_NoWriterReturnsNil(t *testing.T) {
	t.Parallel()
	s := NewServer()
	// notifyWriter is nil by default.
	progress := s.makeProgressFunc("token")
	if progress != nil {
		t.Error("expected nil ProgressFunc when notifyWriter is not set")
	}
}

// --- search_natural tests ---

// TestSearchNaturalTool_Registered verifies the legacy tool still appears when
// the compatibility surface is explicitly requested.
func TestSearchNaturalTool_Registered(t *testing.T) {
	t.Setenv("TRVL_MCP_TOOL_MODE", "legacy")
	s := NewServer()
	resp := sendRequest(t, s, "tools/list", 1, nil)
	if resp == nil || resp.Error != nil {
		t.Fatalf("tools/list failed: %v", resp)
	}

	var result ToolsListResult
	raw, _ := json.Marshal(resp.Result)
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal tools/list result: %v", err)
	}

	found := false
	for _, tool := range result.Tools {
		if tool.Name == "search_natural" {
			found = true
			if tool.Description == "" {
				t.Error("search_natural has empty description")
			}
			break
		}
	}
	if !found {
		t.Error("search_natural not found in tools/list")
	}
}

// TestSearchNatural_EmptyQuery returns an error for missing query.
func TestSearchNatural_EmptyQuery(t *testing.T) {
	t.Parallel()
	_, _, err := handleSearchNatural(context.Background(), map[string]any{"query": ""}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for empty query, got nil")
	}
}

// TestSearchNatural_MissingQuery returns an error for absent query key.
func TestSearchNatural_MissingQuery(t *testing.T) {
	t.Parallel()
	_, _, err := handleSearchNatural(context.Background(), map[string]any{}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for missing query, got nil")
	}
}

// TestSearchNatural_HeuristicWeekend verifies heuristicParse resolves "next weekend".
func TestSearchNatural_HeuristicWeekend(t *testing.T) {
	t.Parallel()
	// Monday 2026-01-05.
	p := heuristicParse("hotels next weekend in Prague", "2026-01-05")
	if p.Intent != "hotel" {
		t.Errorf("intent: got %q, want hotel", p.Intent)
	}
	// "next weekend" from Monday 2026-01-05 = Saturday 2026-01-10.
	if p.CheckIn != "2026-01-10" {
		t.Errorf("check_in: got %q, want 2026-01-10", p.CheckIn)
	}
	if p.CheckOut != "2026-01-12" {
		t.Errorf("check_out: got %q, want 2026-01-12", p.CheckOut)
	}
}

// TestSearchNatural_HeuristicFlight verifies intent detection for flight queries.
func TestSearchNatural_HeuristicFlight(t *testing.T) {
	t.Parallel()
	p := heuristicParse("cheapest flight from Helsinki to Tokyo next month", "2026-01-01")
	if p.Intent != "flight" {
		t.Errorf("intent: got %q, want flight", p.Intent)
	}
}

// TestSearchNatural_HeuristicFlight2 verifies "flying" keyword maps to flight.
func TestSearchNatural_HeuristicFlight2(t *testing.T) {
	t.Parallel()
	p := heuristicParse("I am flying from HEL to PRG on Friday", "2026-01-01")
	if p.Intent != "flight" {
		t.Errorf("intent: got %q, want flight", p.Intent)
	}
}

// TestSearchNatural_HeuristicRoute verifies default route intent for train query.
func TestSearchNatural_HeuristicRoute(t *testing.T) {
	t.Parallel()
	// Avoid any hotel/flight keywords in the query.
	p := heuristicParse("how to travel from Helsinki to Dubrovnik by train or bus", "2026-01-01")
	if p.Intent != "route" {
		t.Errorf("intent: got %q, want route", p.Intent)
	}
}

// TestSearchNatural_HeuristicHotel verifies hotel keyword detection.
func TestSearchNatural_HeuristicHotel(t *testing.T) {
	t.Parallel()
	p := heuristicParse("find me a hotel in Prague for 3 nights", "2026-01-01")
	if p.Intent != "hotel" {
		t.Errorf("intent: got %q, want hotel", p.Intent)
	}
}

// TestSearchNatural_SamplingNilFallback verifies heuristic fallback when sampling is nil.
func TestSearchNatural_SamplingNilFallback(t *testing.T) {
	t.Parallel()
	// A query with no recognizable destination should return a fallback message,
	// not panic.
	content, _, err := handleSearchNatural(
		context.Background(),
		map[string]any{"query": "I want to go somewhere interesting"},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(content) == 0 {
		t.Error("expected at least one content block, got none")
	}
}

// --- Resource subscription tests ---

// TestResourcesSubscribe verifies subscribe/unsubscribe round-trip.
func TestResourcesSubscribe(t *testing.T) {
	t.Parallel()
	s := NewServer()
	uri := "trvl://trips/abc123"

	// Subscribe.
	subResp := sendRequest(t, s, "resources/subscribe", 10,
		map[string]string{"uri": uri})
	if subResp == nil || subResp.Error != nil {
		t.Fatalf("subscribe failed: %v", subResp)
	}

	s.subsMu.Lock()
	subscribed := s.subs[uri]
	s.subsMu.Unlock()
	if !subscribed {
		t.Error("expected URI to be in subs after subscribe")
	}

	// Unsubscribe.
	unsubResp := sendRequest(t, s, "resources/unsubscribe", 11,
		map[string]string{"uri": uri})
	if unsubResp == nil || unsubResp.Error != nil {
		t.Fatalf("unsubscribe failed: %v", unsubResp)
	}

	s.subsMu.Lock()
	stillSubscribed := s.subs[uri]
	s.subsMu.Unlock()
	if stillSubscribed {
		t.Error("expected URI to be removed after unsubscribe")
	}
}

// TestSendResourceUpdated_FiresOnlyForSubscribed verifies notifications are only
// sent for URIs that have active subscriptions.
func TestSendResourceUpdated_FiresOnlyForSubscribed(t *testing.T) {
	t.Parallel()
	s := NewServer()
	var buf bytes.Buffer
	s.notifyWriter = &buf

	const subscribedURI = "trvl://trips/sub-trip"
	const unsubscribedURI = "trvl://trips/unsub-trip"

	// Subscribe to one URI only.
	s.subsMu.Lock()
	s.subs[subscribedURI] = true
	s.subsMu.Unlock()

	// Fire for the unsubscribed URI — nothing should be written.
	s.SendResourceUpdated(unsubscribedURI)
	if buf.Len() != 0 {
		t.Errorf("expected no output for unsubscribed URI, got %q", buf.String())
	}

	// Fire for the subscribed URI — should emit a notification.
	s.SendResourceUpdated(subscribedURI)
	if buf.Len() == 0 {
		t.Error("expected notification for subscribed URI, got none")
	}

	var notif map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &notif); err != nil {
		t.Fatalf("unmarshal notification: %v", err)
	}
	if notif["method"] != "notifications/resources/updated" {
		t.Errorf("method: got %v, want notifications/resources/updated", notif["method"])
	}
	params, _ := notif["params"].(map[string]interface{})
	if params["uri"] != subscribedURI {
		t.Errorf("uri in notification: got %v, want %q", params["uri"], subscribedURI)
	}
}

// TestResourcesCapability_Subscribe verifies the server advertises Subscribe: true.
func TestResourcesCapability_Subscribe(t *testing.T) {
	t.Parallel()
	s := NewServer()
	resp := sendRequest(t, s, "initialize", 1, nil)
	if resp == nil || resp.Error != nil {
		t.Fatalf("initialize failed: %v", resp)
	}

	raw, _ := json.Marshal(resp.Result)
	var result InitializeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if result.Capabilities.Resources == nil {
		t.Fatal("expected Resources capability to be set")
	}
	if !result.Capabilities.Resources.Subscribe {
		t.Error("expected Resources.Subscribe = true")
	}
	if !result.Capabilities.Resources.ListChanged {
		t.Error("expected Resources.ListChanged = true")
	}
}

// --- Tool count test update ---

// TestToolsList_IncludesNewTools verifies all newly added tools appear in tools/list.
func TestToolsList_IncludesNewTools(t *testing.T) {
	t.Setenv("TRVL_MCP_TOOL_MODE", "legacy")
	s := NewServer()
	resp := sendRequest(t, s, "tools/list", 1, nil)
	if resp == nil || resp.Error != nil {
		t.Fatalf("tools/list failed: %v", resp)
	}

	var result ToolsListResult
	raw, _ := json.Marshal(resp.Result)
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal tools/list result: %v", err)
	}

	newTools := []string{
		"search_natural",
		"list_trips",
		"get_trip",
		"create_trip",
		"add_trip_leg",
		"mark_trip_booked",
	}
	toolSet := make(map[string]bool)
	for _, tool := range result.Tools {
		toolSet[tool.Name] = true
	}
	for _, name := range newTools {
		if !toolSet[name] {
			t.Errorf("expected tool %q in tools/list, not found", name)
		}
	}
}

// --- Detect hacks progress emission test ---

// TestDetectHacks_ProgressEmitted verifies that handleDetectTravelHacks calls progress.
func TestDetectHacks_ProgressEmitted(t *testing.T) {
	t.Parallel()
	var calls []string
	mockProgress := func(progress, total float64, message string) {
		calls = append(calls, message)
	}

	// Run with short-circuit: origin/destination/date are required.
	// Use dummy values — in short mode the detectors themselves time out or return nothing,
	// but progress should still fire at the pre-call checkpoints.
	testutil.RequireLiveIntegration(t)

	handleDetectTravelHacks(context.Background(), map[string]any{
		"origin":      "HEL",
		"destination": "PRG",
		"date":        "2026-07-15",
	}, nil, nil, mockProgress)

	if len(calls) == 0 {
		t.Error("expected at least one progress call, got none")
	}
	// First message should mention the route.
	if !strings.Contains(calls[0], "HEL") && !strings.Contains(calls[0], "PRG") {
		t.Errorf("first progress message should mention origin/destination, got %q", calls[0])
	}
	// Last message should mention "100" (done) or "hacks".
	last := calls[len(calls)-1]
	if !strings.Contains(last, "100") && !strings.Contains(last, "hacks") && !strings.Contains(last, "Found") {
		t.Errorf("last progress message unexpected: %q", last)
	}
}

// TestSearchRoute_ProgressEmitted verifies that handleSearchRoute calls progress.
func TestSearchRoute_ProgressEmitted(t *testing.T) {
	t.Parallel()
	var calls []float64
	mockProgress := func(progress, total float64, message string) {
		calls = append(calls, progress)
	}

	testutil.RequireLiveIntegration(t)

	handleSearchRoute(context.Background(), map[string]any{
		"origin":      "HEL",
		"destination": "PRG",
		"date":        "2026-07-15",
	}, nil, nil, mockProgress)

	if len(calls) == 0 {
		t.Error("expected progress calls, got none")
	}
	// First call should be 0 and last should be 100.
	if calls[0] != 0 {
		t.Errorf("first progress value: got %v, want 0", calls[0])
	}
	if calls[len(calls)-1] != 100 {
		t.Errorf("last progress value: got %v, want 100", calls[len(calls)-1])
	}
}

// --- Detect hacks progress in unit mode (no network) ---

// TestDetectHacks_ProgressCalls_Unit verifies progress is fired even with missing fields.
func TestDetectHacks_ProgressCalls_Unit(t *testing.T) {
	t.Parallel()
	var count int
	mockProgress := func(progress, total float64, message string) {
		count++
	}

	// Missing origin/dest/date — function will still call sendProgress before ctx cancel.
	// The detectors will run but return no hacks.
	handleDetectTravelHacks(context.Background(), map[string]any{
		"origin":      "",
		"destination": "",
		"date":        "",
	}, nil, nil, mockProgress)

	// Should have called progress at least for the initial "Analysing..." message.
	if count == 0 {
		t.Error("expected at least one progress call even with empty args")
	}
}
