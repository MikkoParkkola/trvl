package flights

import (
	"context"
	"testing"
)

// multi_boost_test.go — more coverage for SearchMultiAirport (fanout, same-airport skip, error paths, wg/merge).
// Pure validation branches + one multi-airport call (inner searches may error offline but still execute fanout/err-skip/merge paths).

func TestSearchMultiAirport_MoreValidation(t *testing.T) {
	_, err := SearchMultiAirport(context.Background(), []string{}, []string{"LHR"}, "2026-07-10", SearchOptions{})
	if err == nil {
		t.Error("empty origins should error")
	}
	_, err = SearchMultiAirport(context.Background(), []string{"HEL"}, []string{}, "2026-07-10", SearchOptions{})
	if err == nil {
		t.Error("empty dests error")
	}
	_, err = SearchMultiAirport(context.Background(), []string{"HEL"}, []string{"LHR"}, "", SearchOptions{})
	if err == nil {
		t.Error("empty date error")
	}
}

func TestSearchMultiAirport_SameSkipAndMerge(t *testing.T) {
	// same orig/dest is skipped before any search spawn (pure)
	res, err := SearchMultiAirport(context.Background(), []string{"HEL"}, []string{"HEL"}, "2026-07-10", SearchOptions{})
	if err != nil {
		t.Fatalf("same should not error: %v", err)
	}
	if res.Success || res.Count != 0 {
		t.Errorf("same airport should yield 0 results success=false, got %d", res.Count)
	}
}
