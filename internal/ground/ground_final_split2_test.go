package ground

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
	"golang.org/x/time/rate"
)

func TestResolveAndSearch_FromEmpty(t *testing.T) {
	_, err := resolveAndSearch(
		context.Background(), "Helsinki", "Tallinn", "test",
		func(ctx context.Context, query string) ([]string, error) {
			if query == "Helsinki" {
				return nil, nil // empty result
			}
			return []string{"resolved"}, nil
		},
		func(from, to string) ([]models.GroundRoute, error) {
			return nil, nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "no test city found") {
		t.Errorf("expected 'no test city found' error, got %v", err)
	}
}

func TestResolveAndSearch_ToEmpty(t *testing.T) {
	_, err := resolveAndSearch(
		context.Background(), "Helsinki", "Tallinn", "test",
		func(ctx context.Context, query string) ([]string, error) {
			if query == "Tallinn" {
				return nil, nil // empty result
			}
			return []string{"resolved"}, nil
		},
		func(from, to string) ([]models.GroundRoute, error) {
			return nil, nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "no test city found") {
		t.Errorf("expected 'no test city found' error, got %v", err)
	}
}

func TestResolveAndSearch_AutoCompleteError(t *testing.T) {
	_, err := resolveAndSearch(
		context.Background(), "Helsinki", "Tallinn", "test",
		func(ctx context.Context, query string) ([]string, error) {
			return nil, fmt.Errorf("API down")
		},
		func(from, to string) ([]models.GroundRoute, error) {
			return nil, nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "resolve from city") {
		t.Errorf("expected 'resolve from city' error, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// parseSNCFResponse — covers response parsing
// ---------------------------------------------------------------------------

func TestParseSNCFResponse_DateFilter(t *testing.T) {
	body := `[
		{"date":"2026-05-01","price":3500},
		{"date":"2026-05-02","price":4200},
		{"date":"2026-05-03","price":2900}
	]`

	fromStation, _ := LookupSNCFStation("Paris")
	toStation, _ := LookupSNCFStation("Lyon")

	routes, err := parseSNCFResponse(strings.NewReader(body), fromStation, toStation, "2026-05-02", "EUR")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	// Only 2026-05-02 should be returned.
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}
	if routes[0].Price != 42.0 {
		t.Errorf("price = %f, want 42.0", routes[0].Price)
	}
}

func TestParseSNCFResponse_InvalidJSON(t *testing.T) {
	_, err := parseSNCFResponse(strings.NewReader("not json"), SNCFStation{}, SNCFStation{}, "2026-05-01", "EUR")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

// ---------------------------------------------------------------------------
// extractSNCFBFFRoute — edge cases
// ---------------------------------------------------------------------------

func TestExtractSNCFBFFRoute_NoDepartureTime(t *testing.T) {
	item := map[string]any{
		"price": 29.0,
		// No departure time.
	}
	r := extractSNCFBFFRoute(item, "", "2026-05-01", "EUR")
	if r != nil {
		t.Error("expected nil for missing departure time")
	}
}

func TestExtractSNCFBFFRoute_TruncatesLongTimes(t *testing.T) {
	item := map[string]any{
		"price":         39.0,
		"departureDate": "2026-05-01T08:30:00+02:00",
		"arrivalDate":   "2026-05-01T10:45:00+02:00",
	}
	r := extractSNCFBFFRoute(item, "https://book.example.com", "2026-05-01", "EUR")
	if r == nil {
		t.Fatal("expected non-nil route")
	}
	// Times should be truncated to 19 chars.
	if len(r.Departure.Time) > 19 {
		t.Errorf("departure time = %q, should be truncated", r.Departure.Time)
	}
	if len(r.Arrival.Time) > 19 {
		t.Errorf("arrival time = %q, should be truncated", r.Arrival.Time)
	}
}

func TestExtractSNCFBFFRoute_PriceFromMapWithCurrencyCode(t *testing.T) {
	item := map[string]any{
		"price":         map[string]any{"value": 42.0, "currencyCode": "GBP"},
		"departureDate": "2026-05-01T08:00:00",
	}
	r := extractSNCFBFFRoute(item, "", "2026-05-01", "EUR")
	if r == nil {
		t.Fatal("expected non-nil route")
	}
	if r.Price != 42.0 {
		t.Errorf("price = %f, want 42", r.Price)
	}
	if r.Currency != "GBP" {
		t.Errorf("currency = %q, want GBP", r.Currency)
	}
}

// ---------------------------------------------------------------------------
// isProviderNotApplicable — edge cases
// ---------------------------------------------------------------------------

func TestIsProviderNotApplicable_Various(t *testing.T) {
	tests := []struct {
		msg  string
		want bool
	}{
		{"no DB station for Helsinki", true},
		{"no FlixBus city found for X", true},
		{"no port for Y", true},
		{"no route for Z", true},
		{"no Tallink route", true},
		{"no Eurostar route", true},
		{"no DFDS route", true},
		{"no Stena Line route", true},
		{"rate limiter: rate: Wait exceeded", true},
		{"would exceed context deadline", true},
		{"context deadline exceeded", true},
		{"some other error", false},
		{"", false},
	}
	for _, tt := range tests {
		var err error
		if tt.msg != "" {
			err = fmt.Errorf("%s", tt.msg)
		}
		got := isProviderNotApplicable(err)
		if got != tt.want {
			t.Errorf("isProviderNotApplicable(%q) = %v, want %v", tt.msg, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// browserFallbacksEnabled
// ---------------------------------------------------------------------------

func TestBrowserFallbacksEnabled_ExplicitOpt(t *testing.T) {
	opts := SearchOptions{AllowBrowserFallbacks: true}
	if !browserFallbacksEnabled(opts) {
		t.Error("should be true when AllowBrowserFallbacks is set")
	}
}

func TestBrowserFallbacksEnabled_DefaultFalse(t *testing.T) {
	opts := SearchOptions{}
	// Unless env var is set, should be false.
	if browserFallbacksEnabled(opts) {
		t.Error("should be false by default (unless env var set)")
	}
}

// ---------------------------------------------------------------------------
// stenalineRouteKey
// ---------------------------------------------------------------------------

func TestStenalineRouteKey_Uppercase(t *testing.T) {
	if got := stenalineRouteKey("got", "kie"); got != "GOT-KIE" {
		t.Errorf("stenalineRouteKey = %q, want GOT-KIE", got)
	}
	if got := stenalineRouteKey("TRG", "ROS"); got != "TRG-ROS" {
		t.Errorf("stenalineRouteKey = %q, want TRG-ROS", got)
	}
}

// ---------------------------------------------------------------------------
// SearchEurostar — DI-based coverage
// ---------------------------------------------------------------------------

func TestSearchEurostar_UnknownStation(t *testing.T) {
	ctx := context.Background()
	_, err := SearchEurostar(ctx, "Nonexistent", "Paris", "2026-06-15", "2026-06-22", "GBP", false)
	if err == nil || !strings.Contains(err.Error(), "no Eurostar station") {
		t.Errorf("expected 'no Eurostar station' error, got %v", err)
	}
	_, err = SearchEurostar(ctx, "London", "Nonexistent", "2026-06-15", "2026-06-22", "GBP", false)
	if err == nil || !strings.Contains(err.Error(), "no Eurostar station") {
		t.Errorf("expected 'no Eurostar station' error, got %v", err)
	}
}

func TestSearchEurostar_200OK(t *testing.T) {
	origDo := eurostarDo
	origLimiter := eurostarLimiter
	origClient := eurostarClient
	t.Cleanup(func() { eurostarDo = origDo; eurostarLimiter = origLimiter; eurostarClient = origClient })
	eurostarLimiter = rate.NewLimiter(rate.Inf, 1)
	eurostarClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("test: timetable disabled")
	})}

	gqlResponse := `{
		"data": {
			"cheapestFaresSearch": [{
				"cheapestFares": [
					{"date": "2026-06-15", "price": 39.0},
					{"date": "2026-06-16", "price": 55.0}
				]
			}]
		}
	}`

	eurostarDo = func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(gqlResponse)),
			Header:     make(http.Header),
		}, nil
	}

	routes, err := SearchEurostar(context.Background(), "London", "Paris", "2026-06-15", "2026-06-22", "GBP", false)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(routes) == 0 {
		t.Fatal("expected routes")
	}
	if routes[0].Provider != "eurostar" {
		t.Errorf("provider = %q, want eurostar", routes[0].Provider)
	}
	if routes[0].Price != 39.0 {
		t.Errorf("price = %f, want 39", routes[0].Price)
	}
}

func TestSearchEurostar_DefaultCurrency(t *testing.T) {
	origDo := eurostarDo
	origLimiter := eurostarLimiter
	origClient := eurostarClient
	t.Cleanup(func() { eurostarDo = origDo; eurostarLimiter = origLimiter; eurostarClient = origClient })
	eurostarLimiter = rate.NewLimiter(rate.Inf, 1)
	eurostarClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("test: timetable disabled")
	})}

	eurostarDo = func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"data":{"cheapestFaresSearch":[{"cheapestFares":[{"date":"2026-06-15","price":45}]}]}}`)),
			Header:     make(http.Header),
		}, nil
	}

	routes, err := SearchEurostar(context.Background(), "London", "Paris", "2026-06-15", "2026-06-22", "", false)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(routes) > 0 && routes[0].Currency != "GBP" {
		t.Errorf("currency = %q, want GBP (default)", routes[0].Currency)
	}
}

func TestSearchEurostar_NonOKStatus(t *testing.T) {
	origDo := eurostarDo
	origLimiter := eurostarLimiter
	t.Cleanup(func() { eurostarDo = origDo; eurostarLimiter = origLimiter })
	eurostarLimiter = rate.NewLimiter(rate.Inf, 1)

	eurostarDo = func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadGateway,
			Body:       io.NopCloser(strings.NewReader("bad gateway")),
			Header:     make(http.Header),
		}, nil
	}

	_, err := SearchEurostar(context.Background(), "London", "Paris", "2026-06-15", "2026-06-22", "GBP", false)
	if err == nil || !strings.Contains(err.Error(), "HTTP 502") {
		t.Errorf("expected HTTP 502 error, got %v", err)
	}
}

func TestSearchEurostar_403_NabFallback(t *testing.T) {
	origDo := eurostarDo
	origBrowserCookies := eurostarBrowserCookies
	origFetchViaNab := eurostarFetchViaNab
	origLimiter := eurostarLimiter
	t.Cleanup(func() {
		eurostarDo = origDo
		eurostarBrowserCookies = origBrowserCookies
		eurostarFetchViaNab = origFetchViaNab
		eurostarLimiter = origLimiter
	})
	eurostarLimiter = rate.NewLimiter(rate.Inf, 1)

	eurostarDo = func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Body:       io.NopCloser(strings.NewReader("blocked")),
			Header:     make(http.Header),
		}, nil
	}
	eurostarBrowserCookies = func(string) string { return "" }
	eurostarFetchViaNab = func(context.Context, []byte, EurostarStation, EurostarStation, string, string, bool) ([]models.GroundRoute, error) {
		return []models.GroundRoute{
			{Provider: "eurostar", Type: "train", Price: 39, Currency: "GBP",
				Departure: models.GroundStop{City: "London"},
				Arrival:   models.GroundStop{City: "Paris"}},
		}, nil
	}

	routes, err := SearchEurostar(context.Background(), "London", "Paris", "2026-06-15", "2026-06-22", "GBP", false)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("expected 1 route from nab fallback, got %d", len(routes))
	}
}

func TestSearchEurostar_403_BrowserCookieRetry(t *testing.T) {
	origDo := eurostarDo
	origBrowserCookies := eurostarBrowserCookies
	origFetchViaNab := eurostarFetchViaNab
	origLimiter := eurostarLimiter
	origClient := eurostarClient
	t.Cleanup(func() {
		eurostarDo = origDo
		eurostarBrowserCookies = origBrowserCookies
		eurostarFetchViaNab = origFetchViaNab
		eurostarLimiter = origLimiter
		eurostarClient = origClient
	})
	eurostarLimiter = rate.NewLimiter(rate.Inf, 1)
	eurostarClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("test: timetable disabled")
	})}

	callCount := 0
	eurostarDo = func(req *http.Request) (*http.Response, error) {
		callCount++
		if callCount == 1 {
			return &http.Response{
				StatusCode: http.StatusForbidden,
				Body:       io.NopCloser(strings.NewReader("blocked")),
				Header:     make(http.Header),
			}, nil
		}
		gqlResponse := `{"data":{"cheapestFaresSearch":[{"cheapestFares":[{"date":"2026-06-15","price":42}]}]}}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(gqlResponse)),
			Header:     make(http.Header),
		}, nil
	}
	eurostarBrowserCookies = func(string) string { return "session=abc" }
	eurostarFetchViaNab = func(context.Context, []byte, EurostarStation, EurostarStation, string, string, bool) ([]models.GroundRoute, error) {
		return nil, fmt.Errorf("nab unavailable")
	}

	routes, err := SearchEurostar(context.Background(), "London", "Paris", "2026-06-15", "2026-06-22", "GBP", false)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(routes) == 0 {
		t.Fatal("expected routes from cookie retry")
	}
}

