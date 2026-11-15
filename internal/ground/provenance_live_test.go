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

	"github.com/MikkoParkkola/trvl/internal/models"
	"golang.org/x/time/rate"
)

// assertAllLive checks a producer on PR A's live list: every priced route it
// returns is a fare for that departure and must say so.
func assertAllLive(t *testing.T, provider string, routes []models.GroundRoute) {
	t.Helper()
	if len(routes) == 0 {
		t.Fatalf("%s: want routes, got none", provider)
	}
	for i, r := range routes {
		if r.Price > 0 && r.PriceProvenance != models.PriceLive {
			t.Errorf("%s route %d (price %v) provenance = %q, want live", provider, i, r.Price, r.PriceProvenance)
		}
	}
}

func TestLiveProducer_FlixBus(t *testing.T) {
	origClient, origLimiter := httpClient, flixbusLimiter
	flixbusLimiter = rate.NewLimiter(rate.Limit(1000), 1)
	t.Cleanup(func() { httpClient, flixbusLimiter = origClient, origLimiter })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/search/autocomplete") {
			_ = json.NewEncoder(w).Encode([]flixbusAutocompleteItem{{ID: r.URL.Query().Get("q"), Name: "Berlin", Country: "DE", Score: 1, IsFlixbusCity: true}})
			return
		}
		if !strings.Contains(r.URL.Path, "/search/service") {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(flixbusSearchResponse{Trips: []flixbusTrip{{
			DepartureCityID: "city-berlin",
			ArrivalCityID:   "city-munich",
			Results: map[string]flixbusResult{"uid-1": {
				UID:       "uid-1",
				Status:    "available",
				Departure: flixbusStop{Date: "2026-07-01T08:00:00+02:00", CityID: "city-berlin"},
				Arrival:   flixbusStop{Date: "2026-07-01T14:30:00+02:00", CityID: "city-munich"},
				Duration:  flixbusDuration{Hours: 6, Minutes: 30},
				Price:     flixbusPrice{Total: 24.99},
				Legs: []flixbusLeg{{
					Departure:        flixbusStop{Date: "2026-07-01T08:00:00+02:00", CityID: "city-berlin"},
					Arrival:          flixbusStop{Date: "2026-07-01T14:30:00+02:00", CityID: "city-munich"},
					MeansOfTransport: "bus",
				}},
			}},
		}}})
	}))
	defer srv.Close()
	httpClient = &http.Client{Transport: &urlRewriter{base: srv.URL}, Timeout: 5 * time.Second}
	routes, err := SearchFlixBus(context.Background(), "city-berlin", "city-munich", "2026-07-01", SearchOptions{Currency: "EUR"})
	if err != nil {
		t.Fatalf("SearchFlixBus: %v", err)
	}
	assertAllLive(t, "flixbus", routes)
}

func TestLiveProducer_RegioJet(t *testing.T) {
	origClient, origLimiter := httpClient, regiojetLimiter
	regiojetLimiter = rate.NewLimiter(rate.Limit(1000), 1)
	t.Cleanup(func() { httpClient, regiojetLimiter = origClient, origLimiter })
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
				{Country: "Czech Republic", Code: "CZ", Cities: []regiojetCityRaw{{ID: 10, Name: "Prague"}}},
				{Country: "Austria", Code: "AT", Cities: []regiojetCityRaw{{ID: 20, Name: "Vienna"}}},
			})
			return
		}
		if !strings.Contains(r.URL.Path, "/routes/search") {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"routes": []regiojetSearchResult{{
			DepartureTime: "2026-07-01T08:00:00.000+02:00", ArrivalTime: "2026-07-01T12:15:00.000+02:00",
			PriceFrom: 15.90, PriceTo: 25.90, FreeSeatsCount: 42, VehicleTypes: []string{"BUS"},
		}}})
	}))
	defer srv.Close()
	httpClient = &http.Client{Transport: &urlRewriter{base: srv.URL}, Timeout: 5 * time.Second}
	routes, err := SearchRegioJet(context.Background(), 10, 20, "2026-07-01", SearchOptions{Currency: "EUR"})
	if err != nil {
		t.Fatalf("SearchRegioJet: %v", err)
	}
	assertAllLive(t, "regiojet", routes)
}

