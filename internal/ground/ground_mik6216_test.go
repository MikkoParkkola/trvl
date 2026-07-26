package ground

// Regression tests for MIK-6216: bot-walled ground pricing providers.
//   - Trainline: Tier-1 (Chrome JA3 + live datadome cookie + matching UA) fallback.
//   - Ferryhopper: MCP response-schema drift (structuredContent.foundDirectItinerariesForTrip).

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/providers"
	"golang.org/x/time/rate"
)

// fakeTier1 is a providers.Fetcher test double.
type fakeTier1 struct {
	do func(*http.Request) (*http.Response, error)
}

func (f fakeTier1) Do(r *http.Request) (*http.Response, error) { return f.do(r) }
func (f fakeTier1) Get(string) (*http.Response, error)         { return f.do(nil) }

// TestSearchTrainline_Tier1FallbackOn403 proves that when the plain client is
// Datadome-403'd and browser fallbacks are allowed, the Tier-1 path (JA3 client +
// live datadome cookie + matching Chrome UA) is attempted and its 200 response is
// parsed into routes — the Rome2Rio-style bypass (#213) applied to Trainline.
func TestSearchTrainline_Tier1FallbackOn403(t *testing.T) {
	origDo := trainlineDo
	origLimiter := trainlineLimiter
	origTier1Cookies := trainlineTier1Cookies
	origNewTier1 := trainlineNewTier1
	t.Cleanup(func() {
		trainlineDo = origDo
		trainlineLimiter = origLimiter
		trainlineTier1Cookies = origTier1Cookies
		trainlineNewTier1 = origNewTier1
	})
	trainlineLimiter = rate.NewLimiter(rate.Inf, 1)

	// Plain client is always Datadome-403'd (no Set-Cookie → seed retry skipped).
	trainlineDo = func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Body:       io.NopCloser(strings.NewReader("blocked")),
			Header:     make(http.Header),
		}, nil
	}
	// Operator has a live datadome clearance cookie in their browser.
	trainlineTier1Cookies = func(string) []*http.Cookie {
		return []*http.Cookie{{Name: "datadome", Value: "live-clearance"}}
	}

	var sawUA, sawCookie bool
	trainlineNewTier1 = func() (providers.Fetcher, error) {
		return fakeTier1{do: func(r *http.Request) (*http.Response, error) {
			if r.Header.Get("User-Agent") == trainlineChromeUA {
				sawUA = true
			}
			for _, c := range r.Cookies() {
				if c.Name == "datadome" && c.Value == "live-clearance" {
					sawCookie = true
				}
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(mockTrainlineResponse)),
				Header:     make(http.Header),
			}, nil
		}}, nil
	}

	routes, err := SearchTrainline(context.Background(), "London", "Paris", "2026-06-15", "EUR", true)
	if err != nil {
		t.Fatalf("SearchTrainline tier1: %v", err)
	}
	if len(routes) != 3 {
		t.Fatalf("expected 3 routes from tier1 path, got %d", len(routes))
	}
	if !sawUA {
		t.Error("tier1 request did not carry the matching Chrome UA")
	}
	if !sawCookie {
		t.Error("tier1 request did not carry the live datadome cookie")
	}
	if routes[0].Provider != "trainline" {
		t.Errorf("provider = %q, want trainline", routes[0].Provider)
	}
	if routes[0].Departure.City != "London" || routes[0].Arrival.City != "Paris" {
		t.Errorf("cities not populated: %+v", routes[0])
	}
}

// TestSearchTrainline_Tier1SkippedWithoutCookies proves the Tier-1 path is NOT
// attempted when no live cookies are available (the JA3 alone won't pass without
// the clearance cookie), so it falls through to the lower tiers and an honest 403.
func TestSearchTrainline_Tier1SkippedWithoutCookies(t *testing.T) {
	origDo := trainlineDo
	origLimiter := trainlineLimiter
	origTier1Cookies := trainlineTier1Cookies
	origNewTier1 := trainlineNewTier1
	origFetchViaNab := trainlineFetchViaNab
	origBrowserCookies := trainlineBrowserCookies
	t.Cleanup(func() {
		trainlineDo = origDo
		trainlineLimiter = origLimiter
		trainlineTier1Cookies = origTier1Cookies
		trainlineNewTier1 = origNewTier1
		trainlineFetchViaNab = origFetchViaNab
		trainlineBrowserCookies = origBrowserCookies
	})
	trainlineLimiter = rate.NewLimiter(rate.Inf, 1)
	trainlineDo = func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Body:       io.NopCloser(strings.NewReader("blocked")),
			Header:     make(http.Header),
		}, nil
	}
	trainlineTier1Cookies = func(string) []*http.Cookie { return nil }
	trainlineBrowserCookies = func(_ context.Context, _ string) string { return "" }
	trainlineFetchViaNab = func(context.Context, []byte, string, string, string, string) ([]models.GroundRoute, error) {
		return nil, nil
	}
	trainlineNewTier1 = func() (providers.Fetcher, error) {
		t.Fatal("tier1 must not be constructed when no live cookies are present")
		return nil, nil
	}

	_, err := SearchTrainline(context.Background(), "London", "Paris", "2026-06-15", "EUR", true)
	if err == nil {
		t.Fatal("expected an honest 403 error when all fallbacks unavailable")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("expected typed 403 error, got %v", err)
	}
}

