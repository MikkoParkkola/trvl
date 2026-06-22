package flights

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestProbeKiwiOneWayPrice fetches a one-way HEL->BCN Kiwi price for comparison
// against the round-trip probe, to confirm the round-trip request's price is the
// full return total (not a one-way). Opt-in via TRVL_TEST_LIVE_PROBES=1.
func TestProbeKiwiOneWayPrice(t *testing.T) {
	if os.Getenv("TRVL_TEST_LIVE_PROBES") != "1" {
		t.Skip("set TRVL_TEST_LIVE_PROBES=1 to run live Kiwi one-way probe")
	}
	dep := time.Now().AddDate(0, 1, 0).Format("2006-01-02")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	opts := SearchOptions{Adults: 1}
	opts.defaults()
	results, err := SearchKiwiFlights(ctx, "HEL", "BCN", dep, "EUR", opts)
	t.Logf("ONE-WAY HEL->BCN -> %d results err=%v", len(results), err)
	for i, f := range results {
		if i >= 3 {
			break
		}
		t.Logf("  [%d] %.2f %s legs=%d", i, f.Price, f.Currency, len(f.Legs))
	}
}
