package fareintel

import (
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/watch"
)

func TestAnalyzeBuyVerdict(t *testing.T) {
	now := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	history := []float64{100, 110, 120, 130, 140}
	points := makePoints(history, now)
	got := Analyze(90, "EUR", points)
	if got.Verdict != VerdictBuy {
		t.Fatalf("verdict = %#v", got)
	}
	if got.MedianPrice != 120 {
		t.Fatalf("median = %v", got.MedianPrice)
	}
}

func TestAnalyzeWaitVerdict(t *testing.T) {
	now := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	got := Analyze(160, "EUR", makePoints([]float64{100, 110, 120, 130, 140}, now))
	if got.Verdict != VerdictWait {
		t.Fatalf("verdict = %#v", got)
	}
}

func TestAnalyzeInsufficientHistory(t *testing.T) {
	got := Analyze(120, "EUR", makePoints([]float64{100, 110}, time.Now()))
	if got.Verdict != VerdictWatch || got.Confidence != "low" {
		t.Fatalf("verdict = %#v", got)
	}
}

func makePoints(values []float64, now time.Time) []watch.PricePoint {
	points := make([]watch.PricePoint, 0, len(values))
	for i, value := range values {
		points = append(points, Point(value, "EUR", now.AddDate(0, 0, -i)))
	}
	return points
}
