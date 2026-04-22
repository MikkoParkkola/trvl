package mcp

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestWriteJSON(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	resp := Response{JSONRPC: "2.0", ID: float64(1)}
	err := writeJSON(&buf, resp)
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	line := strings.TrimSpace(buf.String())
	var parsed Response
	if err := json.Unmarshal([]byte(line), &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.JSONRPC != "2.0" {
		t.Errorf("jsonrpc = %q", parsed.JSONRPC)
	}
}

func TestWriteJSON_Error(t *testing.T) {
	t.Parallel()
	// Write to a writer that always fails.
	w := &failWriter{}
	err := writeJSON(w, Response{JSONRPC: "2.0"})
	if err == nil {
		t.Error("expected error from failing writer")
	}
}

type failWriter struct{}

func (f *failWriter) Write(p []byte) (int, error) {
	return 0, io.ErrClosedPipe
}

// --- NewServer ---

func TestNewServer(t *testing.T) {
	t.Parallel()
	s := NewServer()
	if s == nil {
		t.Fatal("NewServer returned nil")
	}
	if len(s.tools) != 57 {
		t.Errorf("expected 57 tools, got %d", len(s.tools))
	}
	if len(s.handlers) != 57 {
		t.Errorf("expected 57 handlers, got %d", len(s.handlers))
	}
}

// --- NewHTTPServer ---

func TestNewHTTPServer(t *testing.T) {
	t.Parallel()
	hs := NewHTTPServer(8080)
	if hs == nil {
		t.Fatal("NewHTTPServer returned nil")
	}
	if hs.port != 8080 {
		t.Errorf("port = %d, want 8080", hs.port)
	}
	if hs.server == nil {
		t.Error("server is nil")
	}
}

// --- HandleRequest all methods ---

func TestHandleRequest_AllMethods(t *testing.T) {
	t.Parallel()
	s := NewServer()

	tests := []struct {
		method    string
		expectNil bool
		expectErr bool
	}{
		{"initialize", false, false},
		{"tools/list", false, false},
		{"notifications/initialized", true, false},
		{"unknown/method", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			req := &Request{JSONRPC: "2.0", ID: float64(1), Method: tt.method}
			if tt.method == "tools/call" {
				params := ToolCallParams{Name: "search_flights"}
				raw, _ := json.Marshal(params)
				req.Params = raw
			}

			resp := s.HandleRequest(req)
			if tt.expectNil {
				if resp != nil {
					t.Error("expected nil response")
				}
				return
			}
			if resp == nil {
				t.Fatal("expected non-nil response")
			}
			if tt.expectErr && resp.Error == nil {
				t.Error("expected error")
			}
			if !tt.expectErr && resp.Error != nil {
				t.Errorf("unexpected error: %+v", resp.Error)
			}
		})
	}
}

// --- Protocol version 2025-11-25 features ---

func TestProtocolVersion(t *testing.T) {
	t.Parallel()
	if protocolVersion != "2025-11-25" {
		t.Errorf("protocol version = %q, want 2025-11-25", protocolVersion)
	}
}

