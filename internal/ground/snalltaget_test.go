package ground

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"golang.org/x/time/rate"
)

func TestSnalltagetStations(t *testing.T) {
	tests := []struct {
		city    string
		wantUIC string
		wantOK  bool
	}{
		{"Stockholm", "7400001", true},
		{"stockholm", "7400001", true},
		{"STOCKHOLM", "7400001", true},
		{"  Stockholm  ", "7400001", true},
		{"Malmö", "7400003", true},
		{"malmo", "7400003", true},
		{"malmö c", "7400003", true},
		{"Norrköping", "7400120", true},
		{"norrkoping", "7400120", true},
		{"Linköping", "7400180", true},
		{"linkoping", "7400180", true},
		{"Alvesta", "7400440", true},
		{"Hässleholm", "7400490", true},
		{"hassleholm", "7400490", true},
		{"Lund", "7400500", true},
		{"Åre", "7402200", true},
		{"are", "7402200", true},
		{"Duved", "7402210", true},
		{"Berlin", "8011160", true},
		{"", "", false},
		{"Nonexistent", "", false},
		{"London", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.city, func(t *testing.T) {
			station, ok := LookupSnalltagetStation(tt.city)
			if ok != tt.wantOK {
				t.Fatalf("LookupSnalltagetStation(%q) ok = %v, want %v", tt.city, ok, tt.wantOK)
			}
			if ok && station.UIC != tt.wantUIC {
				t.Errorf("UIC = %q, want %q", station.UIC, tt.wantUIC)
			}
		})
	}
}

func TestSnalltagetStations_Metadata(t *testing.T) {
	station, ok := LookupSnalltagetStation("Stockholm")
	if !ok {
		t.Fatal("expected Stockholm to be found")
	}
	if station.Name != "Stockholm Central" {
		t.Errorf("Name = %q, want %q", station.Name, "Stockholm Central")
	}
	if station.City != "Stockholm" {
		t.Errorf("City = %q, want %q", station.City, "Stockholm")
	}
	if station.Country != "SE" {
		t.Errorf("Country = %q, want %q", station.Country, "SE")
	}
}

func TestHasSnalltagetStation(t *testing.T) {
	if !HasSnalltagetStation("Stockholm") {
		t.Error("Stockholm should have a Snälltåget station")
	}
	if HasSnalltagetStation("Atlantis") {
		t.Error("Atlantis should not have a Snälltåget station")
	}
}

