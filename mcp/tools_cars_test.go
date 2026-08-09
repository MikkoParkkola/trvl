package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

func TestSearchCarsToolSchema(t *testing.T) {
	t.Parallel()

	tool := searchCarsTool()
	if tool.Name != "search_cars" {
		t.Fatalf("tool name = %q, want search_cars", tool.Name)
	}
	if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
		t.Fatalf("search_cars should be annotated read-only: %#v", tool.Annotations)
	}
	for _, prop := range []string{"pickup_location", "pickup_date", "dropoff_date", "currency", "passengers"} {
		if _, ok := tool.InputSchema.Properties[prop]; !ok {
			t.Fatalf("missing schema property %q", prop)
		}
	}
}

func TestHandleSearchCars_NoProviderGivesSensibleStatus(t *testing.T) {
	t.Setenv("SKYSCANNER_API_KEY", "")

	content, structured, err := handleSearchCars(context.Background(), map[string]any{
		"pickup_location": "Helsinki Airport",
		"pickup_date":     futureDateAfter(30),
		"dropoff_date":    futureDateAfter(33),
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("handleSearchCars returned error: %v", err)
	}
	result, ok := structured.(*models.CarSearchResult)
	if !ok {
		t.Fatalf("structured = %T, want *models.CarSearchResult", structured)
	}
	if result.Success || result.Count != 0 {
		t.Fatalf("success/count = %v/%d, want false/0", result.Success, result.Count)
	}
	if len(result.ProviderStatuses) != 1 || result.ProviderStatuses[0].FixHintCode != "MISSING_CREDENTIAL" {
		t.Fatalf("provider statuses = %#v", result.ProviderStatuses)
	}
	if len(content) == 0 || !strings.Contains(content[0].Text, "SKYSCANNER_API_KEY") {
		t.Fatalf("content should explain setup requirement: %#v", content)
	}
}

func TestTravelRouterCarsIntent(t *testing.T) {
	t.Parallel()

	s := NewServer()
	target, intent := s.resolveTravelTarget("rental cars", "", "")
	if target != "search_cars" || intent != "cars" {
		t.Fatalf("resolved to %q/%q, want search_cars/cars", target, intent)
	}
	target, _ = s.resolveTravelTarget("search_cars", "", "")
	if target != "search_cars" {
		t.Fatalf("exact search_cars resolved to %q", target)
	}
}
