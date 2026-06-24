package serpapi

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLiveProbe_SerpAPIHotels exercises the keyed SerpApi hotel path against
// the live API. Gated by TRVL_TEST_LIVE_PROBES=1 (and skipped by -short), it
// mirrors the keyless Google Hotels probe in internal/hotels so the paid
// provider has the same live coverage as the free one. Requires SERPAPI_KEY.
func TestLiveProbe_SerpAPIHotels(t *testing.T) {
	if os.Getenv("TRVL_TEST_LIVE_PROBES") != "1" {
		t.Skip("live probes disabled (set TRVL_TEST_LIVE_PROBES=1)")
	}
	if APIKey() == "" {
		t.Skip("SERPAPI_KEY not set")
	}

	checkIn := time.Now().AddDate(0, 0, 30).Format("2006-01-02")
	checkOut := time.Now().AddDate(0, 0, 31).Format("2006-01-02")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	resp, err := SearchHotels(ctx, "Paris", checkIn, checkOut, "EUR")
	if err != nil {
		t.Fatalf("SerpApi hotels probe failed: %v", err)
	}
	if len(resp.Properties) == 0 {
		t.Fatal("SerpApi returned 0 properties")
	}

	t.Logf("SerpApi hotels: %d properties", len(resp.Properties))

	// Find the first property carrying a real per-night price; SerpApi
	// occasionally returns name-only entries, so scan rather than assume [0].
	var priced *Hotel
	for i := range resp.Properties {
		if resp.Properties[i].RatePerNight.Extracted > 0 {
			priced = &resp.Properties[i]
			break
		}
	}
	if priced == nil {
		t.Fatal("no property had a non-zero per-night price")
	}
	if priced.Name == "" {
		t.Error("priced property has empty name")
	}
	t.Logf("Sample: %s — %.1f rating — %.0f EUR/night",
		priced.Name, priced.Rating, priced.RatePerNight.Extracted)
}
