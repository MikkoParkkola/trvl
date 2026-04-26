package mcp

import (
	"context"
	"sync"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

func TestWrapHandler_ConcurrentCallersGetPrivateStructuredContent(t *testing.T) {
	t.Parallel()

	carryOn := true
	checkedBags := 1
	shared := &models.FlightSearchResult{
		Success:  true,
		Count:    1,
		TripType: "one_way",
		Flights: []models.FlightResult{
			{
				Price:               123,
				Currency:            "EUR",
				Warnings:            []string{"Self-connect risk"},
				Legs:                []models.FlightLeg{{AirlineCode: "AY"}},
				CarryOnIncluded:     &carryOn,
				CheckedBagsIncluded: &checkedBags,
			},
		},
	}

	s := NewServer()
	handler := s.wrapHandler("test_tool", func(context.Context, map[string]any, ElicitFunc, SamplingFunc, ProgressFunc) ([]ContentBlock, interface{}, error) {
		return []ContentBlock{{
			Type: "text",
			Text: "ok",
			Annotations: &ContentAnnotation{
				Audience: []string{"user"},
				Priority: 1,
			},
		}}, shared, nil
	})

	const callers = 2
	contents := make([][]ContentBlock, callers)
	results := make([]*models.FlightSearchResult, callers)
	errs := make([]error, callers)

	var wg sync.WaitGroup
	start := make(chan struct{})
	args := map[string]any{
		"origin":         "HEL",
		"destination":    "NRT",
		"departure_date": "2026-06-15",
	}

	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			content, structured, err := handler(context.Background(), args, nil, nil, nil)
			if err != nil {
				errs[idx] = err
				return
			}
			contents[idx] = content
			results[idx] = structured.(*models.FlightSearchResult)
		}(i)
	}

	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d returned error: %v", i, err)
		}
	}

	if results[0] == results[1] {
		t.Fatal("concurrent callers received the same FlightSearchResult pointer")
	}
	if len(contents[0]) == 0 || len(contents[1]) == 0 {
		t.Fatal("concurrent callers should both receive content blocks")
	}
	if &contents[0][0] == &contents[1][0] {
		t.Fatal("concurrent callers reused the same ContentBlock backing storage")
	}
	if contents[0][0].Annotations == contents[1][0].Annotations {
		t.Fatal("concurrent callers reused the same ContentAnnotation pointer")
	}
	if &contents[0][0].Annotations.Audience[0] == &contents[1][0].Annotations.Audience[0] {
		t.Fatal("concurrent callers reused the same annotation audience backing storage")
	}
	if &results[0].Flights[0] == &results[1].Flights[0] {
		t.Fatal("concurrent callers reused the same FlightResult backing storage")
	}
	if &results[0].Flights[0].Warnings[0] == &results[1].Flights[0].Warnings[0] {
		t.Fatal("concurrent callers reused the same warnings backing storage")
	}
	if results[0].Flights[0].CarryOnIncluded == results[1].Flights[0].CarryOnIncluded {
		t.Fatal("concurrent callers reused the same CarryOnIncluded pointer")
	}
	if results[0].Flights[0].CheckedBagsIncluded == results[1].Flights[0].CheckedBagsIncluded {
		t.Fatal("concurrent callers reused the same CheckedBagsIncluded pointer")
	}

	contents[0][0].Text = "changed"
	contents[0][0].Annotations.Audience[0] = "assistant"
	contents[0] = contents[0][:0]
	results[0].Count = 0
	results[0].Flights[0].Warnings[0] = "changed"
	results[0].Flights[0].Legs[0].AirlineCode = "JL"
	*results[0].Flights[0].CarryOnIncluded = false
	*results[0].Flights[0].CheckedBagsIncluded = 2
	results[0].Flights = results[0].Flights[:0]

	if len(contents[1]) == 0 {
		t.Fatal("caller 1 content unexpectedly empty after caller 0 mutation")
	}
	if got := contents[1][0].Text; got != "ok" {
		t.Fatalf("caller 1 text = %q, want %q", got, "ok")
	}
	if got := contents[1][0].Annotations.Audience[0]; got != "user" {
		t.Fatalf("caller 1 audience = %q, want %q", got, "user")
	}
	if results[1].Count != 1 {
		t.Fatalf("caller 1 Count = %d, want 1", results[1].Count)
	}
	if len(results[1].Flights) != 1 {
		t.Fatalf("caller 1 len(Flights) = %d, want 1", len(results[1].Flights))
	}
	if got := results[1].Flights[0].Warnings[0]; got != "Self-connect risk" {
		t.Fatalf("caller 1 warning = %q, want %q", got, "Self-connect risk")
	}
	if got := results[1].Flights[0].Legs[0].AirlineCode; got != "AY" {
		t.Fatalf("caller 1 airline code = %q, want %q", got, "AY")
	}
	if got := *results[1].Flights[0].CarryOnIncluded; !got {
		t.Fatalf("caller 1 CarryOnIncluded = %v, want true", got)
	}
	if got := *results[1].Flights[0].CheckedBagsIncluded; got != 1 {
		t.Fatalf("caller 1 CheckedBagsIncluded = %d, want 1", got)
	}
}
