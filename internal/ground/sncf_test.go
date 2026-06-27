package ground

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/providers"
	"github.com/MikkoParkkola/trvl/internal/testutil"
	"golang.org/x/time/rate"
)

func TestLookupSNCFStation(t *testing.T) {
	tests := []struct {
		city     string
		wantCode string
		wantOK   bool
	}{
		{"Paris", "FRPAR", true},
		{"paris", "FRPAR", true},
		{"PARIS", "FRPAR", true},
		{"  Paris  ", "FRPAR", true},
		{"Lyon", "FRLYS", true},
		{"Marseille", "FRMRS", true},
		{"Bordeaux", "FRBOJ", true},
		{"Nice", "FRNIC", true},
		{"Strasbourg", "FRSBG", true},
		{"Lille", "FRLIL", true},
		{"Toulouse", "FRTLS", true},
		{"Nantes", "FRNTE", true},
		{"Paris Nord", "FRPNO", true},
		{"Paris Gare de Lyon", "FRPLY", true},
		{"Paris Montparnasse", "FRPMO", true},
		{"Brussels", "BEBMI", true},
		{"Geneva", "CHGVA", true},
		{"Barcelona", "ESBCN", true},
		{"London", "GBSPX", true},
		{"Prague", "", false},
		{"", "", false},
		{"Nonexistent", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.city, func(t *testing.T) {
			station, ok := LookupSNCFStation(tt.city)
			if ok != tt.wantOK {
				t.Fatalf("LookupSNCFStation(%q) ok = %v, want %v", tt.city, ok, tt.wantOK)
			}
			if ok && station.Code != tt.wantCode {
				t.Errorf("Code = %q, want %q", station.Code, tt.wantCode)
			}
		})
	}
}

func TestLookupSNCFStation_Metadata(t *testing.T) {
	station, ok := LookupSNCFStation("Lyon")
	if !ok {
		t.Fatal("expected Lyon to be found")
	}
	if station.Name != "Lyon Part-Dieu" {
		t.Errorf("Name = %q, want %q", station.Name, "Lyon Part-Dieu")
	}
	if station.City != "Lyon" {
		t.Errorf("City = %q, want %q", station.City, "Lyon")
	}
	if station.Country != "FR" {
		t.Errorf("Country = %q, want %q", station.Country, "FR")
	}
}

