package pricesignal

import (
	"math"
	"testing"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

// TRVL.PH.3 / TRVL.PH.4: the confidence floor is the load-bearing guard. Below
// it, no verdict is emitted; at/above it, a verdict appears.
func TestConfidenceFloorBoundary(t *testing.T) {
	// 9 observations, floor 10 -> below floor -> no verdict.
	below := make([]float64, 9)
	for i := range below {
		below[i] = 100
	}
	p := Compute(below, 100, 10)
	if p.Confident {
		t.Fatalf("9 obs with floor 10 must not be confident")
	}
	if p.Band != BandUnknown || p.Verdict != VerdictUnknown {
		t.Fatalf("below floor must be Unknown, got band=%s verdict=%s", p.Band, p.Verdict)
	}
	if p.Observations != 9 {
		t.Fatalf("want 9 observations, got %d", p.Observations)
	}
	// Numbers are still surfaced for transparency.
	if p.Low != 100 || p.High != 100 || p.Median != 100 {
		t.Fatalf("below-floor numbers should still be filled: %+v", p)
	}

	// 10 observations, floor 10 -> at floor -> verdict emitted.
	at := make([]float64, 10)
	for i := range at {
		at[i] = 100
	}
	p = Compute(at, 100, 10)
	if !p.Confident {
		t.Fatalf("10 obs with floor 10 must be confident")
	}
	if p.Verdict == VerdictUnknown {
		t.Fatalf("at floor a verdict must be emitted, got Unknown")
	}
}

func TestBandClassification(t *testing.T) {
	// Spread 100..200 in steps of ~10, 11 observations (>= floor).
	hist := []float64{100, 110, 120, 130, 140, 150, 160, 170, 180, 190, 200}

	cheap := Compute(hist, 105, 0) // default floor 10
	if cheap.Band != BandLow || cheap.Verdict != VerdictBuy {
		t.Fatalf("105 should be low/buy, got %s/%s", cheap.Band, cheap.Verdict)
	}
	if !cheap.Confident {
		t.Fatalf("11 obs should be confident")
	}

	mid := Compute(hist, 150, 0)
	if mid.Band != BandTypical || mid.Verdict != VerdictNeutral {
		t.Fatalf("150 should be typical/neutral, got %s/%s", mid.Band, mid.Verdict)
	}

	dear := Compute(hist, 195, 0)
	if dear.Band != BandHigh || dear.Verdict != VerdictWait {
		t.Fatalf("195 should be high/wait, got %s/%s", dear.Band, dear.Verdict)
	}
}

func TestStatsAndVsMedian(t *testing.T) {
	hist := []float64{100, 110, 120, 130, 140, 150, 160, 170, 180, 190, 200}
	p := Compute(hist, 120, 0)
	if p.Low != 100 || p.High != 200 {
		t.Fatalf("low/high wrong: %+v", p)
	}
	if !approx(p.Median, 150) {
		t.Fatalf("median want 150 got %v", p.Median)
	}
	// (120-150)/150*100 = -20
	if !approx(p.VsMedianPct, -20) {
		t.Fatalf("vsMedianPct want -20 got %v", p.VsMedianPct)
	}
}

func TestIgnoresNonPositiveAndEmpty(t *testing.T) {
	// Empty history -> unknown.
	p := Compute(nil, 100, 0)
	if p.Band != BandUnknown || p.Observations != 0 {
		t.Fatalf("empty history must be unknown/0, got %+v", p)
	}
	// Non-positive prices are dropped.
	hist := []float64{0, -5, 100, 100, 100}
	p = Compute(hist, 100, 0)
	if p.Observations != 3 {
		t.Fatalf("non-positive prices must be ignored, want 3 got %d", p.Observations)
	}
	// Non-positive current -> unknown band even with history.
	p = Compute([]float64{100, 110, 120, 130, 140, 150, 160, 170, 180, 190}, 0, 0)
	if p.Band != BandUnknown {
		t.Fatalf("non-positive current must be unknown, got %s", p.Band)
	}
}

func TestEvenMedian(t *testing.T) {
	if m := median([]float64{10, 20, 30, 40}); !approx(m, 25) {
		t.Fatalf("even median want 25 got %v", m)
	}
}
