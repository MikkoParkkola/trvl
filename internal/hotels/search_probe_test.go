package hotels

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestLiveProbe_GoogleHotels(t *testing.T) {
	if os.Getenv("TRVL_TEST_LIVE_PROBES") != "1" {
		t.Skip("live probes disabled (set TRVL_TEST_LIVE_PROBES=1)")
	}

	// Search for hotels in Paris, 30 days out
	checkin := time.Now().AddDate(0, 0, 30).Format("2006-01-02")
	checkout := time.Now().AddDate(0, 0, 31).Format("2006-01-02")

	opts := HotelSearchOptions{
		CheckIn:  checkin,
		CheckOut: checkout,
		Guests:   2,
		Currency: "EUR",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	results, err := SearchHotels(ctx, "Paris", opts)
	if err != nil {
		t.Fatalf("Google Hotels probe failed: %v", err)
	}

	if len(results.Hotels) == 0 {
		t.Fatal("Google Hotels returned 0 results")
	}

	t.Logf("Google Hotels: %d results", len(results.Hotels))

	// Validate first result has required fields
	r := results.Hotels[0]
	if r.Name == "" {
		t.Error("first result has empty name")
	}
	if r.Rating == 0 {
		t.Error("first result has zero rating")
	}
	// Ratings should be on 0-10 scale (normalized from Google's 0-5)
	if r.Rating > 10 {
		t.Errorf("rating %.1f exceeds 0-10 scale — normalization may be broken", r.Rating)
	}
	if r.Price == 0 {
		t.Error("first result has zero price")
	}

	t.Logf("Sample: %s — %.1f/10 — %.0f %s", r.Name, r.Rating, r.Price, r.Currency)
}
