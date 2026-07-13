package ground

import (
	"context"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestSearchByNameSearchOverride pins the per-call SearchOverride seam added
// for the cross-currency hack detectors: when set, SearchByName must return the
// override's result verbatim and must not touch providers, defaults, or the
// cache. A cancelled context proves no real search ran (the production path
// would observe the cancellation; the override ignores it).
func TestSearchByNameSearchOverride(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var gotFrom, gotTo, gotDate string
	sentinel := &models.GroundSearchResult{
		Success: true,
		Count:   1,
		Routes:  []models.GroundRoute{{Provider: "test", Type: "bus", Price: 42, Currency: "EUR"}},
	}
	opts := SearchOptions{
		SearchOverride: func(_ context.Context, from, to, date string, _ SearchOptions) (*models.GroundSearchResult, error) {
			gotFrom, gotTo, gotDate = from, to, date
			return sentinel, nil
		},
	}

	res, err := SearchByName(ctx, "Helsinki", "Tallinn", "2026-06-01", opts)
	if err != nil {
		t.Fatalf("override path returned error: %v", err)
	}
	if res != sentinel {
		t.Fatalf("SearchByName did not return the override result; got %+v", res)
	}
	if gotFrom != "Helsinki" || gotTo != "Tallinn" || gotDate != "2026-06-01" {
		t.Errorf("override received wrong args: from=%q to=%q date=%q", gotFrom, gotTo, gotDate)
	}
}

// TestSearchByNameNoOverrideStillRuns guards the nil-default branch: with no
// override the call proceeds into the normal path (here short-circuited by a
// cancelled context) rather than panicking or mis-dispatching.
func TestSearchByNameNoOverrideStillRuns(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// No SearchOverride set. The production path runs; with a cancelled context
	// it must return without panicking. We assert only that it does not panic
	// and yields a non-nil result or an error — either is a valid production
	// outcome, unlike the override branch which is deterministic.
	res, err := SearchByName(ctx, "Helsinki", "Tallinn", "2026-06-01", SearchOptions{})
	if err == nil && res == nil {
		t.Fatal("nil-override path returned nil result and nil error")
	}
}