func TestInitializeCapabilities(t *testing.T) {
	t.Parallel()
	s := NewServer()
	resp := sendRequest(t, s, "initialize", 1, nil)
	if resp == nil {
		t.Fatal("expected response")
	}

	resultJSON, _ := json.Marshal(resp.Result)
	var result InitializeResult
	if err := json.Unmarshal(resultJSON, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if result.Capabilities.Tools == nil {
		t.Error("tools capability should be set")
	}
	if result.Capabilities.Prompts == nil {
		t.Error("prompts capability should be set")
	}
	if result.Capabilities.Resources == nil {
		t.Error("resources capability should be set")
	}
	if result.Capabilities.Logging == nil {
		t.Error("logging capability should be set")
	}
	if result.ProtocolVersion != "2025-11-25" {
		t.Errorf("protocol version = %q, want 2025-11-25", result.ProtocolVersion)
	}
}

func TestToolAnnotations(t *testing.T) {
	t.Parallel()
	// Tools that write to disk — ReadOnlyHint is intentionally false.
	writeTools := map[string]bool{
		"update_preferences":      true,
		"configure_provider":      true,
		"remove_provider":         true,
		"test_provider":           true,
		"watch_room_availability": true,
		"build_profile":           true,
		"add_booking":             true,
		"watch_price":             true,
	}

	// Tools that create new resources on each call — not idempotent.
	nonIdempotentTools := map[string]bool{
		"watch_room_availability": true,
		"add_booking":             true,
		"watch_price":             true,
		"check_watches":           true,
		// find_interactive can trigger elicitation and sampling, whose replies
		// are not reproducible across calls — flag it non-idempotent.
		"find_interactive": true,
	}

	s := NewServer()
	for _, tool := range s.tools {
		t.Run(tool.Name, func(t *testing.T) {
			if tool.Annotations == nil {
				t.Fatal("annotations should be set")
			}
			if tool.Annotations.Title == "" {
				t.Error("title should be set")
			}
			if writeTools[tool.Name] {
				if tool.Annotations.ReadOnlyHint {
					t.Error("readOnlyHint should be false for write tool")
				}
			} else {
				if !tool.Annotations.ReadOnlyHint {
					t.Error("readOnlyHint should be true")
				}
			}
			if nonIdempotentTools[tool.Name] {
				if tool.Annotations.IdempotentHint {
					t.Error("idempotentHint should be false for non-idempotent tool")
				}
			} else {
				if !tool.Annotations.IdempotentHint {
					t.Error("idempotentHint should be true")
				}
			}
		})
	}
}

func TestToolOutputSchema(t *testing.T) {
	t.Parallel()
	s := NewServer()
	for _, tool := range s.tools {
		t.Run(tool.Name, func(t *testing.T) {
			if tool.OutputSchema == nil {
				t.Fatal("outputSchema should be set")
			}
			// Should be a valid JSON-serializable object.
			data, err := json.Marshal(tool.OutputSchema)
			if err != nil {
				t.Fatalf("marshal outputSchema: %v", err)
			}
			var schema map[string]interface{}
			if err := json.Unmarshal(data, &schema); err != nil {
				t.Fatalf("outputSchema is not a valid JSON object: %v", err)
			}
			if schema["type"] != "object" {
				t.Errorf("outputSchema type = %v, want object", schema["type"])
			}
		})
	}
}

func TestToolTitle(t *testing.T) {
	t.Parallel()
	s := NewServer()
	for _, tool := range s.tools {
		t.Run(tool.Name, func(t *testing.T) {
			if tool.Title == "" {
				t.Error("tool-level title should be set")
			}
		})
	}
}

func TestStructuredContent(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping live HTTP test in short mode")
	}
	s := NewServer()

	// Call a tool via HandleRequest and verify structuredContent is present.
	params := ToolCallParams{
		Name: "search_flights",
		Arguments: map[string]any{
			"origin":         "HEL",
			"destination":    "NRT",
			"departure_date": "2026-06-15",
		},
	}
	raw, _ := json.Marshal(params)
	req := &Request{
		JSONRPC: "2.0",
		ID:      float64(1),
		Method:  "tools/call",
		Params:  raw,
	}

	resp := s.HandleRequest(req)
	if resp == nil {
		t.Fatal("expected response")
	}
	if resp.Error != nil {
		t.Fatalf("error: %+v", resp.Error)
	}

	resultJSON, _ := json.Marshal(resp.Result)
	var result ToolCallResult
	if err := json.Unmarshal(resultJSON, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Content should have at least one block (exact count varies by network conditions).
	if len(result.Content) < 1 {
		t.Fatalf("expected at least 1 content block, got %d", len(result.Content))
	}

	// Structured content should be present.
	if result.StructuredContent == nil {
		t.Error("structuredContent should be present")
	}
}

func TestContentAnnotations(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping live HTTP test in short mode")
	}
	s := NewServer()

	params := ToolCallParams{
		Name: "search_flights",
		Arguments: map[string]any{
			"origin":         "HEL",
			"destination":    "NRT",
			"departure_date": "2026-06-15",
		},
	}
	raw, _ := json.Marshal(params)
	req := &Request{
		JSONRPC: "2.0",
		ID:      float64(1),
		Method:  "tools/call",
		Params:  raw,
	}

	resp := s.HandleRequest(req)
	if resp == nil {
		t.Fatal("expected response")
	}

	resultJSON, _ := json.Marshal(resp.Result)
	var result ToolCallResult
	json.Unmarshal(resultJSON, &result)

	if len(result.Content) < 1 {
		t.Fatal("expected at least 1 content block")
	}

	// First block: user audience, high priority.
	first := result.Content[0]
	if first.Annotations == nil {
		t.Fatal("first block should have annotations")
	}
	if len(first.Annotations.Audience) == 0 || first.Annotations.Audience[0] != "user" {
		t.Errorf("first block audience = %v, want [user]", first.Annotations.Audience)
	}
	if first.Annotations.Priority != 1.0 {
		t.Errorf("first block priority = %f, want 1.0", first.Annotations.Priority)
	}

	// Second block: assistant audience, lower priority (skip if only 1 block returned).
	if len(result.Content) >= 2 {
		second := result.Content[1]
		if second.Annotations == nil {
			t.Fatal("second block should have annotations")
		}
		if len(second.Annotations.Audience) == 0 || second.Annotations.Audience[0] != "assistant" {
			t.Errorf("second block audience = %v, want [assistant]", second.Annotations.Audience)
		}
		if second.Annotations.Priority != 0.5 {
			t.Errorf("second block priority = %f, want 0.5", second.Annotations.Priority)
		}
	}
}

