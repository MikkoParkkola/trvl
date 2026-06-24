package destinations

import (
	"context"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// EnrichBestEffort must never call the network for a blank location — it is the
// context-gate that keeps enrichment additive and free on the default paths.
func TestEnrichBestEffort_BlankLocationReturnsNil(t *testing.T) {
	for _, loc := range []string{"", "   ", "\t"} {
		if got := EnrichBestEffort(context.Background(), loc, models.DateRange{}); got != nil {
			t.Errorf("location %q: expected nil (no enrichment, no network), got %+v", loc, got)
		}
	}
}

// A cancelled context makes geocoding fail; EnrichBestEffort must degrade to nil
// rather than surface the error (it never blocks or fails the core search).
func TestEnrichBestEffort_SilentDegradeOnContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := EnrichBestEffort(ctx, "Paris", models.DateRange{}); got != nil {
		t.Errorf("expected nil on cancelled context (silent degrade), got %+v", got)
	}
}
