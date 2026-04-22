package mcp

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/testutil"
)

// sendRequest writes a JSON-RPC request to the server and returns the response.
func sendRequest(t *testing.T, s *Server, method string, id any, params any) *Response {
	t.Helper()

	req := Request{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
	}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			t.Fatalf("marshal params: %v", err)
		}
		req.Params = raw
	}

	reqBytes, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	in := bytes.NewBuffer(append(reqBytes, '\n'))
	out := &bytes.Buffer{}

	if err := s.ServeStdio(in, out); err != nil {
		t.Fatalf("ServeStdio: %v", err)
	}

	// May contain notifications before the response. Find the response line.
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var resp Response
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			continue
		}
		// A response has an ID or an error (not a notification).
		if resp.ID != nil || resp.Error != nil {
			return &resp
		}
	}

	// If no response found, check the last line.
	last := strings.TrimSpace(lines[len(lines)-1])
	if last == "" {
		return nil
	}
	var resp Response
	if err := json.Unmarshal([]byte(last), &resp); err != nil {
		t.Fatalf("unmarshal response %q: %v", last, err)
	}
	return &resp
}

func TestInitialize(t *testing.T) {
	t.Parallel()
	s := NewServer()
	resp := sendRequest(t, s, "initialize", 1, nil)
	if resp == nil {
		t.Fatal("expected response, got nil")
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	if resp.ID != float64(1) { // JSON numbers decode as float64
		t.Errorf("expected id=1, got %v", resp.ID)
	}

	// Verify the result structure.
	resultJSON, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var result InitializeResult
	if err := json.Unmarshal(resultJSON, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if result.ProtocolVersion != protocolVersion {
		t.Errorf("protocol version: got %q, want %q", result.ProtocolVersion, protocolVersion)
	}
	if result.ProtocolVersion != "2025-11-25" {
		t.Errorf("protocol version should be 2025-11-25, got %q", result.ProtocolVersion)
	}
	if result.ServerInfo.Name != "trvl" {
		t.Errorf("server name: got %q, want %q", result.ServerInfo.Name, "trvl")
	}
	if result.ServerInfo.Version != serverVersion {
		t.Errorf("server version: got %q, want %q", result.ServerInfo.Version, serverVersion)
	}
	if result.Capabilities.Tools == nil {
		t.Error("expected tools capability to be set")
	}
}

func TestToolsList(t *testing.T) {
	t.Parallel()
	s := NewServer()
	resp := sendRequest(t, s, "tools/list", 2, nil)
	if resp == nil {
		t.Fatal("expected response, got nil")
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	resultJSON, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var result ToolsListResult
	if err := json.Unmarshal(resultJSON, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if len(result.Tools) != 57 {
		t.Fatalf("expected 57 tools, got %d", len(result.Tools))
	}

	expected := map[string]bool{
		"search_flights":             false,
		"search_dates":               false,
		"search_hotels":              false,
		"search_hotel_by_name":       false,
		"hotel_prices":               false,
		"hotel_reviews":              false,
		"destination_info":           false,
		"calculate_trip_cost":        false,
		"weekend_getaway":            false,
		"suggest_dates":              false,
		"optimize_multi_city":        false,
		"nearby_places":              false,
		"travel_guide":               false,
		"local_events":               false,
		"search_ground":              false,
		"search_airport_transfers":   false,
		"search_restaurants":         false,
		"search_deals":               false,
		"plan_trip":                  false,
		"search_route":               false,
		"hotel_rooms":                false,
		"watch_room_availability":    false,
		"get_preferences":            false,
		"update_preferences":         false,
		"detect_travel_hacks":        false,
		"detect_accommodation_hacks": false,
		"search_natural":             false,
		"list_trips":                 false,
		"get_trip":                   false,
		"create_trip":                false,
		"add_trip_leg":               false,
		"mark_trip_booked":           false,
		"get_weather":                false,
		"get_baggage_rules":          false,
		"find_trip_window":           false,
		"search_lounges":             false,
		"check_visa":                 false,
		"calculate_points_value":     false,
		"configure_provider":         false,
		"list_providers":             false,
		"remove_provider":            false,
		"suggest_providers":          false,
		"test_provider":              false,
		"optimize_trip_dates":        false,
		"assess_trip":               false,
		"optimize_booking":          false,
		"build_profile":             false,
		"add_booking":               false,
		"interview_trip":            false,
		"onboard_profile":           false,
		"provider_health":           false,
		"watch_price":               false,
		"list_watches":              false,
		"check_watches":             false,
		"export_ics":                false,
		"plan_flight_bundle":        false,
		"find_interactive":          false,
	}
	for _, tool := range result.Tools {
		if _, ok := expected[tool.Name]; !ok {
			t.Errorf("unexpected tool: %s", tool.Name)
		}
		expected[tool.Name] = true

		if tool.Description == "" {
			t.Errorf("tool %s has empty description", tool.Name)
		}
		if tool.InputSchema.Type != "object" {
			t.Errorf("tool %s schema type: got %q, want %q", tool.Name, tool.InputSchema.Type, "object")
		}
		// Tools with no input parameters (e.g. get_preferences) intentionally
		// have zero properties and zero required fields.
		if len(tool.InputSchema.Properties) == 0 && len(tool.InputSchema.Required) > 0 {
			t.Errorf("tool %s has required fields but no properties", tool.Name)
		}
	}

	for name, found := range expected {
		if !found {
			t.Errorf("missing tool: %s", name)
		}
	}
}

func TestToolsCallSearchFlights(t *testing.T) {
	t.Parallel()
	testutil.RequireLiveIntegration(t)
	s := NewServer()
	params := ToolCallParams{
		Name: "search_flights",
		Arguments: map[string]any{
			"origin":         "HEL",
			"destination":    "NRT",
			"departure_date": "2026-05-15",
		},
	}
	resp := sendRequest(t, s, "tools/call", 3, params)
	if resp == nil {
		t.Fatal("expected response, got nil")
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	resultJSON, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var result ToolCallResult
	if err := json.Unmarshal(resultJSON, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if len(result.Content) == 0 {
		t.Fatal("expected content blocks")
	}
	if result.Content[0].Type != "text" {
		t.Errorf("content type: got %q, want %q", result.Content[0].Type, "text")
	}
}

func TestToolsCallSearchDates(t *testing.T) {
	t.Parallel()
	testutil.RequireLiveIntegration(t)
	s := NewServer()
	params := ToolCallParams{
		Name: "search_dates",
		Arguments: map[string]any{
			"origin":      "HEL",
			"destination": "NRT",
			"start_date":  "2026-05-01",
			"end_date":    "2026-05-31",
		},
	}
	resp := sendRequest(t, s, "tools/call", 4, params)
	if resp == nil {
		t.Fatal("expected response, got nil")
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
}

func TestToolsCallSearchHotels(t *testing.T) {
	t.Parallel()
	testutil.RequireLiveIntegration(t)
	s := NewServer()
	params := ToolCallParams{
		Name: "search_hotels",
		Arguments: map[string]any{
			"location":  "Helsinki",
			"check_in":  "2026-05-15",
			"check_out": "2026-05-18",
		},
	}
	resp := sendRequest(t, s, "tools/call", 5, params)
	if resp == nil {
		t.Fatal("expected response, got nil")
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
}

func TestToolsCallHotelPrices(t *testing.T) {
	t.Parallel()
	testutil.RequireLiveIntegration(t)
	s := NewServer()
	params := ToolCallParams{
		Name: "hotel_prices",
		Arguments: map[string]any{
			"hotel_id":  "abc123",
			"check_in":  "2026-05-15",
			"check_out": "2026-05-18",
		},
	}
	resp := sendRequest(t, s, "tools/call", 6, params)
	if resp == nil {
		t.Fatal("expected response, got nil")
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
}

func TestToolsCallUnknownTool(t *testing.T) {
	t.Parallel()
	s := NewServer()
	params := ToolCallParams{
		Name:      "nonexistent",
		Arguments: map[string]any{},
	}
	resp := sendRequest(t, s, "tools/call", 7, params)
	if resp == nil {
		t.Fatal("expected response, got nil")
	}
	if resp.Error == nil {
		t.Fatal("expected error for unknown tool")
	}
	if resp.Error.Code != -32602 {
		t.Errorf("error code: got %d, want %d", resp.Error.Code, -32602)
	}
}

func TestUnknownMethod(t *testing.T) {
	t.Parallel()
	s := NewServer()
	resp := sendRequest(t, s, "unknown/method", 8, nil)
	if resp == nil {
		t.Fatal("expected response, got nil")
	}
	if resp.Error == nil {
		t.Fatal("expected error for unknown method")
	}
	if resp.Error.Code != -32601 {
		t.Errorf("error code: got %d, want %d", resp.Error.Code, -32601)
	}
}

func TestNotificationNoResponse(t *testing.T) {
	t.Parallel()
	s := NewServer()
	// notifications/initialized should produce no response line.
	req := Request{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	}
	reqBytes, _ := json.Marshal(req)
	in := bytes.NewBuffer(append(reqBytes, '\n'))
	out := &bytes.Buffer{}

	if err := s.ServeStdio(in, out); err != nil {
		t.Fatalf("ServeStdio: %v", err)
	}

	if out.Len() != 0 {
		t.Errorf("expected no output for notification, got %q", out.String())
	}
}

func TestParseError(t *testing.T) {
	t.Parallel()
	s := NewServer()
	in := bytes.NewBufferString("not valid json\n")
	out := &bytes.Buffer{}

	if err := s.ServeStdio(in, out); err != nil {
		t.Fatalf("ServeStdio: %v", err)
	}

	line := strings.TrimSpace(out.String())
	var resp Response
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected parse error")
	}
	if resp.Error.Code != -32700 {
		t.Errorf("error code: got %d, want %d", resp.Error.Code, -32700)
	}
}

func TestMultipleRequests(t *testing.T) {
	t.Parallel()
	s := NewServer()

	// Send initialize + tools/list in sequence.
	initReq := Request{JSONRPC: "2.0", ID: float64(1), Method: "initialize"}
	listReq := Request{JSONRPC: "2.0", ID: float64(2), Method: "tools/list"}

	initBytes, _ := json.Marshal(initReq)
	listBytes, _ := json.Marshal(listReq)

	input := string(initBytes) + "\n" + string(listBytes) + "\n"
	in := bytes.NewBufferString(input)
	out := &bytes.Buffer{}

	if err := s.ServeStdio(in, out); err != nil {
		t.Fatalf("ServeStdio: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 response lines, got %d: %q", len(lines), out.String())
	}

	// Find the response with id=1 and id=2.
	var found1, found2 bool
	for _, line := range lines {
		var resp Response
		if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &resp); err != nil {
			continue
		}
		if resp.ID == float64(1) {
			found1 = true
		}
		if resp.ID == float64(2) {
			found2 = true
		}
	}
	if !found1 {
		t.Error("missing response for id=1")
	}
	if !found2 {
		t.Error("missing response for id=2")
	}
}

func TestEmptyLinesIgnored(t *testing.T) {
	t.Parallel()
	s := NewServer()

	req := Request{JSONRPC: "2.0", ID: float64(1), Method: "initialize"}
	reqBytes, _ := json.Marshal(req)

	// Empty lines should be skipped.
	input := "\n\n" + string(reqBytes) + "\n\n"
	in := bytes.NewBufferString(input)
	out := &bytes.Buffer{}

	if err := s.ServeStdio(in, out); err != nil {
		t.Fatalf("ServeStdio: %v", err)
	}

	// Find the response line (skip any notification lines).
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	var foundResp bool
	for _, line := range lines {
		var resp Response
		if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &resp); err != nil {
			continue
		}
		if resp.ID != nil {
			foundResp = true
			if resp.Error != nil {
				t.Errorf("unexpected error: %+v", resp.Error)
			}
		}
	}
	if !foundResp {
		t.Error("expected a response with an ID")
	}
}
