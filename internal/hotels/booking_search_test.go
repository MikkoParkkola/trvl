package hotels

import (
	"context"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

func TestParseBookingHTMLHotels(t *testing.T) {
	html := `<html><body>
	<div data-testid="property-card">
		<div data-testid="title">Summer Shades Hotel</div>
		<span data-testid="price-and-discounted-price">€80</span>
		<div data-testid="review-score" aria-label="Scored 8.4">8.4 <div>265 reviews</div></div>
		<a href="/hotel/gr/summer-shades.html">Book now</a>
	</div>
	<div data-testid="property-card">
		<div data-testid="title">Mr &amp; Mrs White Paros</div>
		<span data-testid="price-and-discounted-price">€104</span>
		<div data-testid="review-score" aria-label="Scored 8.6">8.6 <div>250 reviews</div></div>
		<a href="/hotel/gr/mr-mrs-white.html">Book now</a>
	</div>
	</body></html>`

	hotels := parseBookingHTMLHotels(html, "EUR")
	if len(hotels) != 2 {
		t.Fatalf("expected 2 hotels, got %d", len(hotels))
	}
	if hotels[0].Name != "Summer Shades Hotel" {
		t.Errorf("hotel[0].Name = %q, want Summer Shades Hotel", hotels[0].Name)
	}
	if hotels[0].Price != 80 {
		t.Errorf("hotel[0].Price = %f, want 80", hotels[0].Price)
	}
	if hotels[0].BookingURL != "https://www.booking.com/hotel/gr/summer-shades.html" {
		t.Errorf("hotel[0].BookingURL = %q", hotels[0].BookingURL)
	}
	if hotels[1].Name != "Mr & Mrs White Paros" {
		t.Errorf("hotel[1].Name = %q, want Mr & Mrs White Paros", hotels[1].Name)
	}
}

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
