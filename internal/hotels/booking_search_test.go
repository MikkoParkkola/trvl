package hotels

import (
	"context"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

func TestSearchBooking_OverrideInTest(t *testing.T) {
	orig := SearchBooking
	SearchBooking = func(ctx context.Context, location string, opts HotelSearchOptions) ([]models.HotelResult, error) {
		return []models.HotelResult{{
			Name:       "Booking Test Hotel",
			Price:      99,
			Currency:   "EUR",
			BookingURL: "https://www.booking.com/hotel/test",
		}}, nil
	}
	defer func() { SearchBooking = orig }()

	results, err := SearchBooking(context.Background(), "Corfu", HotelSearchOptions{
		CheckIn: "2026-08-10", CheckOut: "2026-08-17", Currency: "EUR",
	})
	if err != nil {
		t.Fatalf("SearchBooking failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 hotel, got %d", len(results))
	}
	if results[0].Name != "Booking Test Hotel" {
		t.Errorf("name = %q, want Booking Test Hotel", results[0].Name)
	}
	if results[0].BookingURL != "https://www.booking.com/hotel/test" {
		t.Errorf("BookingURL = %q, want https://www.booking.com/hotel/test", results[0].BookingURL)
	}
}

func TestSearchBooking_RateLimiter(t *testing.T) {
	// Just verify the rate limiter doesn't block on first call
	ctx := context.Background()
	_, err := SearchBooking(ctx, "Athens", HotelSearchOptions{
		CheckIn: "2026-09-01", CheckOut: "2026-09-08", Currency: "EUR",
	})
	// This will likely hit a network error (no mock), but shouldn't
	// fail on rate limiting
	if err != nil && err.Error() == "booking rate limiter: context deadline exceeded" {
		t.Fatal("rate limiter blocked before first call")
	}
}
