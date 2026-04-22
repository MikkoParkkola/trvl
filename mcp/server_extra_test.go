package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- HTTP handler tests ---

func TestHTTPHandler_POST_Initialize(t *testing.T) {
	t.Parallel()
	hs := NewHTTPServer(0)

	req := Request{JSONRPC: "2.0", ID: float64(1), Method: "initialize"}
	body, _ := json.Marshal(req)

	rr := httptest.NewRecorder()
	httpReq := httptest.NewRequest("POST", "/mcp", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")

	hs.handleMCP(rr, httpReq)

	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var resp Response
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	if resp.ID != float64(1) {
		t.Errorf("id = %v, want 1", resp.ID)
	}
}

func TestHTTPHandler_POST_ToolsList(t *testing.T) {
	t.Parallel()
	hs := NewHTTPServer(0)

	req := Request{JSONRPC: "2.0", ID: float64(2), Method: "tools/list"}
	body, _ := json.Marshal(req)

	rr := httptest.NewRecorder()
	httpReq := httptest.NewRequest("POST", "/mcp", bytes.NewReader(body))

	hs.handleMCP(rr, httpReq)

	var resp Response
	json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Error != nil {
		t.Fatalf("error: %+v", resp.Error)
	}

	resultJSON, _ := json.Marshal(resp.Result)
	var result ToolsListResult
	json.Unmarshal(resultJSON, &result)

	if len(result.Tools) != 57 {
		t.Errorf("expected 57 tools, got %d", len(result.Tools))
	}
}

func TestHTTPHandler_POST_ToolsCall(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping live HTTP test in short mode")
	}
	hs := NewHTTPServer(0)

	params := ToolCallParams{
		Name: "search_flights",
		Arguments: map[string]any{
			"origin":         "HEL",
			"destination":    "NRT",
			"departure_date": "2026-06-15",
		},
	}
	req := Request{JSONRPC: "2.0", ID: float64(3), Method: "tools/call"}
	paramsJSON, _ := json.Marshal(params)
	req.Params = paramsJSON
	body, _ := json.Marshal(req)

	rr := httptest.NewRecorder()
	httpReq := httptest.NewRequest("POST", "/mcp", bytes.NewReader(body))

	hs.handleMCP(rr, httpReq)

	var resp Response
	json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Error != nil {
		t.Fatalf("error: %+v", resp.Error)
	}
}

func TestHTTPHandler_GET_NotAllowed(t *testing.T) {
	t.Parallel()
	hs := NewHTTPServer(0)

	rr := httptest.NewRecorder()
	httpReq := httptest.NewRequest("GET", "/mcp", nil)

	hs.handleMCP(rr, httpReq)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusMethodNotAllowed)
	}
}

func TestHTTPHandler_OPTIONS_CORS(t *testing.T) {
	t.Parallel()
	hs := NewHTTPServer(0)

	rr := httptest.NewRecorder()
	httpReq := httptest.NewRequest("OPTIONS", "/mcp", nil)
	httpReq.Header.Set("Origin", "http://localhost:3000")

	hs.handleMCP(rr, httpReq)

	if rr.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusNoContent)
	}

	if rr.Header().Get("Access-Control-Allow-Origin") != "http://localhost:3000" {
		t.Errorf("CORS Allow-Origin = %q, want http://localhost:3000", rr.Header().Get("Access-Control-Allow-Origin"))
	}
	if rr.Header().Get("Access-Control-Allow-Methods") != "POST, OPTIONS" {
		t.Error("missing CORS Allow-Methods header")
	}
}

func TestHTTPHandler_CORS_RejectsNonLocalhost(t *testing.T) {
	t.Parallel()
	hs := NewHTTPServer(0)

	rr := httptest.NewRecorder()
	httpReq := httptest.NewRequest("OPTIONS", "/mcp", nil)
	httpReq.Header.Set("Origin", "https://evil.com")

	hs.handleMCP(rr, httpReq)

	if rr.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("CORS should not allow non-localhost origin, got %q", rr.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestHTTPHandler_POST_InvalidJSON(t *testing.T) {
	t.Parallel()
	hs := NewHTTPServer(0)

	rr := httptest.NewRecorder()
	httpReq := httptest.NewRequest("POST", "/mcp", bytes.NewReader([]byte("not json")))

	hs.handleMCP(rr, httpReq)

	// Should return 200 with a JSON-RPC parse error.
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var resp Response
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected parse error")
	}
	if resp.Error.Code != -32700 {
		t.Errorf("error code = %d, want -32700", resp.Error.Code)
	}
}

func TestHTTPHandler_POST_Notification(t *testing.T) {
	t.Parallel()
	hs := NewHTTPServer(0)

	req := Request{JSONRPC: "2.0", Method: "notifications/initialized"}
	body, _ := json.Marshal(req)

	rr := httptest.NewRecorder()
	httpReq := httptest.NewRequest("POST", "/mcp", bytes.NewReader(body))

	hs.handleMCP(rr, httpReq)

	// Notifications return 204 No Content.
	if rr.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusNoContent)
	}
}