func TestHasSNCFRoute(t *testing.T) {
	tests := []struct {
		from string
		to   string
		want bool
	}{
		{"Paris", "Lyon", true},       // Both French
		{"Paris", "Brussels", true},   // One French
		{"Brussels", "Paris", true},   // One French (reversed)
		{"Brussels", "Geneva", false}, // Neither French
		{"Paris", "Prague", false},    // Prague not in station list
		{"", "Paris", false},
		{"Paris", "", false},
	}

	for _, tt := range tests {
		name := tt.from + "->" + tt.to
		t.Run(name, func(t *testing.T) {
			got := HasSNCFRoute(tt.from, tt.to)
			if got != tt.want {
				t.Errorf("HasSNCFRoute(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.want)
			}
		})
	}
}

func TestAllSNCFStationsHaveRequiredFields(t *testing.T) {
	for city, station := range sncfStations {
		if station.Code == "" {
			t.Errorf("station %q has empty Code", city)
		}
		if station.Name == "" {
			t.Errorf("station %q has empty Name", city)
		}
		if station.City == "" {
			t.Errorf("station %q has empty City", city)
		}
		if station.Country == "" {
			t.Errorf("station %q has empty Country", city)
		}
		if len(station.Code) != 5 {
			t.Errorf("station %q Code %q should be 5 characters", city, station.Code)
		}
	}
}

func TestSNCFStationCodesAreUppercase(t *testing.T) {
	for city, station := range sncfStations {
		if station.Code != strings.ToUpper(station.Code) {
			t.Errorf("station %q Code %q should be uppercase", city, station.Code)
		}
	}
}

func TestBuildSNCFBookingURL(t *testing.T) {
	u := buildSNCFBookingURL("FRPAR", "FRLYS", "2026-04-10")
	if u == "" {
		t.Fatal("booking URL should not be empty")
	}
	if !strings.Contains(u, "sncf-connect.com") {
		t.Error("should contain sncf-connect.com")
	}
	if !strings.Contains(u, "FRPAR") {
		t.Error("should contain origin code")
	}
	if !strings.Contains(u, "FRLYS") {
		t.Error("should contain destination code")
	}
	if !strings.Contains(u, "2026-04-10") {
		t.Error("should contain date")
	}
}

func TestSNCFRateLimiterConfiguration(t *testing.T) {
	assertLimiterConfiguration(t, sncfLimiter, 6*time.Second, 1)
}

func TestSearchSNCF_Integration(t *testing.T) {
	testutil.RequireLiveIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	date := time.Now().AddDate(0, 1, 0).Format("2006-01-02")

	routes, err := SearchSNCF(ctx, "Paris", "Lyon", date, "EUR", false)
	if err != nil {
		// The SNCF API may be behind Cloudflare or temporarily unavailable.
		t.Skipf("SNCF API unavailable (expected in CI): %v", err)
	}
	if len(routes) == 0 {
		t.Skip("no SNCF routes returned (may be outside booking window)")
	}

	r := routes[0]
	if r.Provider != "sncf" {
		t.Errorf("provider = %q, want sncf", r.Provider)
	}
	if r.Type != "train" {
		t.Errorf("type = %q, want train", r.Type)
	}
	if r.Price <= 0 {
		t.Errorf("price = %f, should be > 0", r.Price)
	}
	if r.Currency != "EUR" {
		t.Errorf("currency = %q, want EUR", r.Currency)
	}
	if r.BookingURL == "" {
		t.Error("booking URL should not be empty")
	}
}

func TestSearchSNCF_UnknownStation(t *testing.T) {
	ctx := context.Background()
	_, err := SearchSNCF(ctx, "Nonexistent", "Lyon", "2026-04-10", "EUR", false)
	if err == nil {
		t.Error("expected error for unknown station")
	}
}

func TestSearchSNCFCalendar_UsesNabFallbackOn403(t *testing.T) {
	origDo := sncfDo
	origFetchViaNab := sncfFetchViaNab
	origBrowserCookies := sncfBrowserCookies
	origLimiter := sncfLimiter
	t.Cleanup(func() {
		sncfDo = origDo
		sncfFetchViaNab = origFetchViaNab
		sncfBrowserCookies = origBrowserCookies
		sncfLimiter = origLimiter
	})
	sncfLimiter = rate.NewLimiter(rate.Inf, 1)

	sncfDo = func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Body:       io.NopCloser(strings.NewReader("blocked")),
			Header:     make(http.Header),
		}, nil
	}
	sncfBrowserCookies = func(string) string { return "" }
	sncfFetchViaNab = func(context.Context, string, SNCFStation, SNCFStation, string, string) ([]models.GroundRoute, error) {
		return []models.GroundRoute{
			{
				Provider:  "sncf",
				Type:      "train",
				Price:     29.0,
				Currency:  "EUR",
				Departure: models.GroundStop{City: "Paris"},
				Arrival:   models.GroundStop{City: "Lyon"},
			},
		}, nil
	}

	fromStation, _ := LookupSNCFStation("Paris")
	toStation, _ := LookupSNCFStation("Lyon")
	routes, err := searchSNCFCalendar(context.Background(), fromStation, toStation, "2026-04-10", "EUR", true)
	if err != nil {
		t.Fatalf("searchSNCFCalendar returned error: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}
	if routes[0].Provider != "sncf" {
		t.Fatalf("provider = %q, want %q", routes[0].Provider, "sncf")
	}
	if routes[0].Departure.City != "Paris" || routes[0].Arrival.City != "Lyon" {
		t.Fatalf("unexpected route cities: %+v", routes[0])
	}
}

// sncfCalendarOKBody is a minimal calendar-API JSON body for date 2026-04-10.
const sncfCalendarOKBody = `[{"date":"2026-04-10","price":4500}]`

// resetSNCFSeams stubs the calendar/curl/nab tiers ahead of the headless
// escalation so SearchSNCF reaches the headless step deterministically offline.
func resetSNCFSeams(t *testing.T) {
	t.Helper()
	origDo := sncfDo
	origNab := sncfFetchViaNab
	origCookies := sncfBrowserCookies
	origCurl := sncfViaCurlFn
	origResolve := sncfResolveChallenge
	origOpen := sncfOpenBrowser
	origLimiter := sncfLimiter
	origBrowserScraper := browserScraperSNCFResponses
	t.Cleanup(func() {
		sncfDo = origDo
		sncfFetchViaNab = origNab
		sncfBrowserCookies = origCookies
		sncfViaCurlFn = origCurl
		sncfResolveChallenge = origResolve
		sncfOpenBrowser = origOpen
		sncfLimiter = origLimiter
		browserScraperSNCFResponses = origBrowserScraper
	})
	sncfLimiter = rate.NewLimiter(rate.Inf, 1)
	sncfBrowserCookies = func(string) string { return "" }
	sncfFetchViaNab = func(context.Context, string, SNCFStation, SNCFStation, string, string) ([]models.GroundRoute, error) {
		return nil, errStubbedFallback
	}
	sncfViaCurlFn = func(context.Context, string, string, string, string) ([]models.GroundRoute, error) {
		return nil, errStubbedFallback
	}
	sncfResolveChallenge = func(context.Context, string) (*providers.ChallengeResult, error) {
		return nil, errStubbedFallback
	}
	sncfOpenBrowser = func(string) error { return nil }
	browserScraperSNCFResponses = func(context.Context, string, SNCFStation, SNCFStation, string) ([]map[string]any, string, error) {
		return nil, "", errStubbedFallback
	}
}

// TestSearchSNCF_HeadlessClearedRetriesSilently verifies a ChallengeCleared
// headless resolution triggers a silent calendar retry with the harvested
// cookies and opens NO visible window.
func TestSearchSNCF_HeadlessClearedRetriesSilently(t *testing.T) {
	resetSNCFSeams(t)

	var calls int
	sncfDo = func(*http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return &http.Response{
				StatusCode: http.StatusForbidden,
				Body:       io.NopCloser(strings.NewReader("blocked")),
				Header:     make(http.Header),
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(sncfCalendarOKBody)),
			Header:     make(http.Header),
		}, nil
	}
	var resolveCalled bool
	sncfResolveChallenge = func(context.Context, string) (*providers.ChallengeResult, error) {
		resolveCalled = true
		return &providers.ChallengeResult{
			Status:  providers.ChallengeCleared,
			Cookies: []*http.Cookie{{Name: "datadome", Value: "cleared"}},
		}, nil
	}
	var openCalled bool
	sncfOpenBrowser = func(string) error { openCalled = true; return nil }

	routes, err := SearchSNCF(context.Background(), "Paris", "Lyon", "2026-04-10", "EUR", true)
	if err != nil {
		t.Fatalf("SearchSNCF returned error: %v", err)
	}
	if !resolveCalled {
		t.Error("expected headless challenge resolution to be attempted")
	}
	if openCalled {
		t.Error("visible browser window must NOT open when challenge cleared headlessly")
	}
	if len(routes) != 1 {
		t.Fatalf("expected 1 route from silent retry, got %d", len(routes))
	}
	if routes[0].Price != 45.0 {
		t.Errorf("price = %v, want 45.0", routes[0].Price)
	}
}

