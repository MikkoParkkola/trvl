package mcp

import (
	"context"
	"reflect"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/baggage"
)

func TestGetBaggageRulesTool_Definition(t *testing.T) {
	t.Parallel()
	tool := getBaggageRulesTool()
	if tool.Name != "get_baggage_rules" {
		t.Errorf("Name = %q, want get_baggage_rules", tool.Name)
	}
	if len(tool.InputSchema.Required) == 0 {
		t.Error("expected required params")
	}
	if _, ok := tool.InputSchema.Properties["carry_on_only"]; !ok {
		t.Error("expected carry_on_only property in input schema")
	}
}

func TestHandleGetBaggageRules_All(t *testing.T) {
	t.Parallel()
	content, structured, err := handleGetBaggageRules(context.Background(), map[string]any{
		"airline_code": "ALL",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content) == 0 {
		t.Fatal("expected content")
	}
	if structured == nil {
		t.Fatal("expected structured result")
	}
}

func TestHandleGetBaggageRules_ByAirlineCode(t *testing.T) {
	t.Parallel()
	content, structured, err := handleGetBaggageRules(context.Background(), map[string]any{
		"airline_code": "FR",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content) == 0 {
		t.Fatal("expected content")
	}
	if structured == nil {
		t.Fatal("expected structured result")
	}
}

// extractAirlines pulls the "Airlines" field out of the anonymous response
// struct returned by the list/all path, via reflection.
func extractAirlines(t *testing.T, structured interface{}) []baggage.AirlineBaggage {
	t.Helper()
	v := reflect.ValueOf(structured)
	field := v.FieldByName("Airlines")
	if !field.IsValid() {
		t.Fatalf("structured result has no Airlines field: %#v", structured)
	}
	airlines, ok := field.Interface().([]baggage.AirlineBaggage)
	if !ok {
		t.Fatalf("Airlines field is not []baggage.AirlineBaggage: %T", field.Interface())
	}
	return airlines
}

func TestHandleGetBaggageRules_CarryOnOnly_ExcludesOverheadOnly(t *testing.T) {
	t.Parallel()

	// Sanity: the dataset must contain at least one OverheadOnly carrier for
	// this test to be meaningful.
	var overheadCount int
	for _, ab := range baggage.All() {
		if ab.OverheadOnly {
			overheadCount++
		}
	}
	if overheadCount == 0 {
		t.Skip("no OverheadOnly airlines in dataset; filter is a no-op")
	}

	_, structured, err := handleGetBaggageRules(context.Background(), map[string]any{
		"airline_code":  "all",
		"carry_on_only": true,
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	airlines := extractAirlines(t, structured)
	for _, ab := range airlines {
		if ab.OverheadOnly {
			t.Errorf("carry_on_only=true returned OverheadOnly airline %s (%s)", ab.Code, ab.Name)
		}
	}

	wantLen := len(baggage.All()) - overheadCount
	if len(airlines) != wantLen {
		t.Errorf("filtered len = %d, want %d (total %d − %d overhead-only)",
			len(airlines), wantLen, len(baggage.All()), overheadCount)
	}
}

func TestHandleGetBaggageRules_CarryOnOnly_DefaultReturnsFullSet(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"absent", map[string]any{"airline_code": "all"}},
		{"false", map[string]any{"airline_code": "all", "carry_on_only": false}},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, structured, err := handleGetBaggageRules(context.Background(), tc.args, nil, nil, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			airlines := extractAirlines(t, structured)
			if len(airlines) != len(baggage.All()) {
				t.Errorf("len = %d, want full set %d", len(airlines), len(baggage.All()))
			}
		})
	}
}

// TestHandleGetBaggageRules_CarryOnOnly_IgnoredForSingleAirline confirms the
// flag does not alter single-airline lookups, including OverheadOnly carriers.
func TestHandleGetBaggageRules_CarryOnOnly_IgnoredForSingleAirline(t *testing.T) {
	t.Parallel()

	// FR (Ryanair) is a low-cost carrier flagged OverheadOnly; carry_on_only
	// must not suppress a direct lookup of it.
	_, structured, err := handleGetBaggageRules(context.Background(), map[string]any{
		"airline_code":  "FR",
		"carry_on_only": true,
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	v := reflect.ValueOf(structured)
	found := v.FieldByName("Found")
	if !found.IsValid() || !found.Bool() {
		t.Fatalf("expected single-airline lookup to find FR regardless of carry_on_only")
	}
}