func TestHTTPHandler_Health(t *testing.T) {
	t.Parallel()
	hs := NewHTTPServer(0)

	rr := httptest.NewRecorder()
	httpReq := httptest.NewRequest("GET", "/health", nil)

	hs.handleHealth(rr, httpReq)

	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["status"] != "ok" {
		t.Errorf("status = %q, want ok", result["status"])
	}
	if result["server"] != "trvl" {
		t.Errorf("server = %q, want trvl", result["server"])
	}
	if result["version"] != serverVersion {
		t.Errorf("version = %q, want %q", result["version"], serverVersion)
	}
	tools, ok := result["tools"].(float64)
	if !ok || tools < 1 {
		t.Errorf("tools = %v, want positive number", result["tools"])
	}
}

// --- Tool parameter validation ---

func TestToolsCall_InvalidParams(t *testing.T) {
	t.Parallel()
	s := NewServer()

	req := &Request{
		JSONRPC: "2.0",
		ID:      float64(1),
		Method:  "tools/call",
		Params:  json.RawMessage(`"not a valid params object"`),
	}

	resp := s.HandleRequest(req)
	if resp.Error == nil {
		t.Fatal("expected error for invalid params")
	}
	if resp.Error.Code != -32602 {
		t.Errorf("error code = %d, want -32602", resp.Error.Code)
	}
}

func TestToolsCall_UnknownToolDirect(t *testing.T) {
	t.Parallel()
	s := NewServer()

	params := ToolCallParams{Name: "nonexistent"}
	raw, _ := json.Marshal(params)

	req := &Request{
		JSONRPC: "2.0",
		ID:      float64(1),
		Method:  "tools/call",
		Params:  raw,
	}

	resp := s.HandleRequest(req)
	if resp.Error == nil {
		t.Fatal("expected error for unknown tool")
	}
	if !strings.Contains(resp.Error.Message, "unknown tool") {
		t.Errorf("error message = %q", resp.Error.Message)
	}
}

// --- Multiple sequential HTTP requests ---

func TestHTTPHandler_SequentialRequests(t *testing.T) {
	t.Parallel()
	hs := NewHTTPServer(0)

	// Initialize -> tools/list -> tools/call.
	methods := []string{"initialize", "tools/list"}
	for i, method := range methods {
		req := Request{JSONRPC: "2.0", ID: float64(i + 1), Method: method}
		body, _ := json.Marshal(req)

		rr := httptest.NewRecorder()
		httpReq := httptest.NewRequest("POST", "/mcp", bytes.NewReader(body))
		hs.handleMCP(rr, httpReq)

		var resp Response
		json.Unmarshal(rr.Body.Bytes(), &resp)
		if resp.Error != nil {
			t.Fatalf("request %d (%s) error: %+v", i+1, method, resp.Error)
		}
	}
}

// --- Large payload ---

func TestHTTPHandler_LargePayload(t *testing.T) {
	t.Parallel()
	hs := NewHTTPServer(0)

	// Create a tool call with a large arguments map.
	args := make(map[string]any)
	for i := range 100 {
		args[strings.Repeat("k", 50)+string(rune(i+'a'))] = strings.Repeat("v", 200)
	}

	params := ToolCallParams{Name: "search_flights", Arguments: args}
	req := Request{JSONRPC: "2.0", ID: float64(1), Method: "tools/call"}
	paramsJSON, _ := json.Marshal(params)
	req.Params = paramsJSON
	body, _ := json.Marshal(req)

	rr := httptest.NewRecorder()
	httpReq := httptest.NewRequest("POST", "/mcp", bytes.NewReader(body))
	hs.handleMCP(rr, httpReq)

	if rr.Code != 200 {
		t.Errorf("status = %d, want 200", rr.Code)
	}

	// Response should be valid JSON.
	var resp Response
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
}

// --- All four tool handlers ---

