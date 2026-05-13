package evidence

import (
	"strings"
	"testing"
	"time"
)

func TestAssessFreshness(t *testing.T) {
	now := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	if got := AssessFreshness(now.Add(-time.Hour), now, 24*time.Hour); got != FreshnessFresh {
		t.Fatalf("freshness = %s", got)
	}
	if got := AssessFreshness(now.Add(-48*time.Hour), now, 24*time.Hour); got != FreshnessStale {
		t.Fatalf("freshness = %s", got)
	}
	if got := AssessFreshness(time.Time{}, now, 24*time.Hour); got != FreshnessUnknown {
		t.Fatalf("freshness = %s", got)
	}
}

func TestNewRefSetsIDAndConfidence(t *testing.T) {
	now := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	ref := NewRef("hotel_search", "Google Hotels", "https://example.com", now.Add(-time.Hour), now, 24*time.Hour)
	if ref.ID == "" || ref.Confidence != ConfidenceHigh || ref.Freshness != FreshnessFresh {
		t.Fatalf("ref = %#v", ref)
	}
}

func TestRedactSensitive(t *testing.T) {
	got := RedactSensitive("mikko@example.com booking ABC123 is private")
	if strings.Contains(got, "mikko@example.com") || strings.Contains(got, "ABC123") {
		t.Fatalf("not redacted: %s", got)
	}
}
