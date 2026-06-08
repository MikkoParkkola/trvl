package mcp

import (
	"context"
	"testing"
)

func TestCheckVisaTool_Definition(t *testing.T) {
	t.Parallel()
	tool := checkVisaTool()
	if tool.Name != "check_visa" {
		t.Errorf("Name = %q, want check_visa", tool.Name)
	}
	if len(tool.InputSchema.Required) == 0 {
		t.Error("expected required params")
	}
}

func TestHandleCheckVisa_Success(t *testing.T) {
	t.Parallel()
	content, structured, err := handleCheckVisa(context.Background(), map[string]any{
		"passport":    "IT",
		"destination": "JP",
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