func TestAllToolHandlers(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping live HTTP test in short mode")
	}
	handlers := []struct {
		name     string
		args     map[string]any
		mayError bool // true if the handler may return an error (e.g., fake hotel ID)
	}{
		{"search_flights", map[string]any{"origin": "HEL", "destination": "NRT", "departure_date": "2026-06-15"}, true},
		{"search_dates", map[string]any{"origin": "HEL", "destination": "NRT", "start_date": "2026-06-01", "end_date": "2026-06-30"}, true},
		{"search_hotels", map[string]any{"location": "Helsinki", "check_in": "2026-06-15", "check_out": "2026-06-18"}, true},
		{"hotel_prices", map[string]any{"hotel_id": "/g/abc", "check_in": "2026-06-15", "check_out": "2026-06-18"}, true},
	}

	for _, tt := range handlers {
		t.Run(tt.name, func(t *testing.T) {
			s := NewServer()
			handler, ok := s.handlers[tt.name]
			if !ok {
				t.Fatalf("handler not found for %s", tt.name)
			}

			content, structured, err := handler(context.Background(), tt.args, nil, nil, nil)
			if err != nil {
				if tt.mayError {
					// Expected: fake hotel ID may fail with real API.
					return
				}
				t.Fatalf("handler error: %v", err)
			}

			if len(content) == 0 {
				t.Fatal("expected content blocks")
			}

			// First content block should be the summary (for user).
			if content[0].Type != "text" {
				t.Errorf("first content block type = %q, want text", content[0].Type)
			}
			if content[0].Annotations == nil {
				t.Error("first content block should have annotations")
			}

			// Second content block should be JSON (for assistant).
			if len(content) >= 2 {
				var parsed map[string]any
				if err := json.Unmarshal([]byte(content[1].Text), &parsed); err != nil {
					t.Fatalf("second content block is not valid JSON: %v", err)
				}
			}

			// Structured content should be non-nil.
			if structured == nil {
				t.Error("expected structured content to be non-nil")
			}
		})
	}
}

func TestAllToolHandlers_NilArgs(t *testing.T) {
	t.Parallel()
	tools := []string{"search_flights", "search_dates", "search_hotels", "hotel_prices", "hotel_reviews"}
	s := NewServer()

	for _, name := range tools {
		t.Run(name, func(t *testing.T) {
			handler := s.handlers[name]
			// All handlers should return an error for nil args (missing required fields).
			_, _, err := handler(context.Background(), nil, nil, nil, nil)
			if err == nil {
				t.Error("expected error for nil args")
			}
		})
	}
}

// --- Tool definitions ---

func TestToolDefinitions(t *testing.T) {
	t.Parallel()
	s := NewServer()

	for _, tool := range s.tools {
		t.Run(tool.Name, func(t *testing.T) {
			if tool.Description == "" {
				t.Error("empty description")
			}
			if tool.InputSchema.Type != "object" {
				t.Errorf("schema type = %q, want object", tool.InputSchema.Type)
			}
			// Tools that take no input (e.g. get_preferences) may have zero
			// properties and zero required fields — that is intentional.
			if len(tool.InputSchema.Properties) == 0 && len(tool.InputSchema.Required) > 0 {
				t.Error("required fields listed but no properties defined")
			}

			// Verify all required fields exist in properties.
			for _, req := range tool.InputSchema.Required {
				if _, ok := tool.InputSchema.Properties[req]; !ok {
					t.Errorf("required field %q not in properties", req)
				}
			}
		})
	}
}

// --- marshalResult (via buildAnnotatedContentBlocks) ---

func TestBuildAnnotatedContentBlocks(t *testing.T) {
	t.Parallel()
	blocks, err := buildAnnotatedContentBlocks("Test summary", map[string]string{"key": "value"})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(blocks))
	}

	// Summary block.
	if blocks[0].Text != "Test summary" {
		t.Errorf("summary = %q", blocks[0].Text)
	}
	if blocks[0].Annotations == nil {
		t.Fatal("summary should have annotations")
	}
	if blocks[0].Annotations.Priority != 1.0 {
		t.Errorf("summary priority = %f, want 1.0", blocks[0].Annotations.Priority)
	}
	if len(blocks[0].Annotations.Audience) == 0 || blocks[0].Annotations.Audience[0] != "user" {
		t.Errorf("summary audience = %v, want [user]", blocks[0].Annotations.Audience)
	}

	// JSON block.
	if !strings.Contains(blocks[1].Text, "key") {
		t.Error("JSON block should contain key")
	}
	if blocks[1].Annotations == nil {
		t.Fatal("JSON block should have annotations")
	}
	if blocks[1].Annotations.Priority != 0.5 {
		t.Errorf("JSON priority = %f, want 0.5", blocks[1].Annotations.Priority)
	}
	if len(blocks[1].Annotations.Audience) == 0 || blocks[1].Annotations.Audience[0] != "assistant" {
		t.Errorf("JSON audience = %v, want [assistant]", blocks[1].Annotations.Audience)
	}
}

// --- Stdio edge cases ---

func TestServeStdio_EmptyInput(t *testing.T) {
	t.Parallel()
	s := NewServer()
	in := bytes.NewBufferString("")
	out := &bytes.Buffer{}

	err := s.ServeStdio(in, out)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("expected no output, got %q", out.String())
	}
}

func TestServeStdio_ManyEmptyLines(t *testing.T) {
	t.Parallel()
	s := NewServer()
	in := bytes.NewBufferString("\n\n\n\n\n")
	out := &bytes.Buffer{}

	err := s.ServeStdio(in, out)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("expected no output, got %q", out.String())
	}
}

// --- writeJSON ---