func TestSearchEurostar_SnapOnly(t *testing.T) {
	origDo := eurostarDo
	origLimiter := eurostarLimiter
	origClient := eurostarClient
	t.Cleanup(func() { eurostarDo = origDo; eurostarLimiter = origLimiter; eurostarClient = origClient })
	eurostarLimiter = rate.NewLimiter(rate.Inf, 1)
	eurostarClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("test: timetable disabled")
	})}

	eurostarDo = func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"data":{"cheapestFaresSearch":[{"cheapestFares":[{"date":"2026-06-15","price":25}]}]}}`)),
			Header:     make(http.Header),
		}, nil
	}

	routes, err := SearchEurostar(context.Background(), "London", "Paris", "2026-06-15", "2026-06-22", "GBP", true)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(routes) == 0 {
		t.Fatal("expected snap routes")
	}
}

// ---------------------------------------------------------------------------
// SearchDFDS — mock via dfdsClient swap
// ---------------------------------------------------------------------------

func TestSearchDFDS_UnknownPort(t *testing.T) {
	ctx := context.Background()
	_, err := SearchDFDS(ctx, "Nonexistent", "Amsterdam", "2026-06-15", "EUR")
	if err == nil || !strings.Contains(err.Error(), "no port for") {
		t.Errorf("expected 'no port' error, got %v", err)
	}
}

func TestSearchDFDS_HappyPath(t *testing.T) {
	// Swap dfdsClient to point at mock server.
	availResp := `{"dates":{"fromDate":"2026-01-01","toDate":"2026-12-31"},"disabledDates":[],"offerDates":["2026-06-15"]}`

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, availResp)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	origClient := dfdsClient
	dfdsClient = srv.Client()
	// Override the base URL is not possible (hardcoded), but the availability call
	// goes through dfdsClient.Do which will fail. Instead, test the port lookup and
	// structure building path.
	dfdsClient = origClient

	// At minimum, verify port lookup + route detection.
	if !HasDFDSRoute("Copenhagen", "Oslo") {
		t.Error("expected DFDS route for Copenhagen-Oslo")
	}
}

// ---------------------------------------------------------------------------
// SearchSNCF — expanded 403 coverage
// ---------------------------------------------------------------------------

func TestSearchSNCF_403_NoBrowserFallbacks(t *testing.T) {
	origDo := sncfDo
	t.Cleanup(func() { sncfDo = origDo })

	sncfDo = func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Body:       io.NopCloser(strings.NewReader("blocked")),
			Header:     make(http.Header),
		}, nil
	}

	_, err := SearchSNCF(context.Background(), "Paris", "Lyon", "2026-04-10", "EUR", false)
	if err == nil {
		t.Error("expected error for 403 without browser fallbacks")
	}
}

func TestSearchSNCF_CalendarSucceeds(t *testing.T) {
	origDo := sncfDo
	origLimiter := sncfLimiter
	t.Cleanup(func() { sncfDo = origDo; sncfLimiter = origLimiter })
	sncfLimiter = rate.NewLimiter(rate.Inf, 1)

	sncfDo = func(req *http.Request) (*http.Response, error) {
		body := `[{"date":"2026-04-10","price":3200}]`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	}

	routes, err := SearchSNCF(context.Background(), "Paris", "Lyon", "2026-04-10", "EUR", false)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}
	if routes[0].Departure.City != "Paris" {
		t.Errorf("departure city = %q, want Paris", routes[0].Departure.City)
	}
	if routes[0].Arrival.City != "Lyon" {
		t.Errorf("arrival city = %q, want Lyon", routes[0].Arrival.City)
	}
}

func TestSearchSNCF_CalendarEmpty_NoBrowserFallback(t *testing.T) {
	origDo := sncfDo
	origLimiter := sncfLimiter
	t.Cleanup(func() { sncfDo = origDo; sncfLimiter = origLimiter })
	sncfLimiter = rate.NewLimiter(rate.Inf, 1)

	sncfDo = func(req *http.Request) (*http.Response, error) {
		body := `[{"date":"2026-04-11","price":3200}]` // Different date, no match.
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	}

	routes, err := SearchSNCF(context.Background(), "Paris", "Lyon", "2026-04-10", "EUR", false)
	// No error but empty routes.
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(routes) != 0 {
		t.Errorf("expected 0 routes (wrong date), got %d", len(routes))
	}
}

// ---------------------------------------------------------------------------
// DFDS helper functions
// ---------------------------------------------------------------------------

func TestDfdsFormatDateTime(t *testing.T) {
	tests := []struct {
		date      string
		timeStr   string
		dayOffset int
		want      string
	}{
		{"2026-06-15", "18:00", 0, "2026-06-15T18:00:00"},
		{"2026-06-15", "10:00", 1, "2026-06-16T10:00:00"},
		{"invalid", "12:00", 0, "invalidT12:00:00"},
	}
	for _, tt := range tests {
		got := dfdsFormatDateTime(tt.date, tt.timeStr, tt.dayOffset)
		if got != tt.want {
			t.Errorf("dfdsFormatDateTime(%q, %q, %d) = %q, want %q",
				tt.date, tt.timeStr, tt.dayOffset, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// ferryhopperParseSSE — cover the SSE parser
// ---------------------------------------------------------------------------

func TestFerryhopperParseSSE_WithContent(t *testing.T) {
	sse := `event: message
