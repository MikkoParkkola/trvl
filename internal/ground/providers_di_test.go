package ground

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

type urlRewriter struct {
	base string
}

func (u *urlRewriter) RoundTrip(req *http.Request) (*http.Response, error) {
	newURL := u.base + req.URL.Path
	if req.URL.RawQuery != "" {
		newURL += "?" + req.URL.RawQuery
	}
	newReq, err := http.NewRequestWithContext(req.Context(), req.Method, newURL, req.Body)
	if err != nil {
		return nil, err
	}
	newReq.Header = req.Header
	return http.DefaultTransport.RoundTrip(newReq)
}

func TestSearchFlixBus_DI_HappyPath(t *testing.T) {
	origClient := httpClient
	origLimiter := flixbusLimiter
	t.Cleanup(func() {
		httpClient = origClient
		flixbusLimiter = origLimiter
	})
	flixbusLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if strings.Contains(r.URL.Path, "/search/autocomplete") {
			// Autocomplete: return city info for the queried ID.
			q := r.URL.Query().Get("q")
			_ = json.NewEncoder(w).Encode([]flixbusAutocompleteItem{
				{
					ID: q, Name: "Berlin", Country: "DE",
					Score: 1.0, IsFlixbusCity: true,
					Location: struct {
						Lat float64 `json:"lat"`
						Lon float64 `json:"lon"`
					}{Lat: 52.52, Lon: 13.40},
				},
			})
			return
		}

		if strings.Contains(r.URL.Path, "/search/service") {
			_ = json.NewEncoder(w).Encode(flixbusSearchResponse{
				Trips: []flixbusTrip{
					{
						DepartureCityID: "city-berlin",
						ArrivalCityID:   "city-munich",
						Results: map[string]flixbusResult{
							"uid-1": {
								UID:    "uid-1",
								Status: "available",
								Departure: flixbusStop{
									Date:   "2026-07-01T08:00:00+02:00",
									CityID: "city-berlin",
								},
								Arrival: flixbusStop{
									Date:   "2026-07-01T14:30:00+02:00",
									CityID: "city-munich",
								},
								Duration:  flixbusDuration{Hours: 6, Minutes: 30},
								Price:     flixbusPrice{Total: 24.99, Original: 29.99},
								Available: flixbusAvailable{Seats: 12},
								Legs: []flixbusLeg{
									{
										Departure:        flixbusStop{Date: "2026-07-01T08:00:00+02:00", CityID: "city-berlin"},
										Arrival:          flixbusStop{Date: "2026-07-01T14:30:00+02:00", CityID: "city-munich"},
										MeansOfTransport: "bus",
										Amenities:        []any{"WiFi", "AC"},
									},
								},
							},
							"uid-2": {
								UID:    "uid-2",
								Status: "available",
								Departure: flixbusStop{
									Date:   "2026-07-01T10:00:00+02:00",
									CityID: "city-berlin",
								},
								Arrival: flixbusStop{
									Date:   "2026-07-01T15:00:00+02:00",
									CityID: "city-munich",
								},
								Duration: flixbusDuration{Hours: 5, Minutes: 0},
								Price:    flixbusPrice{Total: 19.99},
								Legs: []flixbusLeg{
									{
										Departure:        flixbusStop{Date: "2026-07-01T10:00:00+02:00", CityID: "city-berlin"},
										Arrival:          flixbusStop{Date: "2026-07-01T12:30:00+02:00", CityID: "city-berlin"},
										MeansOfTransport: "bus",
									},
									{
										Departure:        flixbusStop{Date: "2026-07-01T13:00:00+02:00", CityID: "city-berlin"},
										Arrival:          flixbusStop{Date: "2026-07-01T15:00:00+02:00", CityID: "city-munich"},
										MeansOfTransport: "train",
									},
								},
							},
						},
					},
				},
			})
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	// Redirect httpClient to our test server.
	httpClient = &http.Client{
		Transport: &urlRewriter{base: srv.URL},
		Timeout:   5 * time.Second,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	routes, err := SearchFlixBus(ctx, "city-berlin", "city-munich", "2026-07-01", SearchOptions{Currency: "EUR"})
	if err != nil {
		t.Fatalf("SearchFlixBus: %v", err)
	}

	if len(routes) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(routes))
	}

	// Routes are sorted by price: 19.99, then 24.99.
	r0 := routes[0]
	if r0.Provider != "flixbus" {
		t.Errorf("provider = %q, want 'flixbus'", r0.Provider)
	}
	if r0.Price != 19.99 {
		t.Errorf("price = %.2f, want 19.99", r0.Price)
	}
	if r0.Type != "mixed" {
		t.Errorf("type = %q, want 'mixed' (bus+train legs)", r0.Type)
	}
	if r0.Transfers != 1 {
		t.Errorf("transfers = %d, want 1 (2 legs)", r0.Transfers)
	}
	if r0.Duration != 300 {
		t.Errorf("duration = %d, want 300 (5h)", r0.Duration)
	}
	if r0.Currency != "EUR" {
		t.Errorf("currency = %q, want 'EUR'", r0.Currency)
	}

	r1 := routes[1]
	if r1.Price != 24.99 {
		t.Errorf("price = %.2f, want 24.99", r1.Price)
	}
	if r1.Type != "bus" {
		t.Errorf("type = %q, want 'bus'", r1.Type)
	}
	if r1.Transfers != 0 {
		t.Errorf("transfers = %d, want 0", r1.Transfers)
	}
	if r1.SeatsLeft == nil || *r1.SeatsLeft != 12 {
		t.Errorf("seatsLeft = %v, want 12", r1.SeatsLeft)
	}
	if r1.BookingURL == "" {
		t.Error("booking URL should not be empty")
	}
	if len(r1.Amenities) < 2 {
		t.Errorf("expected at least 2 amenities, got %d", len(r1.Amenities))
	}
}

