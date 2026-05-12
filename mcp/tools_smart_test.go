package mcp

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

func TestSmartToolDefaultAdvertisedSurfaceIsCompact(t *testing.T) {
	oldMode, hadMode := os.LookupEnv("TRVL_MCP_TOOL_MODE")
	if err := os.Unsetenv("TRVL_MCP_TOOL_MODE"); err != nil {
		t.Fatalf("unset TRVL_MCP_TOOL_MODE: %v", err)
	}
	t.Cleanup(func() {
		if hadMode {
			_ = os.Setenv("TRVL_MCP_TOOL_MODE", oldMode)
		} else {
			_ = os.Unsetenv("TRVL_MCP_TOOL_MODE")
		}
	})

	s := NewServer()

	if len(s.tools) > 31 {
		t.Fatalf("compact tool surface should reduce advertised tools by at least 50%%, got %d", len(s.tools))
	}
	if !toolRegistered(s.tools, "travel") {
		t.Fatalf("compact tool surface should advertise the primary travel tool")
	}
	if toolRegistered(s.tools, "search_flights") {
		t.Fatalf("legacy search_flights should remain callable but not advertised in compact mode")
	}
	if _, ok := s.handlers["search_flights"]; !ok {
		t.Fatalf("legacy search_flights handler should remain registered for compatibility")
	}
	if _, ok := s.handlers["travel"]; !ok {
		t.Fatalf("travel handler should be registered")
	}
}

func TestLegacyToolModeStillAdvertisesLegacySurface(t *testing.T) {
	t.Setenv("TRVL_MCP_TOOL_MODE", "legacy")

	s := NewServer()

	if len(s.tools) != 62 {
		t.Fatalf("legacy tool mode should advertise 62 legacy tools, got %d", len(s.tools))
	}
	if !toolRegistered(s.tools, "search_flights") {
		t.Fatalf("legacy mode should advertise search_flights")
	}
	if toolRegistered(s.tools, "travel") {
		t.Fatalf("legacy mode should not add a new advertised tool to the legacy surface")
	}
	if _, ok := s.handlers["travel"]; !ok {
		t.Fatalf("travel handler should still be callable in legacy mode")
	}
}

func TestTravelSmartToolRoutesCoreFamilies(t *testing.T) {
	cases := []struct {
		name   string
		args   map[string]any
		target string
	}{
		{
			name:   "exact legacy flight",
			args:   map[string]any{"intent": "search_flights", "params": map[string]any{"sentinel": "ok"}},
			target: "search_flights",
		},
		{
			name:   "natural flight",
			args:   map[string]any{"query": "find flights from HEL to LHR", "params": map[string]any{"sentinel": "ok"}},
			target: "search_flights",
		},
		{
			name:   "hotel",
			args:   map[string]any{"query": "hotels in Tokyo", "params": map[string]any{"sentinel": "ok"}},
			target: "search_hotels",
		},
		{
			name:   "ground",
			args:   map[string]any{"query": "train from Amsterdam to Paris", "params": map[string]any{"sentinel": "ok"}},
			target: "search_ground",
		},
		{
			name:   "trip planning",
			args:   map[string]any{"intent": "trip planning", "params": map[string]any{"sentinel": "ok"}},
			target: "plan_trip",
		},
		{
			name:   "watch list",
			args:   map[string]any{"intent": "watches", "action": "list", "params": map[string]any{"sentinel": "ok"}},
			target: "list_watches",
		},
		{
			name:   "preferences read",
			args:   map[string]any{"query": "show travel preferences", "params": map[string]any{"sentinel": "ok"}},
			target: "get_preferences",
		},
		{
			name:   "provider health",
			args:   map[string]any{"query": "provider health", "params": map[string]any{"sentinel": "ok"}},
			target: "provider_health",
		},
		{
			name:   "visa",
			args:   map[string]any{"query": "visa requirements for FI passport to TH", "params": map[string]any{"sentinel": "ok"}},
			target: "check_visa",
		},
		{
			name:   "optimize trip dates",
			args:   map[string]any{"query": "optimize trip dates for HEL to BCN", "params": map[string]any{"sentinel": "ok"}},
			target: "optimize_trip_dates",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{handlers: map[string]ToolHandler{}}
			s.handlers[tc.target] = func(ctx context.Context, args map[string]any, elicit ElicitFunc, sampling SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
				if got := argString(args, "sentinel"); got != "ok" {
					t.Fatalf("forwarded params sentinel = %q, want ok", got)
				}
				return []ContentBlock{{Type: "text", Text: "stub result"}}, map[string]any{"core_field": tc.target}, nil
			}

			content, structured, err := s.handleTravel(context.Background(), tc.args, nil, nil, nil)
			if err != nil {
				t.Fatalf("handleTravel returned error: %v", err)
			}
			if len(content) == 0 {
				t.Fatalf("expected forwarded content")
			}

			var got map[string]any
			data, err := json.Marshal(structured)
			if err != nil {
				t.Fatalf("marshal structured: %v", err)
			}
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("unmarshal structured: %v", err)
			}
			if got["dispatched_to"] != tc.target {
				t.Fatalf("dispatched_to = %v, want %s", got["dispatched_to"], tc.target)
			}
			result, ok := got["result"].(map[string]any)
			if !ok {
				t.Fatalf("structured result should include nested result, got %#v", got["result"])
			}
			if result["core_field"] != tc.target {
				t.Fatalf("nested result core_field = %v, want %s", result["core_field"], tc.target)
			}
		})
	}
}

func TestTravelSmartToolRoutesStatefulActions(t *testing.T) {
	cases := []struct {
		name   string
		args   map[string]any
		target string
	}{
		{
			name:   "create watch",
			args:   map[string]any{"intent": "watches", "action": "create", "params": map[string]any{"sentinel": "ok"}},
			target: "watch_price",
		},
		{
			name:   "update preferences",
			args:   map[string]any{"intent": "preferences", "action": "update", "params": map[string]any{"sentinel": "ok"}},
			target: "update_preferences",
		},
		{
			name:   "configure provider",
			args:   map[string]any{"intent": "providers", "action": "configure", "params": map[string]any{"sentinel": "ok"}},
			target: "configure_provider",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{handlers: map[string]ToolHandler{}}
			s.handlers[tc.target] = func(ctx context.Context, args map[string]any, elicit ElicitFunc, sampling SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
				if got := argString(args, "sentinel"); got != "ok" {
					t.Fatalf("forwarded params sentinel = %q, want ok", got)
				}
				return []ContentBlock{{Type: "text", Text: "stub result"}}, map[string]any{"core_field": tc.target}, nil
			}

			_, structured, err := s.handleTravel(context.Background(), tc.args, nil, nil, nil)
			if err != nil {
				t.Fatalf("handleTravel returned error: %v", err)
			}

			var got map[string]any
			data, err := json.Marshal(structured)
			if err != nil {
				t.Fatalf("marshal structured: %v", err)
			}
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("unmarshal structured: %v", err)
			}
			if got["dispatched_to"] != tc.target {
				t.Fatalf("dispatched_to = %v, want %s", got["dispatched_to"], tc.target)
			}
		})
	}
}

func toolRegistered(tools []ToolDef, name string) bool {
	for _, tool := range tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}