func TestHandleToolsCall_PlanTripExecutionFailureSetsIsError(t *testing.T) {
	t.Parallel()
	s := NewServer()

	params := ToolCallParams{
		Name: "plan_trip",
		Arguments: map[string]any{
			"origin":      "HEL",
			"destination": "BCN",
			"depart_date": "2026-07-01",
			"return_date": "2026-07-08",
			"guests":      0,
		},
	}
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}

	resp := s.HandleRequest(&Request{
		JSONRPC: "2.0",
		ID:      float64(2),
		Method:  "tools/call",
		Params:  raw,
	})
	if resp == nil {
		t.Fatal("expected response")
	}
	if resp.Error != nil {
		t.Fatalf("unexpected top-level error: %+v", resp.Error)
	}

	resultJSON, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var result ToolCallResult
	if err := json.Unmarshal(resultJSON, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if !result.IsError {
		t.Fatal("expected isError=true")
	}
	if result.StructuredContent != nil {
		t.Fatalf("structuredContent = %#v, want nil", result.StructuredContent)
	}
	if len(result.Content) != 1 {
		t.Fatalf("content len = %d, want 1", len(result.Content))
	}
	if got := result.Content[0].Text; got != "Trip planning failed: guests must be at least 1" {
		t.Fatalf("content[0].Text = %q, want %q", got, "Trip planning failed: guests must be at least 1")
	}
}

func TestInitializeTracksClientCapabilities(t *testing.T) {
	t.Parallel()
	s := NewServer()

	// Send initialize with elicitation capability.
	initParams := InitializeParams{
		ProtocolVersion: "2025-11-25",
		Capabilities: ClientCapabilities{
			Elicitation: &ElicitationCapability{},
		},
		ClientInfo: ClientInfo{Name: "test-client", Version: "1.0"},
	}
	raw, _ := json.Marshal(initParams)
	req := &Request{
		JSONRPC: "2.0",
		ID:      float64(1),
		Method:  "initialize",
		Params:  raw,
	}

	resp := s.HandleRequest(req)
	if resp == nil {
		t.Fatal("expected response")
	}
	if resp.Error != nil {
		t.Fatalf("error: %+v", resp.Error)
	}

	// Verify client capabilities were stored.
	if s.clientCapabilities.Elicitation == nil {
		t.Error("expected elicitation capability to be stored")
	}
}

func TestSendLog(t *testing.T) {
	t.Parallel()
	s := NewServer()
	var buf bytes.Buffer
	s.notifyWriter = &buf

	s.SendLog("info", "test message")

	line := strings.TrimSpace(buf.String())
	if line == "" {
		t.Fatal("expected log notification")
	}

	var notif map[string]interface{}
	if err := json.Unmarshal([]byte(line), &notif); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if notif["method"] != "notifications/message" {
		t.Errorf("method = %v, want notifications/message", notif["method"])
	}
	params, ok := notif["params"].(map[string]interface{})
	if !ok {
		t.Fatal("params should be an object")
	}
	if params["level"] != "info" {
		t.Errorf("level = %v, want info", params["level"])
	}
	if params["data"] != "test message" {
		t.Errorf("data = %v, want test message", params["data"])
	}
	if params["logger"] != "trvl" {
		t.Errorf("logger = %v, want trvl", params["logger"])
	}
}

func TestSendProgress(t *testing.T) {
	t.Parallel()
	s := NewServer()
	var buf bytes.Buffer
	s.notifyWriter = &buf

	s.SendProgress("token-1", 0.5, 1.0, "Searching...")

	line := strings.TrimSpace(buf.String())
	if line == "" {
		t.Fatal("expected progress notification")
	}

	var notif map[string]interface{}
	if err := json.Unmarshal([]byte(line), &notif); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if notif["method"] != "notifications/progress" {
		t.Errorf("method = %v, want notifications/progress", notif["method"])
	}
	params, ok := notif["params"].(map[string]interface{})
	if !ok {
		t.Fatal("params should be an object")
	}
	if params["progressToken"] != "token-1" {
		t.Errorf("progressToken = %v", params["progressToken"])
	}
	if params["progress"] != 0.5 {
		t.Errorf("progress = %v", params["progress"])
	}
	if params["message"] != "Searching..." {
		t.Errorf("message = %v", params["message"])
	}
}

func TestSendNotification_NilWriter(t *testing.T) {
	t.Parallel()
	s := NewServer()
	// Should not panic or error when no writer is set.
	err := s.SendNotification("notifications/message", LogParams{Level: "info", Logger: "trvl", Data: "test"})
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

func TestMakeElicitFunc_NilWithoutCapability(t *testing.T) {
	t.Parallel()
	s := NewServer()
	// Client does not declare elicitation capability.
	fn := s.makeElicitFunc()
	if fn != nil {
		t.Error("expected nil ElicitFunc when client has no elicitation capability")
	}
}

func TestMakeElicitFunc_NilWithoutWriter(t *testing.T) {
	t.Parallel()
	s := NewServer()
	s.clientCapabilities.Elicitation = &ElicitationCapability{}
	// No writer set.
	fn := s.makeElicitFunc()
	if fn != nil {
		t.Error("expected nil ElicitFunc when no writer is set")
	}
}