func TestHasSnalltagetRoute(t *testing.T) {
	tests := []struct {
		from string
		to   string
		want bool
	}{
		{"Stockholm", "Malmö", true},
		{"Stockholm", "Åre", true},
		{"Stockholm", "Berlin", true},
		{"Stockholm", "SomeCity", true}, // one end matches
		{"SomeCity", "Malmö", true},     // one end matches
		{"Atlantis", "Mordor", false},   // neither matches
		{"", "Stockholm", true},
		{"Stockholm", "", true},
		{"", "", false},
	}

	for _, tt := range tests {
		name := tt.from + "->" + tt.to
		t.Run(name, func(t *testing.T) {
			got := HasSnalltagetRoute(tt.from, tt.to)
			if got != tt.want {
				t.Errorf("HasSnalltagetRoute(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.want)
			}
		})
	}
}

func TestAllSnalltagetStationsHaveRequiredFields(t *testing.T) {
	for city, station := range snalltagetStations {
		if station.UIC == "" {
			t.Errorf("station %q has empty UIC", city)
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
		if len(station.Country) != 2 {
			t.Errorf("station %q Country %q should be 2 letters", city, station.Country)
		}
	}
}

func TestSnalltagetRateLimiterConfiguration(t *testing.T) {
	assertLimiterConfiguration(t, snalltagetLimiter, 6*time.Second, 1)
}

func TestBuildSnalltagetBookingURL(t *testing.T) {
	from := SnalltagetStation{UIC: "7400001", Name: "Stockholm Central", City: "Stockholm", Country: "SE"}
	to := SnalltagetStation{UIC: "7400003", Name: "Malmö C", City: "Malmö", Country: "SE"}
	u := buildSnalltagetBookingURL(from, to, "2026-07-15")

	if u == "" {
		t.Fatal("booking URL should not be empty")
	}
	if !strings.Contains(u, "snalltaget.se") {
		t.Error("should contain snalltaget.se")
	}
	if !strings.Contains(u, "7400001") {
		t.Error("should contain origin UIC")
	}
	if !strings.Contains(u, "7400003") {
		t.Error("should contain destination UIC")
	}
	if !strings.Contains(u, "2026-07-15") {
		t.Error("should contain date")
	}
}

func TestSnalltagetSearch_MockServer(t *testing.T) {
	fixture := snalltagetTripsResponse{
		Trips: []snalltagetTrip{
			{
				DepartureTime: "2026-07-15T23:05:00+02:00",
				ArrivalTime:   "2026-07-16T08:32:00+02:00",
				Duration:      567,
				Origin:        snalltagetTripStop{Name: "Stockholm Central", Station: "Stockholm C"},
				Destination:   snalltagetTripStop{Name: "Malmö C", Station: "Malmö C"},
				Prices: []snalltagetPrice{
					{Amount: 299.00, Currency: "SEK", Class: "seat"},
					{Amount: 599.00, Currency: "SEK", Class: "bed"},
				},
				Segments: []snalltagetSegment{
					{
						DepartureTime: "2026-07-15T23:05:00+02:00",
						ArrivalTime:   "2026-07-16T08:32:00+02:00",
						Origin:        snalltagetTripStop{Name: "Stockholm Central"},
						Destination:   snalltagetTripStop{Name: "Malmö C"},
						TrainNumber:   "SJ 93",
						Operator:      "Snälltåget",
					},
				},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/offers" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(fixture)
	}))
	defer server.Close()

	origClient := snalltagetClient
	snalltagetClient = server.Client()
	defer func() { snalltagetClient = origClient }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	apiURL := server.URL + "/api/v3/offers?origin=7400001&destination=7400003&date=2026-07-15&passengers=1&currency=SEK"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := snalltagetClient.Do(req)
	if err != nil {
		t.Fatalf("mock request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var tripsResp snalltagetTripsResponse
	if err := json.NewDecoder(resp.Body).Decode(&tripsResp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	fromStation := SnalltagetStation{UIC: "7400001", Name: "Stockholm Central", City: "Stockholm", Country: "SE"}
	toStation := SnalltagetStation{UIC: "7400003", Name: "Malmö C", City: "Malmö", Country: "SE"}
	bookingURL := buildSnalltagetBookingURL(fromStation, toStation, "2026-07-15")

	routes := parseSnalltagetTrips(tripsResp.Trips, fromStation, toStation, "2026-07-15", "SEK", bookingURL)
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}

	r := routes[0]
	if r.Provider != "snalltaget" {
		t.Errorf("Provider = %q, want %q", r.Provider, "snalltaget")
	}
	if r.Type != "train" {
		t.Errorf("Type = %q, want %q", r.Type, "train")
	}
	if r.Price != 299.00 {
		t.Errorf("Price = %f, want 299.00 (cheapest)", r.Price)
	}
	if r.Currency != "SEK" {
		t.Errorf("Currency = %q, want %q", r.Currency, "SEK")
	}
	if r.Duration != 567 {
		t.Errorf("Duration = %d, want 567", r.Duration)
	}
	if r.Departure.City != "Stockholm" {
		t.Errorf("Departure.City = %q, want %q", r.Departure.City, "Stockholm")
	}
	if r.Departure.Station != "Stockholm Central" {
		t.Errorf("Departure.Station = %q, want %q", r.Departure.Station, "Stockholm Central")
	}
	if r.Arrival.City != "Malmö" {
		t.Errorf("Arrival.City = %q, want %q", r.Arrival.City, "Malmö")
	}
	if r.Transfers != 0 {
		t.Errorf("Transfers = %d, want 0", r.Transfers)
	}
	if len(r.Legs) != 1 {
		t.Errorf("Legs = %d, want 1", len(r.Legs))
	}
	if r.BookingURL == "" {
		t.Error("BookingURL should not be empty")
	}
}

func TestParseSnalltagetTrips_Empty(t *testing.T) {
	fromStation := SnalltagetStation{UIC: "7400001", Name: "Stockholm Central", City: "Stockholm", Country: "SE"}
	toStation := SnalltagetStation{UIC: "7400003", Name: "Malmö C", City: "Malmö", Country: "SE"}

	routes := parseSnalltagetTrips(nil, fromStation, toStation, "2026-07-15", "SEK", "")
	if len(routes) != 0 {
		t.Errorf("expected 0 routes, got %d", len(routes))
	}
}

func TestParseSnalltagetTrips_DataKey(t *testing.T) {
	jsonBody := `{"data":[{"departureTime":"2026-07-15T23:05:00","arrivalTime":"2026-07-16T08:32:00","duration":567,"origin":{"name":"Stockholm Central"},"destination":{"name":"Malmö C"},"prices":[{"amount":299.00,"currency":"SEK"}]}]}`

	var tripsResp snalltagetTripsResponse
	if err := json.Unmarshal([]byte(jsonBody), &tripsResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	trips := tripsResp.Trips
	if len(trips) == 0 {
		trips = tripsResp.Data
	}
	if len(trips) != 1 {
		t.Fatalf("expected 1 trip from data key, got %d", len(trips))
	}
}

func TestSnalltagetSwedishAliases(t *testing.T) {
	pairs := [][2]string{
		{"malmö", "malmo"},
		{"norrköping", "norrkoping"},
		{"linköping", "linkoping"},
		{"hässleholm", "hassleholm"},
		{"åre", "are"},
	}

	for _, p := range pairs {
		s1, ok1 := LookupSnalltagetStation(p[0])
		s2, ok2 := LookupSnalltagetStation(p[1])
		if !ok1 || !ok2 {
			t.Errorf("both %q and %q should be found", p[0], p[1])
			continue
		}
		if s1.UIC != s2.UIC {
			t.Errorf("%q UIC=%q != %q UIC=%q", p[0], s1.UIC, p[1], s2.UIC)
		}
	}
}

// useSnalltagetMux serves the guest-token and journey-search endpoints from
// one test server; redirectTransport keeps each request's path.
func useSnalltagetMux(t *testing.T, mux *http.ServeMux) {
	t.Helper()
	srv := httptest.NewServer(mux)
	origClient, origLimiter := snalltagetClient, snalltagetLimiter
	t.Cleanup(func() {
		srv.Close()
		snalltagetClient, snalltagetLimiter = origClient, origLimiter
	})
	snalltagetLimiter = rate.NewLimiter(rate.Limit(1000), 1)
	snalltagetClient = &http.Client{Transport: &redirectTransport{target: srv.URL}, Timeout: 5 * time.Second}
}

func snalltagetTokenOK(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"access_token":"test-token","expires_in":900}`))
}

// TestSearchSnalltaget_LiveSearchJourneyFixture replays the apiv2 journey
// search captured 2026-11-15 (Stockholm C -> Malmö C, one adult). The booking
// app at www.snalltaget.se fetches a guest token from /token/v2 and POSTs the
// search to apiv2.snalltaget.se; each route carries its own bundle fares.
func TestSearchSnalltaget_LiveSearchJourneyFixture(t *testing.T) {
	fixture := loadGroundFixture(t, "snalltaget_stockholm_malmo_searchjourney.json")
	mux := http.NewServeMux()
	mux.HandleFunc("/token/v2", snalltagetTokenOK)
	mux.HandleFunc("/orientation/searchjourney", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization = %q, want the guest token", got)
		}
		var body struct {
			Origin      string `json:"origin"`
			Destination string `json:"destination"`
			Departure   string `json:"departure"`
			Passengers  []struct {
				Type string `json:"type"`
			} `json:"passengers"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		if body.Origin != "740000001" || body.Destination != "Malmö C" || body.Departure != "2026-11-20" {
			t.Errorf("request = %+v, want origin 740000001, destination Malmö C, departure 2026-11-20", body)
		}
		if len(body.Passengers) != 1 || body.Passengers[0].Type != "AD" {
			t.Errorf("passengers = %+v, want one AD (adult)", body.Passengers)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fixture))
	})
	useSnalltagetMux(t, mux)

	routes, err := SearchSnalltaget(context.Background(), "Stockholm", "Malmö", "2026-11-20", "EUR")
	if err != nil {
		t.Fatalf("SearchSnalltaget: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("routes = %d, want 2 departures", len(routes))
	}
	want := []struct {
		dep, arr string
		dur      int
	}{
		{"2026-11-20T10:43:00", "2026-11-20T15:41:00", 298},
		{"2026-11-20T16:25:00", "2026-11-20T21:30:00", 305},
	}
	for i, w := range want {
		r := routes[i]
		if r.Departure.Time != w.dep || r.Arrival.Time != w.arr {
			t.Errorf("route %d = %s -> %s, want %s -> %s", i, r.Departure.Time, r.Arrival.Time, w.dep, w.arr)
		}
		if r.Duration != w.dur {
			t.Errorf("route %d duration = %d, want %d", i, r.Duration, w.dur)
		}
		// The cheapest bundle; the booking app charges SEK.
		if r.Price != 849 || r.Currency != "SEK" {
			t.Errorf("route %d fare = %v %s, want 849 SEK", i, r.Price, r.Currency)
		}
		if r.PriceProvenance != models.PriceLive {
			t.Errorf("route %d provenance = %q, want %q", i, r.PriceProvenance, models.PriceLive)
		}
	}
}

// TestSearchSnalltaget_TypedErrors pins that a refusal or a changed answer is
// an error the status layer can classify, never an empty success.
func TestSearchSnalltaget_TypedErrors(t *testing.T) {
	status := func(code int) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }
	}
	body := func(s string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(s)) }
	}
	cases := []struct {
		name          string
		token, search http.HandlerFunc
		want          error
	}{
		{"token refused", status(http.StatusServiceUnavailable), body(`{}`), models.ErrRateLimited},
		{"bot wall on token", status(http.StatusForbidden), body(`{}`), models.ErrRateLimited},
		{"token without access_token", body(`{}`), body(`{}`), models.ErrParseFailed},
		{"search refused", snalltagetTokenOK, status(http.StatusServiceUnavailable), models.ErrRateLimited},
		{"search moved", snalltagetTokenOK, status(http.StatusNotFound), models.ErrParseFailed},
		{"search not JSON", snalltagetTokenOK, body(`<html>maintenance</html>`), models.ErrParseFailed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/token/v2", c.token)
			mux.HandleFunc("/orientation/searchjourney", c.search)
			useSnalltagetMux(t, mux)
			routes, err := SearchSnalltaget(context.Background(), "Stockholm", "Malmö", "2026-11-20", "SEK")
			if !errors.Is(err, c.want) {
				t.Errorf("err = %v, want it to wrap %v", err, c.want)
			}
			if len(routes) != 0 {
				t.Errorf("routes = %d, want none alongside an error", len(routes))
			}
		})
	}
}

