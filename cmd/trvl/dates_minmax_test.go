package main

import (
	"reflect"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// #170: --min-duration/--max-duration request a window of stay lengths (the
// feature @RobertoReale forked fli to add). --duration stays the single-length
// shorthand.
func TestDurationRange(t *testing.T) {
	cases := []struct {
		name                     string
		duration, minDur, maxDur int
		want                     []int
		wantErr                  bool
	}{
		{"single default duration", 7, 0, 0, []int{7}, false},
		{"explicit range", 7, 5, 7, []int{5, 6, 7}, false},
		{"min only fills max", 7, 5, 0, []int{5}, false},
		{"max only fills min", 7, 0, 6, []int{6}, false},
		{"same min max", 7, 5, 5, []int{5}, false},
		{"min exceeds max", 7, 8, 5, nil, true},
		{"non-positive min", 7, -1, 5, nil, true},
		{"zero duration falls back to 1", 0, 0, 0, []int{1}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := durationRange(c.duration, c.minDur, c.maxDur)
			if c.wantErr {
				if err == nil {
					t.Fatalf("durationRange(%d,%d,%d) want error, got %v", c.duration, c.minDur, c.maxDur, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("durationRange(%d,%d,%d) unexpected error: %v", c.duration, c.minDur, c.maxDur, err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("durationRange(%d,%d,%d) = %v, want %v", c.duration, c.minDur, c.maxDur, got, c.want)
			}
		})
	}
}

func TestMergeAndFinalizeDates(t *testing.T) {
	merged := &models.DateSearchResult{Success: true, TripType: "round_trip"}
	mergeDateResults(merged, &models.DateSearchResult{Success: true, DateRange: "Jul", Dates: []models.DatePriceResult{
		{Date: "2026-07-30", ReturnDate: "2026-08-04", Price: 779},
		{Date: "2026-07-25", ReturnDate: "2026-07-30", Price: 46},
	}})
	// A failed sub-search contributes nothing.
	mergeDateResults(merged, &models.DateSearchResult{Success: false, Dates: []models.DatePriceResult{
		{Date: "2026-09-01", ReturnDate: "2026-09-06", Price: 1},
	}})
	// A duplicate depart+return is deduped.
	mergeDateResults(merged, &models.DateSearchResult{Success: true, Dates: []models.DatePriceResult{
		{Date: "2026-07-30", ReturnDate: "2026-08-04", Price: 779},
		{Date: "2026-08-01", ReturnDate: "2026-08-07", Price: 49},
	}})
	finalizeMergedDates(merged)

	if merged.Count != 3 {
		t.Fatalf("count = %d, want 3 (deduped, failed search excluded)", merged.Count)
	}
	if merged.Dates[0].Price != 46 {
		t.Errorf("cheapest first: got %.0f, want 46", merged.Dates[0].Price)
	}
	for i := 1; i < len(merged.Dates); i++ {
		if merged.Dates[i].Price < merged.Dates[i-1].Price {
			t.Errorf("dates not sorted ascending by price at %d", i)
		}
	}
}
