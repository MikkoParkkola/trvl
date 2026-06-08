package mcp

import (
	"context"
	"testing"
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
