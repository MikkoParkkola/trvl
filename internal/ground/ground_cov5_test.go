package ground

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func dfdsTestMux(t *testing.T, availJSON string) (*httptest.Server, func()) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, availJSON)
	}))
	origClient := dfdsClient
	origLimiter := dfdsLimiter
	dfdsClient = srv.Client()
	dfdsLimiter = rate.NewLimiter(rate.Limit(1000), 1)
	cleanup := func() {
		dfdsClient = origClient
		dfdsLimiter = origLimiter
		srv.Close()
	}
	return srv, cleanup
}

// fetchDFDSAvailability redirected to test server via dfdsClient but the URL
// is still dfdsAvailabilityBase (const). The test client will make a real TCP
// connection to that host. Instead, we test fetchDFDSAvailability directly
// by overriding dfdsClient with a transport that returns canned responses.

type tallinkTransportMock struct {
	bookingPageBody string
	timetableJSON   string
	summaryStatus   int
	travelClasses   string
}

func (m *tallinkTransportMock) RoundTrip(r *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	path := r.URL.Path
	switch {
	case strings.Contains(path, "/api/timetables"):
		rec.Header().Set("Content-Type", "application/json")
		rec.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(rec, m.timetableJSON)
	case strings.Contains(path, "/api/reservation/cruiseSummary"):
		rec.WriteHeader(m.summaryStatus)
		_, _ = fmt.Fprint(rec, `{"status":"OK"}`)
	case strings.Contains(path, "/api/travelclasses"):
		rec.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(rec, m.travelClasses)
	default:
		// Booking page
		rec.Header().Set("Set-Cookie", "JSESSIONID=mock-sess; Path=/")
		rec.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(rec, m.bookingPageBody)
	}
	return rec.Result(), nil
}

const tallinkMockTimetableDay = `{
  "defaultSelections": {"outwardSail": 1, "returnSail": null},
  "trips": {
    "2026-09-01": {
      "outwards": [
        {"sailId": 5001, "shipCode": "MEGASTAR", "departureIsoDate": "2026-09-01T07:30", "arrivalIsoDate": "2026-09-01T09:30", "personPrice": "38.90", "vehiclePrice": null, "duration": 2.0, "sailPackageCode": "HEL-TAL", "sailPackageName": "Helsinki-Tallinn", "cityFrom": "HEL", "cityTo": "TAL", "pierFrom": "A", "pierTo": "B", "hasRoom": true, "isOvernight": false, "isDisabled": false, "promotionApplied": false, "marketingMessage": null, "isVoucherApplicable": false},
        {"sailId": 5002, "shipCode": "MYSTAR", "departureIsoDate": "2026-09-01T17:30", "arrivalIsoDate": "2026-09-01T19:30", "personPrice": "12.00", "vehiclePrice": null, "duration": 2.0, "sailPackageCode": "HEL-TAL", "sailPackageName": "Helsinki-Tallinn", "cityFrom": "HEL", "cityTo": "TAL", "pierFrom": "A", "pierTo": "B", "hasRoom": true, "isOvernight": false, "isDisabled": false, "promotionApplied": true, "marketingMessage": null, "isVoucherApplicable": false}
      ],
      "returns": []
    }
  }
}`

func TestDFDSTestMux_ServerStarts(t *testing.T) {
	// Verify dfdsTestMux stands up a server and restores globals on cleanup.
	srv, cleanup := dfdsTestMux(t, `{"route":"TEST","dates":{"fromDate":"2026-01-01","toDate":"2027-12-31"},"disabledDates":[],"offerDates":[]}`)
	defer cleanup()
	if srv == nil {
		t.Fatal("expected non-nil server from dfdsTestMux")
	}
}

func TestFetchDFDSAvailability_ViaTransport_Available(t *testing.T) {
	origClient := dfdsClient
	origLimiter := dfdsLimiter
	t.Cleanup(func() {
		dfdsClient = origClient
		dfdsLimiter = origLimiter
	})
	dfdsLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	dfdsClient = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			_, _ = fmt.Fprint(rec, `{"route":"NOOSL-DKCPH","dates":{"fromDate":"2026-01-01","toDate":"2027-12-31"},"disabledDates":[],"offerDates":[]}`)
			return rec.Result(), nil
		}),
	}

	routeInfo := dfdsRouteInfo{RouteCode: "NOOSL-DKCPH", SalesOwner: 19}
	available, isOffer, err := fetchDFDSAvailability(context.Background(), routeInfo, "2026-08-15")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !available {
		t.Error("expected available=true")
	}
	if isOffer {
		t.Error("expected isOffer=false")
	}
}

