package mcp

import (
	"context"
	"testing"
	"time"
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
	// Dates must be in the future or optimizeNestedRT rejects them. The pricer
	// is stubbed (date-independent), so only the relative window spacing matters
	// — compute future dates so this test never goes stale (was hard-coded to
	// 2026-07-01, which broke once that date passed).
	base := time.Now().AddDate(0, 2, 0)
	d := func(offset int) string { return base.AddDate(0, 0, offset).Format("2006-01-02") }
	args := map[string]any{
		"origin": "HEL", "destination": "AMS",
		"window1_depart": d(0), "window1_return": d(4),
		"window2_depart": d(19), "window2_return": d(23),
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
