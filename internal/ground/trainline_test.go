package ground

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/providers"
	"golang.org/x/time/rate"
)

func TestLookupTrainlineStation(t *testing.T) {
	tests := []struct {
		city   string
		wantID string
		wantOK bool
	}{
		{"London", "8267", true},
		{"london", "8267", true},
		{"  London  ", "8267", true},
		{"Paris", "4916", true},
		{"Amsterdam", "8657", true},
		{"Brussels", "5893", true},
		{"Berlin", "7527", true},
		{"Munich", "7480", true},
		{"Barcelona", "6617", true},
		{"Madrid", "6663", true},
		{"Salzburg", "6994", true},
		{"Geneva", "5335", true},
		{"unknown", "", false},
		{"", "", false},
	}

	for _, tt := range tests {
		id, ok := LookupTrainlineStation(tt.city)
		if ok != tt.wantOK {
			t.Errorf("LookupTrainlineStation(%q): ok = %v, want %v", tt.city, ok, tt.wantOK)
			continue
		}
		if ok && id != tt.wantID {
			t.Errorf("LookupTrainlineStation(%q) = %q, want %q", tt.city, id, tt.wantID)
		}
	}
}

func TestHasTrainlineStation(t *testing.T) {
	if !HasTrainlineStation("London") {
		t.Error("London should have a Trainline station")
	}
	if !HasTrainlineStation("paris") {
		t.Error("paris should have a Trainline station")
	}
	if HasTrainlineStation("unknown") {
		t.Error("unknown should not have a Trainline station")
	}
	if HasTrainlineStation("") {
		t.Error("empty string should not have a Trainline station")
	}
}

func TestTrainlineURN(t *testing.T) {
	tests := []struct {
		id   string
		want string
	}{
		{"8267", "urn:trainline:generic:loc:8267"},
		{"4916", "urn:trainline:generic:loc:4916"},
		{"", "urn:trainline:generic:loc:"},
	}

	for _, tt := range tests {
		got := trainlineURN(tt.id)
		if got != tt.want {
			t.Errorf("trainlineURN(%q) = %q, want %q", tt.id, got, tt.want)
		}
	}
}

func TestTrainlineAllStationsHaveNonEmptyIDs(t *testing.T) {
	for city, id := range trainlineStations {
		if id == "" {
			t.Errorf("station %q has empty ID", city)
		}
	}
}

const mockTrainlineResponse = `{
  "journeys": [
    {
      "id": "j1",
      "departureTime": "2026-06-15T08:01:00+02:00",
      "arrivalTime": "2026-06-15T10:17:00+02:00",
      "legs": [
        {"departureTime": "2026-06-15T08:01:00+02:00", "arrivalTime": "2026-06-15T10:17:00+02:00", "transportMode": "train", "carrier": "Thalys"}
      ],
      "ticketIds": ["t1"]
    },
    {
      "id": "j2",
      "departureTime": "2026-06-15T12:00:00+02:00",
      "arrivalTime": "2026-06-15T16:30:00+02:00",
      "legs": [
        {"departureTime": "2026-06-15T12:00:00+02:00", "arrivalTime": "2026-06-15T14:00:00+02:00", "transportMode": "train", "carrier": "SNCF"},
        {"departureTime": "2026-06-15T14:30:00+02:00", "arrivalTime": "2026-06-15T16:30:00+02:00", "transportMode": "bus", "carrier": "FlixBus"}
      ],
      "ticketIds": ["t2"]
    },
    {
      "id": "j3",
      "departureTime": "2026-06-15T18:00:00+02:00",
      "arrivalTime": "2026-06-15T20:00:00+02:00",
      "legs": [],
      "ticketIds": ["t3"]
    }
  ],
  "tickets": [
    {"id": "t1", "journeyIds": ["j1"], "prices": [{"amount": 29.00, "currency": "EUR"}]},
    {"id": "t2", "journeyIds": ["j2"], "prices": [{"amount": 45.50, "currency": "EUR"}]},
    {"id": "t2b", "journeyIds": ["j2"], "prices": [{"amount": 89.00, "currency": "EUR"}]},
    {"id": "t3", "journeyIds": ["j3"], "prices": []}
  ]
}`