// TestSearchSNCF_NeedsHumanOpensVisibleWindow verifies an interactive captcha
// escalates to the VISIBLE browser path.
func TestSearchSNCF_NeedsHumanOpensVisibleWindow(t *testing.T) {
	resetSNCFSeams(t)

	sncfDo = func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Body:       io.NopCloser(strings.NewReader("blocked")),
			Header:     make(http.Header),
		}, nil
	}
	sncfResolveChallenge = func(context.Context, string) (*providers.ChallengeResult, error) {
		return &providers.ChallengeResult{Status: providers.ChallengeNeedsHuman, Marker: "datadome"}, nil
	}
	var openCalled bool
	sncfOpenBrowser = func(string) error { openCalled = true; return nil }

	_, err := SearchSNCF(context.Background(), "Paris", "Lyon", "2026-04-10", "EUR", true)
	if err == nil {
		t.Fatal("expected an error after needs-human fallback exhausts")
	}
	if !openCalled {
		t.Error("expected the visible browser window to open on ChallengeNeedsHuman")
	}
}

// TestSearchSNCF_NoFallbackHonestError verifies that without browser fallbacks
// the headless escalation never runs and the typed API error is returned.
func TestSearchSNCF_NoFallbackHonestError(t *testing.T) {
	resetSNCFSeams(t)

	sncfDo = func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Body:       io.NopCloser(strings.NewReader("blocked")),
			Header:     make(http.Header),
		}, nil
	}
	var resolveCalled, openCalled bool
	sncfResolveChallenge = func(context.Context, string) (*providers.ChallengeResult, error) {
		resolveCalled = true
		return nil, errStubbedFallback
	}
	sncfOpenBrowser = func(string) error { openCalled = true; return nil }

	_, err := SearchSNCF(context.Background(), "Paris", "Lyon", "2026-04-10", "EUR", false)
	if err == nil {
		t.Fatal("expected an error when browser fallbacks are disallowed")
	}
	if resolveCalled {
		t.Error("headless challenge resolution must NOT run without browser fallbacks")
	}
	if openCalled {
		t.Error("visible browser window must NOT open without browser fallbacks")
	}
}
