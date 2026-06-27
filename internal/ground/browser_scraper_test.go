package ground

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseBrowserPrice(t *testing.T) {
	t.Parallel()

	cases := []struct {
		raw      string
		fallback string
		price    float64
		currency string
		ok       bool
	}{
		{raw: "£ 39.50", fallback: "EUR", price: 39.50, currency: "GBP", ok: true},
		{raw: "€12,30", fallback: "USD", price: 12.30, currency: "EUR", ok: true},
		{raw: "$99", fallback: "EUR", price: 99, currency: "USD", ok: true},
		{raw: "", fallback: "EUR", ok: false},
		{raw: "€ nope", fallback: "EUR", ok: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.raw, func(t *testing.T) {
			t.Parallel()
			price, currency, ok := parseBrowserPrice(tc.raw, tc.fallback)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if !ok {
				return
			}
			if price != tc.price {
				t.Fatalf("price = %v, want %v", price, tc.price)
			}
			if currency != tc.currency {
				t.Fatalf("currency = %q, want %q", currency, tc.currency)
			}
		})
	}
}

func TestParseTrainlineBrowserText(t *testing.T) {
	t.Parallel()

	text := `
	06:31 09:47 £ 79.90
	08:01 11:18 £ 39.00
	09:31 12:47 £ 49.00
	`
	routes := parseTrainlineBrowserText(text, "London", "Paris", "2026-04-10", "EUR")
	if len(routes) != 3 {
		t.Fatalf("routes = %d, want 3", len(routes))
	}
	for _, route := range routes {
		if route.Provider != "trainline" {
			t.Fatalf("provider = %q, want trainline", route.Provider)
		}
		if route.Price != 39 {
			t.Fatalf("price = %v, want 39", route.Price)
		}
		if route.Currency != "GBP" {
			t.Fatalf("currency = %q, want GBP", route.Currency)
		}
		if route.Departure.City != "London" || route.Arrival.City != "Paris" {
			t.Fatalf("cities not populated: %+v", route)
		}
		if !strings.Contains(route.BookingURL, "/london/paris/2026-04-10") {
			t.Fatalf("booking URL = %q", route.BookingURL)
		}
	}
}

func TestParseTrainlineBrowserText_DailyMinimum(t *testing.T) {
	t.Parallel()

	routes := parseTrainlineBrowserText("Lowest fare today € 12.30", "Lyon", "Paris", "2026-04-10", "EUR")
	if len(routes) != 1 {
		t.Fatalf("routes = %d, want 1", len(routes))
	}
	if routes[0].Departure.Time != "2026-04-10" || routes[0].Arrival.Time != "2026-04-10" {
		t.Fatalf("daily-minimum times not preserved: %+v", routes[0])
	}
	if routes[0].Price != 12.30 || routes[0].Currency != "EUR" {
		t.Fatalf("price = %v %s, want 12.30 EUR", routes[0].Price, routes[0].Currency)
	}
}

func TestBrowserScrapeRoutes_UnsupportedProvider(t *testing.T) {
	t.Parallel()

	_, err := BrowserScrapeRoutes(context.Background(), "nosuchprovider", "London", "Paris", "2026-04-10", "EUR")
	if !errors.Is(err, errBrowserScraperUnsupportedProvider) {
		t.Fatalf("err = %v, want unsupported-provider error", err)
	}
}

