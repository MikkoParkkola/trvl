package hotels

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
)

// TestCaptureBookingFixture is a developer tool (not part of the default suite)
// that harvests an aws-waf-token via CDP, fetches a real Booking.com search
// page, and writes the apollo JSON blob to testdata/. Run with:
//
//	TRVL_CAPTURE_BOOKING=1 TRVL_ALLOW_BROWSER_COOKIES=1 go test ./internal/hotels/ -run TestCaptureBookingFixture -v -count=1
func TestCaptureBookingFixture(t *testing.T) {
	if os.Getenv("TRVL_CAPTURE_BOOKING") == "" {
		t.Skip("set TRVL_CAPTURE_BOOKING=1 to capture a live fixture")
	}

	searchURL := buildBookingSearchURL("Berlin", "2026-08-10", "2026-08-12", "EUR", 2, 0)
	t.Logf("search URL: %s", searchURL)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	token, err := acquireBookingWAFToken(ctx, searchURL, true)
	if err != nil {
		t.Fatalf("token harvest: %v", err)
	}
	t.Logf("aws-waf-token len=%d", len(token))

	client := batchexec.NewClient()
	status, body, err := client.GetWithHeaders(ctx, searchURL, map[string]string{
		"Accept":          "text/html,application/xhtml+xml",
		"Accept-Language": "en-US,en;q=0.9",
		"Cookie":          "aws-waf-token=" + token,
	})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	t.Logf("status=%d body_len=%d", status, len(body))
	if status != 200 {
		t.Fatalf("non-200 status %d", status)
	}

	blob := extractBookingApolloBlob(string(body))
	if blob == "" {
		// Save the full body for inspection if the blob marker is missing.
		_ = os.WriteFile("testdata/booking_search_raw.html", body, 0o644)
		t.Fatalf("apollo blob not found; wrote raw HTML for inspection")
	}
	if err := os.WriteFile("testdata/booking_search_apollo.json", []byte(blob), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	t.Logf("wrote apollo fixture, len=%d", len(blob))

	hotels := parseBookingApollo(blob, "EUR")
	priced := 0
	for _, h := range hotels {
		if h.Price > 0 {
			priced++
		}
	}
	t.Logf("parsed %d hotels, %d priced", len(hotels), priced)
	if len(hotels) > 0 {
		t.Logf("sample: name=%q price=%.2f cur=%s url=%s rating=%.1f reviews=%d",
			hotels[0].Name, hotels[0].Price, hotels[0].Currency, hotels[0].BookingURL,
			hotels[0].Rating, hotels[0].ReviewCount)
	}
}
