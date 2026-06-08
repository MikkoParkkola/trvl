package mcp

import (
	"context"
	"testing"
)

func TestOptimizeBookingTool_Definition(t *testing.T) {
	t.Parallel()
	tool := optimizeBookingTool()
	if tool.Name != "optimize_booking" {
		t.Errorf("Name = %q, want optimize_booking", tool.Name)
	}
	if len(tool.InputSchema.Required) == 0 {
		t.Error("expected required params")
	}
}

func TestSuggestDatesTool_Definition(t *testing.T) {
	t.Parallel()
	tool := suggestDatesTool()
	if tool.Name != "suggest_dates" {
		t.Errorf("Name = %q, want suggest_dates", tool.Name)
	}
	if len(tool.InputSchema.Required) == 0 {
		t.Error("expected required params")
	}
}

func TestOptimizeMultiCityTool_Definition(t *testing.T) {
	t.Parallel()
	tool := optimizeMultiCityTool()
	if tool.Name != "optimize_multi_city" {
		t.Errorf("Name = %q, want optimize_multi_city", tool.Name)
	}
	if len(tool.InputSchema.Required) == 0 {
		t.Error("expected required params")
	}
}

// handleOptimizeBooking does not validate missing params at the handler
// level — it passes empty strings to the external API. We only test definition.

func TestHandleSuggestDates_MissingRequiredParams(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args map[string]any
	}{
		{"empty", map[string]any{}},
		{"missing_origin", map[string]any{"destination": "BCN", "target_date": "2026-08-01"}},
		{"missing_destination", map[string]any{"origin": "HEL", "target_date": "2026-08-01"}},
		{"missing_target_date", map[string]any{"origin": "HEL", "destination": "BCN"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := handleSuggestDates(context.Background(), tt.args, nil, nil, nil)
			if err == nil {
				t.Error("expected error for missing params")
			}
		})
	}
}

func TestHandleOptimizeMultiCity_MissingParams(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args map[string]any
	}{
		{"empty", map[string]any{}},
		{"missing_home_airport", map[string]any{"cities": "BCN,ROM", "depart_date": "2026-08-01"}},
		{"missing_cities", map[string]any{"home_airport": "HEL", "depart_date": "2026-08-01"}},
		{"missing_depart_date", map[string]any{"home_airport": "HEL", "cities": "BCN,ROM"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := handleOptimizeMultiCity(context.Background(), tt.args, nil, nil, nil)
			if err == nil {
				t.Error("expected error for missing params")
			}
		})
	}
}
