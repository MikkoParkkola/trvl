package ground

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestSearchSNCF_DI_UnknownStation(t *testing.T) {
	_, err := SearchSNCF(context.Background(), "Timbuktu", "Paris", "2026-08-15", "EUR", false)
	if err == nil {
		t.Fatal("expected error for unknown station")
	}
	if !strings.Contains(err.Error(), "no SNCF station") {
		t.Errorf("error = %q, want 'no SNCF station'", err.Error())
	}
}

// ---------------------------------------------------------------------------
// Trainline — full SearchTrainline via httptest
// ---------------------------------------------------------------------------

// TestSearchTrainline_DI_HappyPath tests SearchTrainline with a canned
// journey-search response matching the real API structure.

func TestSearchTrainline_DI_HappyPath(t *testing.T) {
	origDo := trainlineDo
	origBrowserCookies := trainlineBrowserCookies
	origLimiter := trainlineLimiter
	t.Cleanup(func() {
		trainlineDo = origDo
		trainlineBrowserCookies = origBrowserCookies
		trainlineLimiter = origLimiter
	})
	trainlineLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(trainlineJourneySearchResponse{
			Journeys: []trainlineJourney{
				{
					ID:            "j-1",
					DepartureTime: "2026-07-10T07:00:00+02:00",
					ArrivalTime:   "2026-07-10T10:15:00+02:00",
					Legs: []trainlineLeg{
						{
							DepartureTime: "2026-07-10T07:00:00+02:00",
							ArrivalTime:   "2026-07-10T10:15:00+02:00",
							TransportMode: "train",
							Carrier:       "Eurostar",
						},
					},
					TicketIDs: []string{"t-1"},
				},
				{
					ID:            "j-2",
					DepartureTime: "2026-07-10T12:00:00+02:00",
					ArrivalTime:   "2026-07-10T16:30:00+02:00",
					Legs: []trainlineLeg{
						{
							DepartureTime: "2026-07-10T12:00:00+02:00",
							ArrivalTime:   "2026-07-10T14:00:00+02:00",
							TransportMode: "train",
						},
						{
							DepartureTime: "2026-07-10T14:30:00+02:00",
							ArrivalTime:   "2026-07-10T16:30:00+02:00",
							TransportMode: "bus",
						},
					},
					TicketIDs: []string{"t-2"},
				},
			},
			Tickets: []trainlineTicket{
				{
					ID:         "t-1",
					JourneyIDs: []string{"j-1"},
					Prices:     []trainlinePrice{{Amount: 79.00, Currency: "GBP"}},
				},
				{
					ID:         "t-2",
					JourneyIDs: []string{"j-2"},
					Prices:     []trainlinePrice{{Amount: 45.50, Currency: "GBP"}},
				},
			},
		})
	}))
	defer srv.Close()

	trainlineDo = func(req *http.Request) (*http.Response, error) {
		mockURL := srv.URL + req.URL.Path
		mockReq, err := http.NewRequestWithContext(req.Context(), req.Method, mockURL, req.Body)
		if err != nil {
			return nil, err
		}
		mockReq.Header = req.Header
		return http.DefaultClient.Do(mockReq)
	}
	trainlineBrowserCookies = func(string) string { return "" }

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	routes, err := SearchTrainline(ctx, "london", "paris", "2026-07-10", "GBP", false)
	if err != nil {
		t.Fatalf("SearchTrainline: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(routes))
	}

	r0 := routes[0]
	if r0.Provider != "trainline" {
		t.Errorf("provider = %q, want 'trainline'", r0.Provider)
	}
	if r0.Price != 79.00 {
		t.Errorf("price = %.2f, want 79.00", r0.Price)
	}
	if r0.Currency != "GBP" {
		t.Errorf("currency = %q, want 'GBP'", r0.Currency)
	}
	if r0.Type != "train" {
		t.Errorf("type = %q, want 'train'", r0.Type)
	}
	if r0.Transfers != 0 {
		t.Errorf("transfers = %d, want 0 (1 leg)", r0.Transfers)
	}
	if r0.Departure.City != "london" {
		t.Errorf("departure city = %q, want 'london'", r0.Departure.City)
	}

	r1 := routes[1]
	if r1.Price != 45.50 {
		t.Errorf("price = %.2f, want 45.50", r1.Price)
	}
	if r1.Type != "mixed" {
		t.Errorf("type = %q, want 'mixed' (train+bus)", r1.Type)
	}
	if r1.Transfers != 1 {
		t.Errorf("transfers = %d, want 1 (2 legs)", r1.Transfers)
	}
	if !strings.Contains(r1.BookingURL, "thetrainline.com") {
		t.Errorf("booking URL = %q, should contain 'thetrainline.com'", r1.BookingURL)
	}
}

// TestSearchTrainline_DI_HTTP403_NoFallback verifies that a 403 with
// allowBrowserFallbacks=false returns an error without attempting fallbacks.

