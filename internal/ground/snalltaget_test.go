package ground

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