// TestSearchFlixBus_DI_UnavailableFiltered verifies that FlixBus results with
// status != "available" are filtered out.

func TestSearchFlixBus_DI_UnavailableFiltered(t *testing.T) {
	origClient := httpClient
	origLimiter := flixbusLimiter
	t.Cleanup(func() {
		httpClient = origClient
		flixbusLimiter = origLimiter
	})
	flixbusLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if strings.Contains(r.URL.Path, "/search/autocomplete") {
			_ = json.NewEncoder(w).Encode([]flixbusAutocompleteItem{})
			return
		}

		_ = json.NewEncoder(w).Encode(flixbusSearchResponse{
			Trips: []flixbusTrip{
				{
					Results: map[string]flixbusResult{
						"uid-sold-out": {
							UID:       "uid-sold-out",
							Status:    "sold_out",
							Departure: flixbusStop{Date: "2026-07-01T08:00:00+02:00"},
							Arrival:   flixbusStop{Date: "2026-07-01T14:00:00+02:00"},
							Duration:  flixbusDuration{Hours: 6},
							Price:     flixbusPrice{Total: 15.00},
						},
					},
				},
			},
		})
	}))
	defer srv.Close()

	httpClient = &http.Client{
		Transport: &urlRewriter{base: srv.URL},
		Timeout:   5 * time.Second,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	routes, err := SearchFlixBus(ctx, "city-1", "city-2", "2026-07-01", SearchOptions{})
	if err != nil {
		t.Fatalf("SearchFlixBus: %v", err)
	}
	if len(routes) != 0 {
		t.Errorf("expected 0 routes (all sold out), got %d", len(routes))
	}
}

// TestFlixBusAutoComplete_DI tests the autocomplete endpoint via DI.

func TestFlixBusAutoComplete_DI(t *testing.T) {
	origClient := httpClient
	origLimiter := flixbusLimiter
	t.Cleanup(func() {
		httpClient = origClient
		flixbusLimiter = origLimiter
	})
	flixbusLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]flixbusAutocompleteItem{
			{ID: "city-1", Name: "Berlin", Country: "DE", Score: 1.0, IsFlixbusCity: true},
			{ID: "city-2", Name: "Bernau", Country: "DE", Score: 0.5, IsFlixbusCity: true},
			{ID: "station-x", Name: "Berlin HBF", Country: "DE", Score: 0.8, IsFlixbusCity: false}, // filtered
		})
	}))
	defer srv.Close()

	httpClient = &http.Client{
		Transport: &urlRewriter{base: srv.URL},
		Timeout:   5 * time.Second,
	}

	cities, err := FlixBusAutoComplete(context.Background(), "ber")
	if err != nil {
		t.Fatalf("FlixBusAutoComplete: %v", err)
	}
	if len(cities) != 2 {
		t.Fatalf("expected 2 cities (non-city filtered), got %d", len(cities))
	}
	if cities[0].Name != "Berlin" {
		t.Errorf("first city = %q, want 'Berlin'", cities[0].Name)
	}
}

// ---------------------------------------------------------------------------
// RegioJet — locations + search via httptest
// ---------------------------------------------------------------------------

// TestSearchRegioJet_DI_HappyPath injects a mock HTTP server and verifies
// the full SearchRegioJet flow including location loading and search.