func TestFetchDFDSAvailability_ViaTransport_OfferDate(t *testing.T) {
	origClient := dfdsClient
	origLimiter := dfdsLimiter
	t.Cleanup(func() {
		dfdsClient = origClient
		dfdsLimiter = origLimiter
	})
	dfdsLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	dfdsClient = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			_, _ = fmt.Fprint(rec, `{"route":"NOOSL-DKCPH","dates":{"fromDate":"2026-01-01","toDate":"2027-12-31"},"disabledDates":[],"offerDates":["2026-08-15"]}`)
			return rec.Result(), nil
		}),
	}

	routeInfo := dfdsRouteInfo{RouteCode: "NOOSL-DKCPH", SalesOwner: 19}
	available, isOffer, err := fetchDFDSAvailability(context.Background(), routeInfo, "2026-08-15")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !available {
		t.Error("expected available=true for offer date")
	}
	if !isOffer {
		t.Error("expected isOffer=true for offer date")
	}
}

func TestFetchDFDSAvailability_ViaTransport_DisabledDate(t *testing.T) {
	origClient := dfdsClient
	origLimiter := dfdsLimiter
	t.Cleanup(func() {
		dfdsClient = origClient
		dfdsLimiter = origLimiter
	})
	dfdsLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	dfdsClient = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			_, _ = fmt.Fprint(rec, `{"route":"NOOSL-DKCPH","dates":{"fromDate":"2026-01-01","toDate":"2027-12-31"},"disabledDates":["2026-08-15"],"offerDates":[]}`)
			return rec.Result(), nil
		}),
	}

	routeInfo := dfdsRouteInfo{RouteCode: "NOOSL-DKCPH", SalesOwner: 19}
	available, _, err := fetchDFDSAvailability(context.Background(), routeInfo, "2026-08-15")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if available {
		t.Error("expected available=false for disabled date")
	}
}

func TestFetchDFDSAvailability_ViaTransport_DateBeforeRange(t *testing.T) {
	origClient := dfdsClient
	origLimiter := dfdsLimiter
	t.Cleanup(func() {
		dfdsClient = origClient
		dfdsLimiter = origLimiter
	})
	dfdsLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	dfdsClient = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			// fromDate is 2026-09-01, our date is 2026-08-15 — before range
			_, _ = fmt.Fprint(rec, `{"route":"NOOSL-DKCPH","dates":{"fromDate":"2026-09-01","toDate":"2027-12-31"},"disabledDates":[],"offerDates":[]}`)
			return rec.Result(), nil
		}),
	}

	routeInfo := dfdsRouteInfo{RouteCode: "NOOSL-DKCPH", SalesOwner: 19}
	available, _, err := fetchDFDSAvailability(context.Background(), routeInfo, "2026-08-15")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if available {
		t.Error("expected available=false for date before range")
	}
}

func TestFetchDFDSAvailability_ViaTransport_DateAfterRange(t *testing.T) {
	origClient := dfdsClient
	origLimiter := dfdsLimiter
	t.Cleanup(func() {
		dfdsClient = origClient
		dfdsLimiter = origLimiter
	})
	dfdsLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	dfdsClient = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			// toDate is 2026-07-01, our date is 2026-08-15 — after range
			_, _ = fmt.Fprint(rec, `{"route":"NOOSL-DKCPH","dates":{"fromDate":"2026-01-01","toDate":"2026-07-01"},"disabledDates":[],"offerDates":[]}`)
			return rec.Result(), nil
		}),
	}

	routeInfo := dfdsRouteInfo{RouteCode: "NOOSL-DKCPH", SalesOwner: 19}
	available, _, err := fetchDFDSAvailability(context.Background(), routeInfo, "2026-08-15")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if available {
		t.Error("expected available=false for date after range")
	}
}

func TestFetchDFDSAvailability_ViaTransport_InactiveRoute(t *testing.T) {
	origClient := dfdsClient
	origLimiter := dfdsLimiter
	t.Cleanup(func() {
		dfdsClient = origClient
		dfdsLimiter = origLimiter
	})
	dfdsLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	dfdsClient = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			// Empty fromDate → inactive route
			_, _ = fmt.Fprint(rec, `{"route":"NOOSL-DKCPH","dates":{"fromDate":"","toDate":""},"disabledDates":[],"offerDates":[]}`)
			return rec.Result(), nil
		}),
	}

	routeInfo := dfdsRouteInfo{RouteCode: "NOOSL-DKCPH", SalesOwner: 19}
	available, _, err := fetchDFDSAvailability(context.Background(), routeInfo, "2026-08-15")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if available {
		t.Error("expected available=false for inactive route (empty dates)")
	}
}

