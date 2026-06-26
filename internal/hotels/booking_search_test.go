package hotels

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"golang.org/x/time/rate"
)

func TestExtractBookingApolloBlob(t *testing.T) {
	page := `<html><head>` +
		`<script data-capla-store-data="apollo" type="application/json">{"ROOT_QUERY":{}}</script>` +
		`</head></html>`
	got := extractBookingApolloBlob(page)
	if got != `{"ROOT_QUERY":{}}` {
		t.Fatalf("extractBookingApolloBlob = %q", got)
	}
	if extractBookingApolloBlob("<html>no apollo here</html>") != "" {
		t.Error("expected empty blob when marker absent")
	}
}

// TestParseBookingApollo_Fixture parses a real Booking.com Apollo store blob
// captured into testdata/ and asserts the field mapping. Capture/refresh the
// fixture with TestCaptureBookingFixture (TRVL_CAPTURE_BOOKING=1).
func TestParseBookingApollo_Fixture(t *testing.T) {
	blob, err := os.ReadFile("testdata/booking_search_apollo.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	hotels, err := parseBookingApollo(string(blob), "EUR")
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	if len(hotels) == 0 {
		t.Fatal("expected >=1 hotel from fixture")
	}

	priced := 0
	var sample *models.HotelResult
	for i := range hotels {
		if hotels[i].Price > 0 {
			priced++
			if sample == nil {
				sample = &hotels[i]
			}
		}
	}
	if priced == 0 {
		t.Fatal("expected >=1 priced hotel from fixture")
	}
	if sample.Name == "" {
		t.Error("priced hotel missing Name")
	}
	if sample.Currency == "" {
		t.Error("priced hotel missing Currency")
	}
	if sample.BookingURL == "" {
		t.Error("priced hotel missing BookingURL")
	}
	t.Logf("fixture parsed %d hotels (%d priced); sample: %q %.2f %s rating=%.1f reviews=%d stars=%d url=%s",
		len(hotels), priced, sample.Name, sample.Price, sample.Currency,
		sample.Rating, sample.ReviewCount, sample.Stars, sample.BookingURL)
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

func TestBuildBookingSearchURL(t *testing.T) {
	got := buildBookingSearchURL("Berlin", "2026-08-10", "2026-08-12", "eur", 2, 25)
	for _, want := range []string{
		"ss=Berlin", "checkin=2026-08-10", "checkout=2026-08-12",
		"group_adults=2", "selected_currency=EUR", "offset=25",
	} {
		if !contains(got, want) {
			t.Errorf("URL %q missing %q", got, want)
		}
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestSearchBooking_RateLimiter(t *testing.T) {
	origLimiter := bookingSearchLimiter
	bookingSearchLimiter = rate.NewLimiter(rate.Every(3*time.Second), 1)
	defer func() { bookingSearchLimiter = origLimiter }()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := bookingSearchLimiter.Wait(ctx); err != nil {
		t.Fatalf("first limiter wait blocked: %v", err)
	}
}