func TestSearchRegioJet_DI_HappyPath(t *testing.T) {
	origClient := httpClient
	origLimiter := regiojetLimiter
	t.Cleanup(func() {
		httpClient = origClient
		regiojetLimiter = origLimiter
	})
	regiojetLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	// Clear the location cache so our test server is used.
	regiojetLocationCacheMu.Lock()
	origCache := regiojetLocationCache
	regiojetLocationCache = nil
	regiojetLocationCacheMu.Unlock()
	t.Cleanup(func() {
		regiojetLocationCacheMu.Lock()
		regiojetLocationCache = origCache
		regiojetLocationCacheMu.Unlock()
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if strings.Contains(r.URL.Path, "/consts/locations") {
			_ = json.NewEncoder(w).Encode([]regiojetCountry{
				{
					Country: "Czech Republic",
					Code:    "CZ",
					Cities: []regiojetCityRaw{
						{ID: 10, Name: "Prague", Aliases: []string{"Praha"}},
						{ID: 11, Name: "Brno"},
					},
				},
				{
					Country: "Austria",
					Code:    "AT",
					Cities: []regiojetCityRaw{
						{ID: 20, Name: "Vienna", Aliases: []string{"Wien"}},
					},
				},
			})
			return
		}

		if strings.Contains(r.URL.Path, "/routes/search") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"routes": []regiojetSearchResult{
					{
						DepartureTime:  "2026-07-01T08:00:00.000+02:00",
						ArrivalTime:    "2026-07-01T12:15:00.000+02:00",
						PriceFrom:      15.90,
						PriceTo:        25.90,
						TransfersCount: 0,
						FreeSeatsCount: 42,
						VehicleTypes:   []string{"BUS"},
					},
					{
						DepartureTime:  "2026-07-01T10:30:00.000+02:00",
						ArrivalTime:    "2026-07-01T14:30:00.000+02:00",
						PriceFrom:      12.50,
						PriceTo:        18.00,
						TransfersCount: 1,
						FreeSeatsCount: 5,
						VehicleTypes:   []string{"TRAIN", "BUS"},
					},
				},
			})
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	httpClient = &http.Client{
		Transport: &urlRewriter{base: srv.URL},
		Timeout:   5 * time.Second,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	routes, err := SearchRegioJet(ctx, 10, 20, "2026-07-01", SearchOptions{Currency: "EUR"})
	if err != nil {
		t.Fatalf("SearchRegioJet: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(routes))
	}

	// Sorted by price: 12.50, then 15.90.
	r0 := routes[0]
	if r0.Provider != "regiojet" {
		t.Errorf("provider = %q, want 'regiojet'", r0.Provider)
	}
	if r0.Price != 12.50 {
		t.Errorf("price = %.2f, want 12.50", r0.Price)
	}
	if r0.PriceMax != 18.00 {
		t.Errorf("priceMax = %.2f, want 18.00", r0.PriceMax)
	}
	if r0.Type != "mixed" {
		t.Errorf("type = %q, want 'mixed' (TRAIN+BUS)", r0.Type)
	}
	if r0.Transfers != 1 {
		t.Errorf("transfers = %d, want 1", r0.Transfers)
	}
	if r0.SeatsLeft == nil || *r0.SeatsLeft != 5 {
		t.Errorf("seatsLeft = %v, want 5", r0.SeatsLeft)
	}
	// City name should be resolved from mock locations.
	if r0.Departure.City != "Prague" {
		t.Errorf("departure city = %q, want 'Prague'", r0.Departure.City)
	}
	if r0.Arrival.City != "Vienna" {
		t.Errorf("arrival city = %q, want 'Vienna'", r0.Arrival.City)
	}

	r1 := routes[1]
	if r1.Price != 15.90 {
		t.Errorf("price = %.2f, want 15.90", r1.Price)
	}
	if r1.Type != "bus" {
		t.Errorf("type = %q, want 'bus'", r1.Type)
	}
}

// TestSearchRegioJet_DI_DateFilter verifies that results departing on a
// different date are filtered out.

func TestSearchRegioJet_DI_DateFilter(t *testing.T) {
	origClient := httpClient
	origLimiter := regiojetLimiter
	t.Cleanup(func() {
		httpClient = origClient
		regiojetLimiter = origLimiter
	})
	regiojetLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	regiojetLocationCacheMu.Lock()
	origCache := regiojetLocationCache
	regiojetLocationCache = nil
	regiojetLocationCacheMu.Unlock()
	t.Cleanup(func() {
		regiojetLocationCacheMu.Lock()
		regiojetLocationCache = origCache
		regiojetLocationCacheMu.Unlock()
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if strings.Contains(r.URL.Path, "/consts/locations") {
			_ = json.NewEncoder(w).Encode([]regiojetCountry{
				{Country: "CZ", Code: "CZ", Cities: []regiojetCityRaw{{ID: 10, Name: "Prague"}}},
				{Country: "AT", Code: "AT", Cities: []regiojetCityRaw{{ID: 20, Name: "Vienna"}}},
			})
			return
		}

		// Return results spanning two days.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"routes": []regiojetSearchResult{
				{
					DepartureTime:  "2026-07-01T22:00:00.000+02:00",
					ArrivalTime:    "2026-07-02T06:00:00.000+02:00",
					PriceFrom:      10.00,
					FreeSeatsCount: 10,
					VehicleTypes:   []string{"BUS"},
				},
				{
					DepartureTime:  "2026-07-02T08:00:00.000+02:00",
					ArrivalTime:    "2026-07-02T12:00:00.000+02:00",
					PriceFrom:      15.00,
					FreeSeatsCount: 20,
					VehicleTypes:   []string{"BUS"},
				},
			},
		})
	}))
	defer srv.Close()

	httpClient = &http.Client{
		Transport: &urlRewriter{base: srv.URL},
		Timeout:   5 * time.Second,
	}

	routes, err := SearchRegioJet(context.Background(), 10, 20, "2026-07-01", SearchOptions{})
	if err != nil {
		t.Fatalf("SearchRegioJet: %v", err)
	}
	// Only the route departing on 2026-07-01 should be included.
	if len(routes) != 1 {
		t.Fatalf("expected 1 route (date-filtered), got %d", len(routes))
	}
	if routes[0].Price != 10.00 {
		t.Errorf("price = %.2f, want 10.00", routes[0].Price)
	}
}