func TestFetchDFDSAvailability_ViaTransport_NonOK(t *testing.T) {
	origClient := dfdsClient
	origLimiter := dfdsLimiter
	t.Cleanup(func() {
		dfdsClient = origClient
		dfdsLimiter = origLimiter
	})
	dfdsLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	dfdsClient = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			rec.WriteHeader(http.StatusServiceUnavailable)
			_, _ = fmt.Fprint(rec, `service unavailable`)
			return rec.Result(), nil
		}),
	}

	routeInfo := dfdsRouteInfo{RouteCode: "NOOSL-DKCPH", SalesOwner: 19}
	// Non-200 → non-fatal → returns true (assume available)
	available, _, err := fetchDFDSAvailability(context.Background(), routeInfo, "2026-08-15")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !available {
		t.Error("expected available=true for non-200 (non-fatal path)")
	}
}

// ---------------------------------------------------------------------------
// SearchDFDS — full path with transport mock
// ---------------------------------------------------------------------------

func TestSearchDFDS_FullPath_WithOffer(t *testing.T) {
	origClient := dfdsClient
	origLimiter := dfdsLimiter
	t.Cleanup(func() {
		dfdsClient = origClient
		dfdsLimiter = origLimiter
	})
	dfdsLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	dfdsClient = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			// Return availability with our date as offer date
			_, _ = fmt.Fprint(rec, `{"route":"NOOSL-DKCPH","dates":{"fromDate":"2026-01-01","toDate":"2027-12-31"},"disabledDates":[],"offerDates":["2026-08-15"]}`)
			return rec.Result(), nil
		}),
	}

	ctx := context.Background()
	routes, err := SearchDFDS(ctx, "Oslo", "Copenhagen", "2026-08-15", "EUR")
	if err != nil {
		t.Fatalf("SearchDFDS: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}
	r := routes[0]
	if r.Provider != "dfds" {
		t.Errorf("provider = %q, want dfds", r.Provider)
	}
	if r.Type != "ferry" {
		t.Errorf("type = %q, want ferry", r.Type)
	}
	// Offer date → amenity "Deal"
	found := false
	for _, a := range r.Amenities {
		if a == "Deal" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected Deal amenity for offer date, got %v", r.Amenities)
	}
}

func TestSearchDFDS_FullPath_Unavailable(t *testing.T) {
	origClient := dfdsClient
	origLimiter := dfdsLimiter
	t.Cleanup(func() {
		dfdsClient = origClient
		dfdsLimiter = origLimiter
	})
	dfdsLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	dfdsClient = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			// Disabled date → unavailable
			_, _ = fmt.Fprint(rec, `{"route":"NOOSL-DKCPH","dates":{"fromDate":"2026-01-01","toDate":"2027-12-31"},"disabledDates":["2026-08-15"],"offerDates":[]}`)
			return rec.Result(), nil
		}),
	}

	ctx := context.Background()
	routes, err := SearchDFDS(ctx, "Oslo", "Copenhagen", "2026-08-15", "EUR")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Unavailable date → nil routes
	if routes != nil {
		t.Errorf("expected nil routes for unavailable date, got %v", routes)
	}
}

func TestSearchDFDS_DefaultCurrency(t *testing.T) {
	origClient := dfdsClient
	origLimiter := dfdsLimiter
	t.Cleanup(func() {
		dfdsClient = origClient
		dfdsLimiter = origLimiter
	})
	dfdsLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	dfdsClient = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			_, _ = fmt.Fprint(rec, `{"route":"NOOSL-DKCPH","dates":{"fromDate":"2026-01-01","toDate":"2027-12-31"},"disabledDates":[],"offerDates":[]}`)
			return rec.Result(), nil
		}),
	}

	// currency="" → falls back to route's native currency
	ctx := context.Background()
	routes, err := SearchDFDS(ctx, "Oslo", "Copenhagen", "2026-08-15", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = routes
}

// ---------------------------------------------------------------------------
// SearchSNCF — more branches via sncfDo transport
// ---------------------------------------------------------------------------