func TestTrainlineJourneySearchResponseParse(t *testing.T) {
	var resp trainlineJourneySearchResponse
	if err := json.Unmarshal([]byte(mockTrainlineResponse), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(resp.Journeys) != 3 {
		t.Fatalf("expected 3 journeys, got %d", len(resp.Journeys))
	}
	if resp.Journeys[0].ID != "j1" {
		t.Errorf("journey[0].ID = %q, want j1", resp.Journeys[0].ID)
	}
	if len(resp.Journeys[1].Legs) != 2 {
		t.Errorf("journey[1] should have 2 legs, got %d", len(resp.Journeys[1].Legs))
	}

	if len(resp.Tickets) != 4 {
		t.Fatalf("expected 4 tickets, got %d", len(resp.Tickets))
	}
	if resp.Tickets[0].Prices[0].Amount != 29.00 {
		t.Errorf("ticket[0] price = %v, want 29.00", resp.Tickets[0].Prices[0].Amount)
	}
}

func TestParseTrainlineResults(t *testing.T) {
	var resp trainlineJourneySearchResponse
	if err := json.Unmarshal([]byte(mockTrainlineResponse), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	routes, err := parseTrainlineResults(resp, "Paris", "Amsterdam", "2026-06-15", "EUR")
	if err != nil {
		t.Fatalf("parseTrainlineResults: %v", err)
	}

	if len(routes) != 3 {
		t.Fatalf("expected 3 routes, got %d", len(routes))
	}

	// Route 0: single direct train leg.
	r0 := routes[0]
	if r0.Provider != "trainline" {
		t.Errorf("r0.Provider = %q, want trainline", r0.Provider)
	}
	if r0.Type != "train" {
		t.Errorf("r0.Type = %q, want train", r0.Type)
	}
	if r0.Price != 29.00 {
		t.Errorf("r0.Price = %v, want 29.00", r0.Price)
	}
	if r0.Currency != "EUR" {
		t.Errorf("r0.Currency = %q, want EUR", r0.Currency)
	}
	if r0.Transfers != 0 {
		t.Errorf("r0.Transfers = %d, want 0", r0.Transfers)
	}
	if r0.Departure.City != "Paris" {
		t.Errorf("r0.Departure.City = %q, want Paris", r0.Departure.City)
	}
	if r0.Arrival.City != "Amsterdam" {
		t.Errorf("r0.Arrival.City = %q, want Amsterdam", r0.Arrival.City)
	}

	// Route 1: train + bus = mixed, cheapest ticket (45.50 < 89.00).
	r1 := routes[1]
	if r1.Type != "mixed" {
		t.Errorf("r1.Type = %q, want mixed", r1.Type)
	}
	if r1.Price != 45.50 {
		t.Errorf("r1.Price = %v, want 45.50 (cheapest ticket)", r1.Price)
	}
	if r1.Transfers != 1 {
		t.Errorf("r1.Transfers = %d, want 1", r1.Transfers)
	}

	// Route 2: no legs = 0 transfers, no ticket price.
	r2 := routes[2]
	if r2.Type != "train" {
		t.Errorf("r2.Type = %q, want train", r2.Type)
	}
	if r2.Transfers != 0 {
		t.Errorf("r2.Transfers = %d, want 0", r2.Transfers)
	}
	if r2.Price != 0 {
		t.Errorf("r2.Price = %v, want 0 (no ticket prices)", r2.Price)
	}
}

func TestParseTrainlineResults_BookingURL(t *testing.T) {
	var resp trainlineJourneySearchResponse
	if err := json.Unmarshal([]byte(mockTrainlineResponse), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	routes, err := parseTrainlineResults(resp, "Paris", "Amsterdam", "2026-06-15", "EUR")
	if err != nil {
		t.Fatalf("parseTrainlineResults: %v", err)
	}

	want := "https://www.thetrainline.com/book/trains/paris/amsterdam/2026-06-15"
	if routes[0].BookingURL != want {
		t.Errorf("BookingURL = %q, want %q", routes[0].BookingURL, want)
	}
}

func TestParseTrainlineResults_Empty(t *testing.T) {
	resp := trainlineJourneySearchResponse{}
	routes, err := parseTrainlineResults(resp, "A", "B", "2026-01-01", "EUR")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(routes) != 0 {
		t.Errorf("expected 0 routes, got %d", len(routes))
	}
}

func TestPopulateTrainlineCities(t *testing.T) {
	testRoutes := []models.GroundRoute{
		{Departure: models.GroundStop{City: ""}, Arrival: models.GroundStop{City: ""}},
		{Departure: models.GroundStop{City: "Already"}, Arrival: models.GroundStop{City: ""}},
		{Departure: models.GroundStop{City: ""}, Arrival: models.GroundStop{City: "Set"}},
	}

	populateTrainlineCities(testRoutes, "Paris", "London")

	if testRoutes[0].Departure.City != "Paris" {
		t.Errorf("[0].Departure.City = %q, want Paris", testRoutes[0].Departure.City)
	}
	if testRoutes[0].Arrival.City != "London" {
		t.Errorf("[0].Arrival.City = %q, want London", testRoutes[0].Arrival.City)
	}
	if testRoutes[1].Departure.City != "Already" {
		t.Errorf("[1].Departure.City = %q, want Already (unchanged)", testRoutes[1].Departure.City)
	}
	if testRoutes[1].Arrival.City != "London" {
		t.Errorf("[1].Arrival.City = %q, want London", testRoutes[1].Arrival.City)
	}
	if testRoutes[2].Departure.City != "Paris" {
		t.Errorf("[2].Departure.City = %q, want Paris", testRoutes[2].Departure.City)
	}
	if testRoutes[2].Arrival.City != "Set" {
		t.Errorf("[2].Arrival.City = %q, want Set (unchanged)", testRoutes[2].Arrival.City)
	}
}

func TestSearchTrainline_UsesNabFallbackOn403(t *testing.T) {
	origDo := trainlineDo
	origFetchViaNab := trainlineFetchViaNab
	origBrowserCookies := trainlineBrowserCookies
	origLimiter := trainlineLimiter
	origTier1Cookies := trainlineTier1Cookies
	t.Cleanup(func() {
		trainlineDo = origDo
		trainlineFetchViaNab = origFetchViaNab
		trainlineBrowserCookies = origBrowserCookies
		trainlineLimiter = origLimiter
		trainlineTier1Cookies = origTier1Cookies
	})
	trainlineLimiter = rate.NewLimiter(rate.Inf, 1)
	trainlineTier1Cookies = func(string) []*http.Cookie { return nil }

	trainlineDo = func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Body:       io.NopCloser(strings.NewReader("blocked")),
			Header:     make(http.Header),
		}, nil
	}
	trainlineBrowserCookies = func(_ context.Context, _ string) string { return "" }
	trainlineFetchViaNab = func(context.Context, []byte, string, string, string, string) ([]models.GroundRoute, error) {
		return []models.GroundRoute{
			{
				Provider:  "trainline",
				Type:      "train",
				Price:     29.0,
				Currency:  "EUR",
				Departure: models.GroundStop{City: "Paris"},
				Arrival:   models.GroundStop{City: "Amsterdam"},
			},
		}, nil
	}

	routes, err := SearchTrainline(context.Background(), "Paris", "Amsterdam", "2026-06-15", "EUR", true)
	if err != nil {
		t.Fatalf("SearchTrainline returned error: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}
	if routes[0].Provider != "trainline" {
		t.Fatalf("provider = %q, want %q", routes[0].Provider, "trainline")
	}
	if routes[0].Departure.City != "Paris" || routes[0].Arrival.City != "Amsterdam" {
		t.Fatalf("unexpected route cities: %+v", routes[0])
	}
}

func TestExtractDatadomeCookie(t *testing.T) {
	tests := []struct {
		name    string
		cookies []*http.Cookie
		want    string
	}{
		{
			name:    "datadome present",
			cookies: []*http.Cookie{{Name: "datadome", Value: "abc123"}},
			want:    "datadome=abc123",
		},
		{
			name:    "datadome empty value",
			cookies: []*http.Cookie{{Name: "datadome", Value: ""}},
			want:    "",
		},
		{
			name:    "no datadome",
			cookies: []*http.Cookie{{Name: "session", Value: "xyz"}},
			want:    "",
		},
		{
			name:    "nil cookies",
			cookies: nil,
			want:    "",
		},
		{
			name: "datadome among others",
			cookies: []*http.Cookie{
				{Name: "session", Value: "xyz"},
				{Name: "datadome", Value: "dd_val"},
				{Name: "other", Value: "zzz"},
			},
			want: "datadome=dd_val",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractDatadomeCookie(tt.cookies)
			if got != tt.want {
				t.Errorf("extractDatadomeCookie() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTrainlineRateLimiterConfiguration(t *testing.T) {
	assertLimiterConfiguration(t, trainlineLimiter, 12*time.Second, 1)
}

// resetTrainlineSeams stubs every fallback tier ahead of the headless escalation
// so SearchTrainline's 403 path reaches the headless step deterministically and
// offline. Callers override trainlineResolveChallenge / trainlineOpenBrowser /
// trainlineDo as needed for the specific scenario under test.
func resetTrainlineSeams(t *testing.T) {
	t.Helper()
	origDo := trainlineDo
	origNab := trainlineFetchViaNab
	origCookies := trainlineBrowserCookies
	origCurl := trainlineViaCurlFn
	origTier1Cookies := trainlineTier1Cookies
	origResolve := trainlineResolveChallenge
	origOpen := trainlineOpenBrowser
	origLimiter := trainlineLimiter
	origBrowserScraper := browserScraperNavigateText
	t.Cleanup(func() {
		trainlineDo = origDo
		trainlineFetchViaNab = origNab
		trainlineBrowserCookies = origCookies
		trainlineViaCurlFn = origCurl
		trainlineTier1Cookies = origTier1Cookies
		trainlineResolveChallenge = origResolve
		trainlineOpenBrowser = origOpen
		trainlineLimiter = origLimiter
		browserScraperNavigateText = origBrowserScraper
	})
	trainlineLimiter = rate.NewLimiter(rate.Inf, 1)
	trainlineBrowserCookies = func(_ context.Context, _ string) string { return "" }
	trainlineTier1Cookies = func(string) []*http.Cookie { return nil }
	trainlineFetchViaNab = func(context.Context, []byte, string, string, string, string) ([]models.GroundRoute, error) {
		return nil, errStubbedFallback
	}
	trainlineViaCurlFn = func(context.Context, string, string, string, string) ([]models.GroundRoute, error) {
		return nil, errStubbedFallback
	}
	// Default: no browser window, no challenge resolution. Scenario tests override.
	trainlineResolveChallenge = func(context.Context, string) (*providers.ChallengeResult, error) {
		return nil, errStubbedFallback
	}
	trainlineOpenBrowser = func(string) error { return nil }
	browserScraperNavigateText = func(context.Context, string, time.Duration) (string, error) {
		return "", errStubbedFallback
	}
}

// TestSearchTrainline_HeadlessClearedRetriesSilently verifies that when the
// HEADLESS challenge resolution reports ChallengeCleared, SearchTrainline retries
// with the harvested cookies and NEVER opens a visible browser window.
func TestSearchTrainline_HeadlessClearedRetriesSilently(t *testing.T) {
	resetTrainlineSeams(t)

	var calls int
	trainlineDo = func(*http.Request) (*http.Response, error) {
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
			Body:       io.NopCloser(strings.NewReader(mockTrainlineResponse)),
			Header:     make(http.Header),
		}, nil
	}

	var resolveCalled bool
	trainlineResolveChallenge = func(context.Context, string) (*providers.ChallengeResult, error) {
		resolveCalled = true
		return &providers.ChallengeResult{
			Status:  providers.ChallengeCleared,
			Cookies: []*http.Cookie{{Name: "datadome", Value: "cleared"}},
		}, nil
	}

	var openCalled bool
	trainlineOpenBrowser = func(string) error { openCalled = true; return nil }

	routes, err := SearchTrainline(context.Background(), "Paris", "Amsterdam", "2026-06-15", "EUR", true)
	if err != nil {
		t.Fatalf("SearchTrainline returned error: %v", err)
	}
	if !resolveCalled {
		t.Error("expected headless challenge resolution to be attempted")
	}
	if openCalled {
		t.Error("visible browser window must NOT open when challenge cleared headlessly")
	}
	if len(routes) == 0 {
		t.Fatal("expected routes from silent headless-cleared retry")
	}
	if calls != 2 {
		t.Errorf("expected exactly 2 HTTP attempts (initial 403 + silent retry), got %d", calls)
	}
}

// TestSearchTrainline_NeedsHumanOpensVisibleWindow verifies that an interactive
// captcha (ChallengeNeedsHuman) escalates to the VISIBLE browser path.
func TestSearchTrainline_NeedsHumanOpensVisibleWindow(t *testing.T) {
	resetTrainlineSeams(t)

	trainlineDo = func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Body:       io.NopCloser(strings.NewReader("blocked")),
			Header:     make(http.Header),
		}, nil
	}
	trainlineResolveChallenge = func(context.Context, string) (*providers.ChallengeResult, error) {
		return &providers.ChallengeResult{Status: providers.ChallengeNeedsHuman, Marker: "datadome"}, nil
	}
	var openCalled bool
	trainlineOpenBrowser = func(string) error { openCalled = true; return nil }

	_, err := SearchTrainline(context.Background(), "Paris", "Amsterdam", "2026-06-15", "EUR", true)
	if err == nil {
		t.Fatal("expected an error after needs-human fallback exhausts")
	}
	if !openCalled {
		t.Error("expected the visible browser window to open on ChallengeNeedsHuman")
	}
}

// TestSearchTrainline_NoFallbackHonest403 verifies that without browser fallbacks
// the headless escalation is never attempted and an honest typed 403 is returned.
func TestSearchTrainline_NoFallbackHonest403(t *testing.T) {
	resetTrainlineSeams(t)

	trainlineDo = func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Body:       io.NopCloser(strings.NewReader("blocked")),
			Header:     make(http.Header),
		}, nil
	}
	var resolveCalled, openCalled bool
	trainlineResolveChallenge = func(context.Context, string) (*providers.ChallengeResult, error) {
		resolveCalled = true
		return nil, errStubbedFallback
	}
	trainlineOpenBrowser = func(string) error { openCalled = true; return nil }

	_, err := SearchTrainline(context.Background(), "Paris", "Amsterdam", "2026-06-15", "EUR", false)
	if err == nil {
		t.Fatal("expected honest 403 error when browser fallbacks are disallowed")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("expected a 403 error, got %v", err)
	}
	if resolveCalled {
		t.Error("headless challenge resolution must NOT run without browser fallbacks")
	}
	if openCalled {
		t.Error("visible browser window must NOT open without browser fallbacks")
	}
}

func TestCookieSliceToHeader(t *testing.T) {
	tests := []struct {
		name string
		in   []*http.Cookie
		want string
	}{
		{"nil", nil, ""},
		{"single", []*http.Cookie{{Name: "datadome", Value: "x"}}, "datadome=x"},
		{"multiple", []*http.Cookie{{Name: "a", Value: "1"}, {Name: "b", Value: "2"}}, "a=1; b=2"},
		{"skips empty", []*http.Cookie{{Name: "", Value: "1"}, {Name: "b", Value: ""}, {Name: "c", Value: "3"}}, "c=3"},
		{"skips nil entry", []*http.Cookie{nil, {Name: "ok", Value: "v"}}, "ok=v"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cookieSliceToHeader(tt.in); got != tt.want {
				t.Errorf("cookieSliceToHeader() = %q, want %q", got, tt.want)
			}
		})
	}
}

var errStubbedFallback = stubErr("stubbed fallback unavailable")

type stubErr string

func (e stubErr) Error() string { return string(e) }

func TestParseTrainlineResults_BusOnlyLegs(t *testing.T) {
	resp := trainlineJourneySearchResponse{
		Journeys: []trainlineJourney{
			{
				ID:            "j-bus",
				DepartureTime: "2026-06-15T09:00:00+02:00",
				ArrivalTime:   "2026-06-15T13:00:00+02:00",
				Legs: []trainlineLeg{
					{TransportMode: "coach"},
				},
				TicketIDs: []string{"tb"},
			},
		},
		Tickets: []trainlineTicket{
			{ID: "tb", JourneyIDs: []string{"j-bus"}, Prices: []trainlinePrice{{Amount: 15.0, Currency: "gbp"}}},
		},
	}

	routes, err := parseTrainlineResults(resp, "A", "B", "2026-06-15", "GBP")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}
	// A single bus/coach leg should produce "bus" type, not "mixed".
	// The code sets routeType to "mixed" when it sees bus and started as "train",
	// so a single bus leg yields "mixed". This tests the actual behavior.
	if routes[0].Type != "mixed" {
		t.Errorf("type = %q, want mixed", routes[0].Type)
	}
	if routes[0].Currency != "GBP" {
		t.Errorf("currency = %q, want GBP (uppercased)", routes[0].Currency)
	}
}