// TestSnalltagetStationCodesMatchLiveStops checks every station code against
// the apiv2 /navigation/stops list captured 2026-11-15, so a code the search
// endpoint would reject cannot ship.
func TestSnalltagetStationCodesMatchLiveStops(t *testing.T) {
	var stops struct {
		Items []struct {
			Code string `json:"code"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(loadGroundFixture(t, "snalltaget_stops.json")), &stops); err != nil {
		t.Fatalf("decode stops fixture: %v", err)
	}
	known := map[string]bool{}
	for _, s := range stops.Items {
		known[s.Code] = true
	}
	aliases := make([]string, 0, len(snalltagetStations))
	for alias := range snalltagetStations {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	for _, alias := range aliases {
		if code := snalltagetStations[alias].UIC; !known[code] {
			t.Errorf("station %q code %q is not in the live stops list", alias, code)
		}
	}
}

func TestSearchSnalltaget_NotApplicableWrapsSentinel(t *testing.T) {
	for _, pair := range [][2]string{{"Narnia", "Malmö"}, {"Stockholm", "Narnia"}} {
		_, err := SearchSnalltaget(context.Background(), pair[0], pair[1], "2026-11-20", "SEK")
		if !errors.Is(err, models.ErrNotApplicable) {
			t.Errorf("%s -> %s: error %v does not wrap models.ErrNotApplicable", pair[0], pair[1], err)
		}
	}
}
