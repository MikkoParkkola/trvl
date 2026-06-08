package mcp

import (
	"context"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/testutil"
)

func TestSearchLoungesTool_Definition(t *testing.T) {
	t.Parallel()
	tool := searchLoungesTool()
	if tool.Name != "search_lounges" {
		t.Errorf("Name = %q, want search_lounges", tool.Name)
	}
	if len(tool.InputSchema.Required) == 0 {
		t.Error("expected required params")
	}
}

func TestHandleSearchLounges_MissingParams(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args map[string]any
	}{
		{"empty", map[string]any{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := handleSearchLounges(context.Background(), tt.args, nil, nil, nil)
			if err == nil {
				t.Error("expected error for missing params")
			}
		})
	}
}

func TestHandleSearchLounges_InvalidIATA(t *testing.T) {
	t.Parallel()
	_, _, err := handleSearchLounges(context.Background(), map[string]any{
		"airport": "INVALID",
	}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for invalid IATA code")
	}
}

func TestHandleSearchLounges_Success(t *testing.T) {
	t.Parallel()
	testutil.RequireLiveProbe(t)
	content, structured, err := handleSearchLounges(context.Background(), map[string]any{
		"airport": "HEL",
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