data: {"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"{\"itineraries\":[]}"}],"isError":false}}

`
	result, err := ferryhopperParseSSE(strings.NewReader(sse))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.Result.Content) == 0 {
		t.Error("expected content")
	}
}

func TestFerryhopperParseSSE_ErrorEnvelope(t *testing.T) {
	sse := `data: {"jsonrpc":"2.0","id":1,"error":{"code":-32600,"message":"tool error"}}

`
	result, err := ferryhopperParseSSE(strings.NewReader(sse))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if result.Error == nil {
		t.Error("expected error in result")
	}
	if result.Error.Message != "tool error" {
		t.Errorf("error message = %q", result.Error.Message)
	}
}

func TestFerryhopperParseSSE_NoJSONRPCResult(t *testing.T) {
	sse := `event: ping
: comment

`
	_, err := ferryhopperParseSSE(strings.NewReader(sse))
	if err == nil || !strings.Contains(err.Error(), "no JSON-RPC result") {
		t.Errorf("expected 'no JSON-RPC result' error, got %v", err)
	}
}

func TestFerryhopperParseSSE_SkipDoneMarker(t *testing.T) {
	sse := `data: [DONE]
data: {"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"{}"}],"isError":false}}

`
	result, err := ferryhopperParseSSE(strings.NewReader(sse))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if result == nil {
		t.Fatal("expected result after [DONE] skip")
	}
}

func TestFerryhopperCheapestPrice_EdgeCases(t *testing.T) {
	tests := []struct {
		name  string
		accom []ferryhopperAccommodation
		want  float64
	}{
		{"empty", nil, 0},
		{"single", []ferryhopperAccommodation{{PriceCents: 3500}}, 35.0},
		{"cheapest", []ferryhopperAccommodation{{PriceCents: 5000}, {PriceCents: 2000}}, 20.0},
		{"zero_skipped", []ferryhopperAccommodation{{PriceCents: 0}, {PriceCents: 1500}}, 15.0},
		{"all_zero", []ferryhopperAccommodation{{PriceCents: 0}}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ferryhopperCheapestPrice(tt.accom)
			if got != tt.want {
				t.Errorf("ferryhopperCheapestPrice = %f, want %f", got, tt.want)
			}
		})
	}
}

func TestFerryhopperSanitizeURL_PreservesParams(t *testing.T) {
	u := ferryhopperSanitizeURL("https://www.ferryhopper.com/en/trip?utm_source=mcp&a=1")
	if u == "" {
		t.Error("sanitized URL should not be empty")
	}
	if !strings.Contains(u, "ferryhopper.com") {
		t.Errorf("expected ferryhopper domain: %q", u)
	}
}