func TestBrowserScrapeRoutes_TrainlineUsesGoBrowserSeam(t *testing.T) {
	orig := browserScraperNavigateText
	t.Cleanup(func() { browserScraperNavigateText = orig })

	var gotURL string
	browserScraperNavigateText = func(_ context.Context, targetURL string, dwell time.Duration) (string, error) {
		gotURL = targetURL
		if dwell != 15*time.Second {
			t.Fatalf("dwell = %v, want 15s", dwell)
		}
		return "06:31 09:47 £39", nil
	}

	routes, err := BrowserScrapeRoutes(context.Background(), "trainline", "London", "Paris", "2026-04-10", "EUR")
	if err != nil {
		t.Fatalf("BrowserScrapeRoutes: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("routes = %d, want 1", len(routes))
	}
	if !strings.Contains(gotURL, "thetrainline.com/book/results") {
		t.Fatalf("target URL = %q", gotURL)
	}
}

func TestBrowserScrapeRoutes_SNCFUsesGoBrowserSeam(t *testing.T) {
	orig := browserScraperSNCFResponses
	t.Cleanup(func() { browserScraperSNCFResponses = orig })

	browserScraperSNCFResponses = func(_ context.Context, bookingURL string, fromStation, toStation SNCFStation, date string) ([]map[string]any, string, error) {
		if !strings.Contains(bookingURL, "/FRPAR/FRLYS/2026-04-10") {
			t.Fatalf("booking URL = %q", bookingURL)
		}
		if fromStation.Code != "FRPAR" || toStation.Code != "FRLYS" || date != "2026-04-10" {
			t.Fatalf("unexpected SNCF arguments: %+v %+v %s", fromStation, toStation, date)
		}
		return []map[string]any{{
			"journeys": []any{map[string]any{
				"price":             map[string]any{"amount": 2900.0, "currency": "EUR"},
				"departureDate":     "2026-04-10T07:00:00",
				"arrivalDate":       "2026-04-10T09:00:00",
				"durationInMinutes": 120.0,
			}},
		}}, "key", nil
	}

	routes, err := BrowserScrapeRoutes(context.Background(), "sncf", "Paris", "Lyon", "2026-04-10", "EUR")
	if err != nil {
		t.Fatalf("BrowserScrapeRoutes: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("routes = %d, want 1", len(routes))
	}
	route := routes[0]
	if route.Provider != "sncf" || route.Price != 2900 {
		t.Fatalf("route parse mismatch: %+v", route)
	}
	if route.Departure.City != "Paris" || route.Departure.Station == "" {
		t.Fatalf("departure not enriched: %+v", route.Departure)
	}
	if route.Arrival.City != "Lyon" || route.Arrival.Station == "" {
		t.Fatalf("arrival not enriched: %+v", route.Arrival)
	}
}

func TestCaptureSNCFKey_UsesGoBrowserSeam(t *testing.T) {
	orig := browserScraperCaptureHeader
	t.Cleanup(func() { browserScraperCaptureHeader = orig })

	browserScraperCaptureHeader = func(_ context.Context, targetURLs []string, headerName string, dwell time.Duration) (string, error) {
		if len(targetURLs) != 2 {
			t.Fatalf("target URLs = %d, want 2", len(targetURLs))
		}
		if headerName != "x-bff-key" {
			t.Fatalf("header = %q, want x-bff-key", headerName)
		}
		if dwell != 5*time.Second {
			t.Fatalf("dwell = %v, want 5s", dwell)
		}
		return "live-key", nil
	}

	if got := captureSNCFKey(context.Background()); got != "live-key" {
		t.Fatalf("captureSNCFKey = %q, want live-key", got)
	}
}

func TestDecodeBrowserJSONBodies(t *testing.T) {
	t.Parallel()

	bodies := []string{
		`{"journeys":[{"price":42}]}`,
		`{"_httpError":403,"_body":"blocked"}`,
		`<html>not json</html>`,
		``,
	}
	got := decodeBrowserJSONBodies(bodies)
	if len(got) != 1 {
		t.Fatalf("decoded = %d, want 1", len(got))
	}
	if _, ok := got[0]["journeys"]; !ok {
		t.Fatalf("decoded body missing journeys: %+v", got[0])
	}
}

func TestBrowserScrapeRoutes_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser integration test in short mode")
	}
	if testingEnv := strings.TrimSpace(os.Getenv("TRVL_TEST_BROWSER")); testingEnv == "" {
		t.Skip("set TRVL_TEST_BROWSER=1 to run browser integration tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	date := time.Now().AddDate(0, 1, 0).Format("2006-01-02")
	routes, err := BrowserScrapeRoutes(ctx, "trainline", "London", "Paris", date, "EUR")
	if err != nil {
		t.Skipf("browser scraper unavailable: %v", err)
	}
	if len(routes) == 0 {
		t.Skip("no routes returned")
	}
	if routes[0].Price <= 0 {
		t.Fatalf("price = %f, want > 0", routes[0].Price)
	}
}