// loadGroundFixture reads a captured fixture from testdata.
func loadGroundFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return string(b)
}

// TestSearchFerryhopper_StructuredContentSchema is the regression for the
// Ferryhopper MCP schema drift: trip data now lives in
// result.structuredContent.foundDirectItinerariesForTrip (content[0].text is only
// a human summary), with renamed fields (ownerCompany, vessel,
// accommodations[].expectedPrice.totalPriceInCents). Exercises the full
// SearchFerryhopper path against a captured live response.
func TestSearchFerryhopper_StructuredContentSchema(t *testing.T) {
	fixture := loadGroundFixture(t, "ferryhopper_piraeus_santorini.json")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", fixture)
	}))
	defer srv.Close()

	origClient := ferryhopperClient
	origURL := ferryhopperMCPURL
	ferryhopperClient = srv.Client()
	ferryhopperMCPURL = srv.URL
	t.Cleanup(func() {
		ferryhopperClient = origClient
		ferryhopperMCPURL = origURL
	})

	routes, err := SearchFerryhopper(context.Background(), "Piraeus", "Santorini", "2026-07-15", "EUR")
	if err != nil {
		t.Fatalf("SearchFerryhopper: %v", err)
	}
	if len(routes) != 8 {
		t.Fatalf("expected 8 routes from captured fixture, got %d", len(routes))
	}

	for i, r := range routes {
		if r.Type != "ferry" {
			t.Errorf("route %d type = %q, want ferry", i, r.Type)
		}
		if r.Currency != "EUR" {
			t.Errorf("route %d currency = %q, want EUR", i, r.Currency)
		}
		if r.Price <= 0 {
			t.Errorf("route %d has no price (expectedPrice.totalPriceInCents not parsed): %+v", i, r)
		}
		if r.Provider == "" || r.Provider == "ferryhopper" {
			t.Errorf("route %d provider = %q, want a carrier name from ownerCompany", i, r.Provider)
		}
		if r.Departure.City == "" || r.Arrival.City == "" {
			t.Errorf("route %d missing ports: %+v", i, r)
		}
		if !strings.HasPrefix(r.BookingURL, "https://") {
			t.Errorf("route %d booking URL not sanitized https: %q", i, r.BookingURL)
		}
	}

	// First captured trip is FAST FERRIES, cheapest LOUNGE_DECK fare = 4800 cents.
	if got := routes[0].Provider; got != "fast ferries" {
		t.Errorf("route 0 provider = %q, want \"fast ferries\" (from ownerCompany.name)", got)
	}
	if got := routes[0].Price; got != 48.0 {
		t.Errorf("route 0 price = %.2f, want 48.00 (4800 cents)", got)
	}
}

// TestSearchFerryhopper_LegacyTextSchemaStillParses ensures backward
// compatibility with the older shape (trip JSON embedded in content[0].text).
func TestSearchFerryhopper_LegacyTextSchemaStillParses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, mockFerryhopperSSEResponse(mockFerryhopperTripJSON))
	}))
	defer srv.Close()

	origClient := ferryhopperClient
	origURL := ferryhopperMCPURL
	ferryhopperClient = srv.Client()
	ferryhopperMCPURL = srv.URL
	t.Cleanup(func() {
		ferryhopperClient = origClient
		ferryhopperMCPURL = origURL
	})

	routes, err := SearchFerryhopper(context.Background(), "Piraeus", "Santorini", "2026-05-15", "EUR")
	if err != nil {
		t.Fatalf("SearchFerryhopper legacy: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("expected 2 routes from legacy fixture, got %d", len(routes))
	}
	// Legacy operator field → provider; cheapest fare 4500 cents = 45.00.
	if routes[0].Provider != "seajets" {
		t.Errorf("provider = %q, want seajets (legacy operator)", routes[0].Provider)
	}
	if routes[0].Price != 45.0 {
		t.Errorf("price = %.2f, want 45.00 (legacy price cents)", routes[0].Price)
	}
}