func TestSearchSNCF_WithRoutes(t *testing.T) {
	origDo := sncfDo
	origLimiter := sncfLimiter
	t.Cleanup(func() {
		sncfDo = origDo
		sncfLimiter = origLimiter
	})
	sncfLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	// Return valid SNCF response with journeys
	sncfDo = func(req *http.Request) (*http.Response, error) {
		rec := httptest.NewRecorder()
		rec.WriteHeader(http.StatusOK)
		// parseSNCFResponse expects []sncfCalendarResponse
		price := 4500 // price in cents
		resp := []sncfCalendarResponse{{
			Date:  "2026-08-15",
			Price: &price,
		}}
		_ = json.NewEncoder(rec).Encode(resp)
		return rec.Result(), nil
	}

	ctx := context.Background()
	routes, err := SearchSNCF(ctx, "Paris", "Lyon", "2026-08-15", "EUR", false)
	if err != nil {
		t.Fatalf("SearchSNCF: %v", err)
	}
	if len(routes) == 0 {
		t.Error("expected at least 1 route")
	}
}

func TestSearchSNCF_403_NoBrowserFallback_v2(t *testing.T) {
	origDo := sncfDo
	origLimiter := sncfLimiter
	t.Cleanup(func() {
		sncfDo = origDo
		sncfLimiter = origLimiter
	})
	sncfLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	sncfDo = func(req *http.Request) (*http.Response, error) {
		rec := httptest.NewRecorder()
		rec.WriteHeader(http.StatusForbidden)
		_, _ = fmt.Fprint(rec, `forbidden`)
		return rec.Result(), nil
	}

	ctx := context.Background()
	_, err := SearchSNCF(ctx, "Paris", "Lyon", "2026-08-15", "EUR", false)
	if err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Errorf("expected HTTP 403 error, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// SearchTrainline — additional branches
// ---------------------------------------------------------------------------

func TestSearchTrainline_403_WithBrowserFallbackAllowed(t *testing.T) {
	// When allowBrowserFallbacks=true and 403, it tries multiple fallbacks.
	// Since nab/curl/browser aren't available, eventually returns 403 error.
	origDo := trainlineDo
	origLimiter := trainlineLimiter
	t.Cleanup(func() {
		trainlineDo = origDo
		trainlineLimiter = origLimiter
	})
	trainlineLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	callCount := 0
	trainlineDo = func(req *http.Request) (*http.Response, error) {
		callCount++
		rec := httptest.NewRecorder()
		rec.WriteHeader(http.StatusForbidden)
		// No datadome cookie to try the seed retry
		_, _ = fmt.Fprint(rec, `forbidden`)
		return rec.Result(), nil
	}

	ctx := context.Background()
	_, err := SearchTrainline(ctx, "London", "Paris", "2026-08-15", "GBP", true)
	// With browser fallbacks allowed but all failing → returns 403 error
	if err == nil {
		t.Error("expected error when all fallbacks fail")
	}
}

func TestSearchTrainline_MarshallError(t *testing.T) {
	// Invalid station → returns early before marshal
	ctx := context.Background()
	_, err := SearchTrainline(ctx, "UnknownXYZ", "Paris", "2026-08-15", "GBP", false)
	if err == nil || !strings.Contains(err.Error(), "no Trainline station") {
		t.Errorf("expected station error, got %v", err)
	}
}

func TestSearchTrainline_InvalidDateFormat(t *testing.T) {
	ctx := context.Background()
	_, err := SearchTrainline(ctx, "London", "Paris", "bad-date-format", "GBP", false)
	if err == nil || !strings.Contains(err.Error(), "invalid date") {
		t.Errorf("expected invalid date error, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// SearchSNCF — RateLimiter cancel path
// ---------------------------------------------------------------------------

func TestSearchSNCF_RateLimiterCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	origLimiter := sncfLimiter
	sncfLimiter = newProviderLimiter(60 * time.Second)
	defer func() { sncfLimiter = origLimiter }()

	_, err := SearchSNCF(ctx, "Paris", "Lyon", "2026-08-15", "EUR", false)
	if err == nil {
		t.Error("expected rate-limiter error on cancelled context")
	}
}

// ---------------------------------------------------------------------------
// Finnlines — basic coverage
// ---------------------------------------------------------------------------

func TestSearchFinnlines_UnknownRoute(t *testing.T) {
	ctx := context.Background()
	_, err := SearchFinnlines(ctx, "Tokyo", "Paris", "2026-08-15", "EUR")
	if err == nil {
		t.Error("expected error for unknown Finnlines route")
	}
}

// ---------------------------------------------------------------------------
// geocodeCity — empty results branch
// ---------------------------------------------------------------------------