func TestSearchTrainline_DI_HTTP403_NoFallback(t *testing.T) {
	origDo := trainlineDo
	origBrowserCookies := trainlineBrowserCookies
	origLimiter := trainlineLimiter
	t.Cleanup(func() {
		trainlineDo = origDo
		trainlineBrowserCookies = origBrowserCookies
		trainlineLimiter = origLimiter
	})
	trainlineLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"blocked"}`))
	}))
	defer srv.Close()

	trainlineDo = func(req *http.Request) (*http.Response, error) {
		mockURL := srv.URL + req.URL.Path
		mockReq, _ := http.NewRequestWithContext(req.Context(), req.Method, mockURL, req.Body)
		mockReq.Header = req.Header
		return http.DefaultClient.Do(mockReq)
	}
	trainlineBrowserCookies = func(string) string { return "" }

	_, err := SearchTrainline(context.Background(), "london", "paris", "2026-07-10", "GBP", false)
	if err == nil {
		t.Fatal("expected error for 403 without fallbacks")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("error = %q, should mention 403", err.Error())
	}
}

// ---------------------------------------------------------------------------
// Eckeroline — live API via httptest
// ---------------------------------------------------------------------------

// TestSearchEckeroLine_DI_LiveAPI tests SearchEckeroLine by mocking the
// Eckeroline Magento API (homepage + getdepartures).

func TestSearchEckeroLine_DI_LiveAPI(t *testing.T) {
	origClient := eckerolineClient
	origLimiter := eckerolineLimiter
	t.Cleanup(func() {
		eckerolineClient = origClient
		eckerolineLimiter = origLimiter
	})
	eckerolineLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	reqCount := 0
	var mu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		reqCount++
		currentReq := reqCount
		mu.Unlock()

		if r.URL.Path == "/" && r.Method == http.MethodGet {
			// Homepage: return HTML with form_key and set session cookie.
			http.SetCookie(w, &http.Cookie{
				Name:  "PHPSESSID",
				Value: "test-session-123",
			})
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><input name="form_key" type="hidden" value="testkey123"/></html>`))
			return
		}

		if strings.Contains(r.URL.Path, "getdepartures") && r.Method == http.MethodPost {
			// Verify session cookie is forwarded.
			cookie := r.Header.Get("Cookie")
			if !strings.Contains(cookie, "PHPSESSID") {
				t.Errorf("request %d: expected PHPSESSID cookie, got %q", currentReq, cookie)
			}

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]eckerolineDeparture{
				{Time: "09:00", Price: 22.50, Ship: "M/S Finlandia"},
				{Time: "15:15", Price: 29.00, Ship: "M/S Finlandia"},
			})
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	eckerolineClient = &http.Client{
		Transport: &urlRewriter{base: srv.URL},
		Timeout:   5 * time.Second,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	routes, err := SearchEckeroLine(ctx, "Helsinki", "Tallinn", "2026-07-15", "EUR")
	if err != nil {
		t.Fatalf("SearchEckeroLine: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("expected 2 routes from live API, got %d", len(routes))
	}

	r0 := routes[0]
	if r0.Provider != "eckeroline" {
		t.Errorf("provider = %q, want 'eckeroline'", r0.Provider)
	}
	if r0.Type != "ferry" {
		t.Errorf("type = %q, want 'ferry'", r0.Type)
	}
	if r0.Price != 22.50 {
		t.Errorf("price = %.2f, want 22.50", r0.Price)
	}
	if r0.Departure.City != "Helsinki" {
		t.Errorf("departure city = %q, want 'Helsinki'", r0.Departure.City)
	}
	if r0.Arrival.City != "Tallinn" {
		t.Errorf("arrival city = %q, want 'Tallinn'", r0.Arrival.City)
	}
	if !strings.Contains(r0.Departure.Time, "09:00") {
		t.Errorf("departure time = %q, should contain '09:00'", r0.Departure.Time)
	}
	// Live results should have "Live" amenity tag.
	foundLive := false
	for _, a := range r0.Amenities {
		if a == "Live" {
			foundLive = true
		}
	}
	if !foundLive {
		t.Errorf("expected 'Live' amenity tag, got %v", r0.Amenities)
	}

	r1 := routes[1]
	if r1.Price != 29.00 {
		t.Errorf("price = %.2f, want 29.00", r1.Price)
	}
}

// TestSearchEckeroLine_DI_FallbackToSchedule verifies that when the live
// API returns an error or empty results, SearchEckeroLine falls back to
// the published timetable with reference prices.

func TestSearchEckeroLine_DI_FallbackToSchedule(t *testing.T) {
	origClient := eckerolineClient
	origLimiter := eckerolineLimiter
	t.Cleanup(func() {
		eckerolineClient = origClient
		eckerolineLimiter = origLimiter
	})
	eckerolineLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return 500 for all requests to trigger fallback.
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	eckerolineClient = &http.Client{
		Transport: &urlRewriter{base: srv.URL},
		Timeout:   5 * time.Second,
	}

	// Use a Wednesday (2026-07-15 is a Wednesday) — all "daily" sailings match.
	routes, err := SearchEckeroLine(context.Background(), "Helsinki", "Tallinn", "2026-07-15", "EUR")
	if err != nil {
		t.Fatalf("SearchEckeroLine fallback: %v", err)
	}
	if len(routes) == 0 {
		t.Fatal("expected fallback schedule routes, got 0")
	}

	// All fallback routes should have the base price.
	for _, r := range routes {
		if r.Price != eckerolineBasePrice {
			t.Errorf("fallback price = %.2f, want %.2f", r.Price, eckerolineBasePrice)
		}
		if r.Provider != "eckeroline" {
			t.Errorf("provider = %q, want 'eckeroline'", r.Provider)
		}
	}

	// Verify "Reference" amenity tag on fallback routes.
	foundRef := false
	for _, a := range routes[0].Amenities {
		if a == "Reference" {
			foundRef = true
		}
	}
	if !foundRef {
		t.Errorf("expected 'Reference' amenity tag on fallback, got %v", routes[0].Amenities)
	}
}

// ---------------------------------------------------------------------------
// urlRewriter: test helper to redirect all HTTP requests to a test server
// ---------------------------------------------------------------------------

// urlRewriter is an http.RoundTripper that rewrites request URLs to point
// at a local test server, preserving the original path and query string.
