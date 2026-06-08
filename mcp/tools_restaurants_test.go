package mcp

import (
	"context"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/testutil"
)

func TestSearchRestaurantsTool_Definition(t *testing.T) {
	t.Parallel()
	tool := searchRestaurantsTool()
	if tool.Name != "search_restaurants" {
		t.Errorf("Name = %q, want search_restaurants", tool.Name)
	}
	if len(tool.InputSchema.Required) == 0 {
		t.Error("expected required params")
	}
}

func TestHandleSearchRestaurants_MissingParams(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args map[string]any
	}{
		{"empty", map[string]any{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := handleSearchRestaurants(context.Background(), tt.args, nil, nil, nil)
			if err == nil {
				t.Error("expected error for missing params")
			}
		})
	}
}

func TestHandleSearchRestaurants_Success(t *testing.T) {
	t.Parallel()
	testutil.RequireLiveProbe(t)
	content, structured, err := handleSearchRestaurants(context.Background(), map[string]any{
		"location": "Rome, Italy",
		"query":    "pizza",
		"limit":    3,
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
