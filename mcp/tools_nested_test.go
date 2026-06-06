package mcp

import (
	"context"
	"testing"
)

// TestOptimizeNestedRT_SavingsScenario (MIK-3076): with a stubbed pricer where
// the destination-anchored round-trip is cheap, the nested pairing must beat the
// naive two-round-trip baseline by >=15% and be surfaced first.
func TestOptimizeNestedRT_SavingsScenario(t *testing.T) {
	stub := func(_ context.Context, origin, _, _, returnDate string) float64 {
		if returnDate == "" {
			return 150 // one-way
		}
		if origin == "AMS" { // round-trip rooted on side B (cheap)
			return 100
		}
		return 300 // round-trip rooted on side A
	}
	args := map[string]any{
		"origin": "HEL", "destination": "AMS",
		"window1_depart": "2026-07-01", "window1_return": "2026-07-05",
		"window2_depart": "2026-07-20", "window2_return": "2026-07-24",
	}
	_, raw, err := optimizeNestedRT(context.Background(), args, stub)
	if err != nil {
		t.Fatalf("optimizeNestedRT error: %v", err)
	}
	res, ok := raw.(nestedRTResult)
	if !ok {
		t.Fatalf("unexpected result type %T", raw)
	}
	if len(res.Pairings) == 0 {
		t.Fatal("no pairings returned")
	}
	naive := 600.0 // 2x RoundTripFromA(300)
	cheapest := res.Pairings[0].Cost
	if cheapest > naive {
		t.Errorf("cheapest pairing %.0f should beat naive %.0f", cheapest, naive)
	}
	if res.BestSave < 0.15*naive {
		t.Errorf("best savings %.0f < 15%% of naive %.0f", res.BestSave, naive)
	}
}

func TestOptimizeNestedRT_BadArgs(t *testing.T) {
	stub := func(_ context.Context, _, _, _, _ string) float64 { return 100 }
	_, _, err := optimizeNestedRT(context.Background(), map[string]any{"origin": "HEL"}, stub)
	if err == nil {
		t.Error("expected error for missing destination/windows")
	}
}
