package mcp

import (
	"context"
	"testing"
)

func TestWatchOpportunitiesTool_Definition(t *testing.T) {
	t.Parallel()
	tool := watchOpportunitiesTool()
	if tool.Name != "watch_opportunities" {
		t.Errorf("Name = %q, want watch_opportunities", tool.Name)
	}
}

func TestListOpportunityWatchesTool_Definition(t *testing.T) {
	t.Parallel()
	tool := listOpportunityWatchesTool()
	if tool.Name != "list_opportunity_watches" {
		t.Errorf("Name = %q, want list_opportunity_watches", tool.Name)
	}
}

func TestHandleWatchOpportunities_Defaults(t *testing.T) {
	t.Parallel()
	// All params are optional; should succeed with defaults.
	content, structured, err := handleWatchOpportunities(context.Background(), map[string]any{}, nil, nil, nil)
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

func TestHandleListOpportunityWatches_Empty(t *testing.T) {
	t.Parallel()
	content, structured, err := handleListOpportunityWatches(context.Background(), map[string]any{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if structured == nil {
		t.Fatal("expected structured result")
	}
	_ = content
}
