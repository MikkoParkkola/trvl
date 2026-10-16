package ground

import (
	"context"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/testutil"
)

// TestGroundProbe searches Helsinki->Tallinn 14 days out across all providers.
// The route covers FlixBus, ferries (Tallink, Viking Line, Eckerö Line), and
// Transitous, giving broad coverage of the provider stack. Some providers may
// be down or return zero results on any given day, so we only assert no panic
// and log per-provider counts.
//
// Opt-in via TRVL_TEST_LIVE_PROBES=1.
func TestGroundProbe(t *testing.T) {
	testutil.RequireLiveProbe(t)

	date := time.Now().AddDate(0, 0, 14).Format("2006-01-02")
	t.Logf("searching Helsinki -> Tallinn on %s", date)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	result, err := SearchByName(ctx, "Helsinki", "Tallinn", date, SearchOptions{
		NoCache: true,
	})
	testutil.SkipIfTransient(t, err)
	if err != nil {
		t.Fatalf("SearchByName: %v", err)
	}

	t.Logf("total routes: %d, success: %v", result.Count, result.Success)
	if result.Error != "" {
		t.Logf("partial errors: %s", result.Error)
	}

	// Count routes per provider for diagnostics.
	providerCounts := make(map[string]int)
	for _, r := range result.Routes {
		providerCounts[r.Provider]++
	}
	for provider, count := range providerCounts {
		t.Logf("  %-20s %d routes", provider, count)
	}

	// Log cheapest route if any results came back.
	if len(result.Routes) > 0 {
		r := result.Routes[0] // sorted by price
		t.Logf("cheapest: %s %s, %.2f %s, %d min, %s -> %s",
			r.Provider, r.Type, r.Price, r.Currency,
			r.Duration, r.Departure.Time, r.Arrival.Time)
	}
}