func TestLiveProducer_Trenitalia(t *testing.T) {
	solutions := loadTrenitaliaFixture(t, "trenitalia_solutions.json")
	locations := loadTrenitaliaFixture(t, "trenitalia_locations.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "locations/search"):
			_, _ = w.Write(locations)
		case strings.Contains(r.URL.Path, "ticket/solutions"):
			_, _ = w.Write(solutions)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	origHost, origLimiter := trenitaliaHost, trenitaliaLimiter
	trenitaliaHost, trenitaliaLimiter = srv.URL, newProviderLimiter(0)
	t.Cleanup(func() { trenitaliaHost, trenitaliaLimiter = origHost, origLimiter })
	routes, err := SearchTrenitalia(context.Background(), "Milan", "Rome", "2026-07-15", "EUR")
	if err != nil {
		t.Fatalf("SearchTrenitalia: %v", err)
	}
	assertAllLive(t, "trenitalia", routes)
}

func TestLiveProducer_Italo(t *testing.T) {
	status := loadItaloFixture(t, "italo_status.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/login":
			http.SetCookie(w, &http.Cookie{Name: "BIGSessionToken", Value: "test-jwt"})
			_, _ = w.Write([]byte(`{"isLoggedIn":true,"isAnonymous":true}`))
		case r.URL.Path == "/api/v1/booking":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"operationId":"op-1","pollAfter":1,"isCompleted":false}`))
		case strings.HasPrefix(r.URL.Path, "/api/v1/booking/status/"):
			_, _ = w.Write(status)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	origLogin, origAPI, origLimiter, origPoll := italoLoginHost, italoAPIHost, italoLimiter, italoPollInterval
	italoLoginHost, italoAPIHost, italoLimiter, italoPollInterval = srv.URL, srv.URL, newProviderLimiter(0), 0
	t.Cleanup(func() {
		italoLoginHost, italoAPIHost, italoLimiter, italoPollInterval = origLogin, origAPI, origLimiter, origPoll
	})
	routes, err := SearchItalo(context.Background(), "Milan", "Rome", "2026-07-15", "EUR")
	if err != nil {
		t.Fatalf("SearchItalo: %v", err)
	}
	assertAllLive(t, "italo", routes)
}

func TestLiveProducer_Ferryhopper(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", goldenFerryhopperFrame)
	}))
	defer srv.Close()
	origClient, origURL, origLimiter := ferryhopperClient, ferryhopperMCPURL, ferryhopperLimiter
	ferryhopperClient, ferryhopperMCPURL, ferryhopperLimiter = srv.Client(), srv.URL, rate.NewLimiter(rate.Limit(1000), 1)
	t.Cleanup(func() { ferryhopperClient, ferryhopperMCPURL, ferryhopperLimiter = origClient, origURL, origLimiter })
	routes, err := SearchFerryhopper(context.Background(), "Piraeus", "Santorini", "2026-07-15", "EUR")
	if err != nil {
		t.Fatalf("SearchFerryhopper: %v", err)
	}
	assertAllLive(t, "ferryhopper", routes)
}

// TestLiveProducer_SNCFPerDeparture covers the SNCF per-departure (BFF) path.
// The calendar path is a day minimum and is guarded as non-live below.
func TestLiveProducer_SNCFPerDeparture(t *testing.T) {
	data := map[string]any{"journeys": []any{map[string]any{
		"departureDate": "2026-06-01T08:30:00", "arrivalDate": "2026-06-01T10:30:00",
		"duration": 120.0, "price": map[string]any{"amount": 45.5, "currency": "EUR"}, "transfers": 0.0,
	}}}
	assertAllLive(t, "sncf bff", parseSNCFBFFResponse(data, "https://sncf-connect.com/booking", "2026-06-01", "EUR"))
}

// TestLiveProducer_TrainlineAPI covers the Trainline journey-search parser.
func TestLiveProducer_TrainlineAPI(t *testing.T) {
	var resp trainlineJourneySearchResponse
	if err := json.Unmarshal([]byte(mockTrainlineResponse), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	routes, err := parseTrainlineResults(resp, "Paris", "Amsterdam", "2026-06-15", "EUR")
	if err != nil {
		t.Fatalf("parseTrainlineResults: %v", err)
	}
	assertAllLive(t, "trainline", routes)
}