// ---------------------------------------------------------------------------
// SNCF — full SearchSNCF via httptest (deeper than existing calendar-only tests)
// ---------------------------------------------------------------------------

// TestSearchSNCF_DI_CalendarHappyPath tests the full SearchSNCF function
// (not just searchSNCFCalendar) with a mock server, verifying station
// lookup, booking URL construction, and route enrichment.

func TestSearchSNCF_DI_CalendarHappyPath(t *testing.T) {
	origDo := sncfDo
	origBrowserCookies := sncfBrowserCookies
	origLimiter := sncfLimiter
	t.Cleanup(func() {
		sncfDo = origDo
		sncfBrowserCookies = origBrowserCookies
		sncfLimiter = origLimiter
	})
	sncfLimiter = rate.NewLimiter(rate.Limit(1000), 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		price := 4500 // 45.00 EUR in cents
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"date": "2026-08-15", "price": price},
		})
	}))
	defer srv.Close()

	sncfDo = func(req *http.Request) (*http.Response, error) {
		mockURL := srv.URL + req.URL.Path + "?" + req.URL.RawQuery
		mockReq, err := http.NewRequestWithContext(req.Context(), req.Method, mockURL, nil)
		if err != nil {
			return nil, err
		}
		mockReq.Header = req.Header
		return http.DefaultClient.Do(mockReq)
	}
	sncfBrowserCookies = func(string) string { return "" }

	routes, err := SearchSNCF(context.Background(), "Paris", "Marseille", "2026-08-15", "EUR", false)
	if err != nil {
		t.Fatalf("SearchSNCF: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}

	r := routes[0]
	if r.Provider != "sncf" {
		t.Errorf("provider = %q, want 'sncf'", r.Provider)
	}
	if r.Price != 45.0 {
		t.Errorf("price = %.2f, want 45.0 (4500 cents / 100)", r.Price)
	}
	if r.Currency != "EUR" {
		t.Errorf("currency = %q, want 'EUR'", r.Currency)
	}
	if r.Departure.City != "Paris" {
		t.Errorf("departure city = %q, want 'Paris'", r.Departure.City)
	}
	if r.Departure.Station != "Paris (toutes gares)" {
		t.Errorf("departure station = %q, want 'Paris (toutes gares)'", r.Departure.Station)
	}
	if r.Arrival.City != "Marseille" {
		t.Errorf("arrival city = %q, want 'Marseille'", r.Arrival.City)
	}
	if r.Arrival.Station != "Marseille Saint-Charles" {
		t.Errorf("arrival station = %q, want 'Marseille Saint-Charles'", r.Arrival.Station)
	}
	if !strings.Contains(r.BookingURL, "sncf-connect.com") {
		t.Errorf("booking URL = %q, should contain 'sncf-connect.com'", r.BookingURL)
	}
	if !strings.Contains(r.BookingURL, "FRPAR") {
		t.Errorf("booking URL should contain origin station code FRPAR")
	}
}

// TestSearchSNCF_DI_UnknownStation verifies that unknown cities produce a
// clear error.
