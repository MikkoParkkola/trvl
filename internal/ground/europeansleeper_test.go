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

func TestEuropeanSleeperStations(t *testing.T) {
	tests := []struct {
		city    string
		wantUIC string
		wantOK  bool
	}{
		{"Brussels", "8800004", true},
		{"brussels", "8800004", true},
		{"BRUSSELS", "8800004", true},
		{"  Brussels  ", "8800004", true},
		{"Antwerp", "8800046", true},
		{"Rotterdam", "8400530", true},
		{"Amsterdam", "8400058", true},
		{"Berlin", "8011160", true},
		{"Dresden", "8010085", true},
		{"Prague", "5400014", true},
		{"praha", "5400014", true},
		{"", "", false},
		{"Nonexistent", "", false},
		{"London", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.city, func(t *testing.T) {
			station, ok := LookupEuropeanSleeperStation(tt.city)
			if ok != tt.wantOK {
				t.Fatalf("LookupEuropeanSleeperStation(%q) ok = %v, want %v", tt.city, ok, tt.wantOK)
			}
			if ok && station.UIC != tt.wantUIC {
				t.Errorf("UIC = %q, want %q", station.UIC, tt.wantUIC)
			}
		})
	}
}

func TestEuropeanSleeperStations_Metadata(t *testing.T) {
	station, ok := LookupEuropeanSleeperStation("Brussels")
	if !ok {
		t.Fatal("expected Brussels to be found")
	}
	if station.Name != "Bruxelles-Midi" {
		t.Errorf("Name = %q, want %q", station.Name, "Bruxelles-Midi")
	}
	if station.City != "Brussels" {
		t.Errorf("City = %q, want %q", station.City, "Brussels")
	}
	if station.Country != "BE" {
		t.Errorf("Country = %q, want %q", station.Country, "BE")
	}
}

func TestHasEuropeanSleeperStation(t *testing.T) {
	if !HasEuropeanSleeperStation("Brussels") {
		t.Error("Brussels should have a European Sleeper station")
	}
	if HasEuropeanSleeperStation("Atlantis") {
		t.Error("Atlantis should not have a European Sleeper station")
	}
}

func TestHasEuropeanSleeperRoute(t *testing.T) {
	tests := []struct {
		from string
		to   string
		want bool
	}{
		{"Brussels", "Prague", true},
		{"Amsterdam", "Berlin", true},
		{"Brussels", "SomeCity", true}, // one end matches
		{"SomeCity", "Prague", true},   // one end matches
		{"Atlantis", "Mordor", false},  // neither end matches
		{"", "Brussels", true},
		{"Brussels", "", true},
		{"", "", false},
	}

	for _, tt := range tests {
		name := tt.from + "->" + tt.to
		t.Run(name, func(t *testing.T) {
			got := HasEuropeanSleeperRoute(tt.from, tt.to)
			if got != tt.want {
				t.Errorf("HasEuropeanSleeperRoute(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.want)
			}
		})
	}
}

func TestAllEuropeanSleeperStationsHaveRequiredFields(t *testing.T) {
	for city, station := range europeanSleeperStations {
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

func TestEuropeanSleeperRateLimiterConfiguration(t *testing.T) {
	assertLimiterConfiguration(t, europeanSleeperLimiter, 6*time.Second, 1)
}

func TestBuildEuropeanSleeperBookingURL(t *testing.T) {
	from := EuropeanSleeperStation{UIC: "8800004", Name: "Bruxelles-Midi", City: "Brussels", Country: "BE"}
	to := EuropeanSleeperStation{UIC: "5400014", Name: "Praha hlavní nádraží", City: "Prague", Country: "CZ"}
	u := buildEuropeanSleeperBookingURL(from, to, "2026-07-15")

	if u == "" {
		t.Fatal("booking URL should not be empty")
	}
	if !strings.Contains(u, "europeansleeper.eu") {
		t.Error("should contain europeansleeper.eu")
	}
	if !strings.Contains(u, "8800004") {
		t.Error("should contain origin UIC")
	}
	if !strings.Contains(u, "5400014") {
		t.Error("should contain destination UIC")
	}
	if !strings.Contains(u, "2026-07-15") {
		t.Error("should contain date")
	}
}

func TestEuropeanSleeperSearch_MockServer(t *testing.T) {
	// Build a fixture response matching the Sqills S3 shape.
	fixture := europeanSleeperTripsResponse{
		Trips: []europeanSleeperTrip{
			{
				DepartureTime: "2026-07-15T19:22:00+02:00",
				ArrivalTime:   "2026-07-16T08:04:00+02:00",
				Duration:      762,
				Origin:        europeanSleeperTripStop{Name: "Bruxelles-Midi", Station: "Brussels-Midi"},
				Destination:   europeanSleeperTripStop{Name: "Praha hlavní nádraží", Station: "Prague hl.n."},
				Prices: []europeanSleeperPrice{
					{Amount: 49.00, Currency: "EUR", Class: "seat"},
					{Amount: 89.00, Currency: "EUR", Class: "couchette"},
				},
				Segments: []europeanSleeperSegment{
					{
						DepartureTime: "2026-07-15T19:22:00+02:00",
						ArrivalTime:   "2026-07-16T08:04:00+02:00",
						Origin:        europeanSleeperTripStop{Name: "Bruxelles-Midi"},
						Destination:   europeanSleeperTripStop{Name: "Praha hlavní nádraží"},
						TrainNumber:   "ES 452",
						Operator:      "European Sleeper",
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

	// Override the base URL for testing.
	origBase := europeanSleeperBase
	defer func() {
		// Restore — but since it is a const, we use the client override approach instead.
		_ = origBase
	}()

	// Use a custom client pointed at the test server.
	origClient := europeanSleeperClient
	europeanSleeperClient = server.Client()
	defer func() { europeanSleeperClient = origClient }()

	// Override the API URL by calling the parse function directly.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	apiURL := server.URL + "/api/v3/offers?origin=8800004&destination=5400014&date=2026-07-15&passengers=1&currency=EUR"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := europeanSleeperClient.Do(req)
	if err != nil {
		t.Fatalf("mock request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var tripsResp europeanSleeperTripsResponse
	if err := json.NewDecoder(resp.Body).Decode(&tripsResp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	fromStation := EuropeanSleeperStation{UIC: "8800004", Name: "Bruxelles-Midi", City: "Brussels", Country: "BE"}
	toStation := EuropeanSleeperStation{UIC: "5400014", Name: "Praha hlavní nádraží", City: "Prague", Country: "CZ"}
	bookingURL := buildEuropeanSleeperBookingURL(fromStation, toStation, "2026-07-15")

	routes := parseEuropeanSleeperTrips(tripsResp.Trips, fromStation, toStation, "2026-07-15", "EUR", bookingURL)
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}

	r := routes[0]
	if r.Provider != "european_sleeper" {
		t.Errorf("Provider = %q, want %q", r.Provider, "european_sleeper")
	}
	if r.Type != "train" {
		t.Errorf("Type = %q, want %q", r.Type, "train")
	}
	if r.Price != 49.00 {
		t.Errorf("Price = %f, want 49.00 (cheapest)", r.Price)
	}
	if r.Currency != "EUR" {
		t.Errorf("Currency = %q, want %q", r.Currency, "EUR")
	}
	if r.Duration != 762 {
		t.Errorf("Duration = %d, want 762", r.Duration)
	}
	if r.Departure.City != "Brussels" {
		t.Errorf("Departure.City = %q, want %q", r.Departure.City, "Brussels")
	}
	if r.Departure.Station != "Bruxelles-Midi" {
		t.Errorf("Departure.Station = %q, want %q", r.Departure.Station, "Bruxelles-Midi")
	}
	if r.Arrival.City != "Prague" {
		t.Errorf("Arrival.City = %q, want %q", r.Arrival.City, "Prague")
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

func TestParseEuropeanSleeperTrips_Empty(t *testing.T) {
	fromStation := EuropeanSleeperStation{UIC: "8800004", Name: "Bruxelles-Midi", City: "Brussels", Country: "BE"}
	toStation := EuropeanSleeperStation{UIC: "5400014", Name: "Praha hlavní nádraží", City: "Prague", Country: "CZ"}

	routes := parseEuropeanSleeperTrips(nil, fromStation, toStation, "2026-07-15", "EUR", "")
	if len(routes) != 0 {
		t.Errorf("expected 0 routes, got %d", len(routes))
	}
}

func TestParseEuropeanSleeperTrips_DataKey(t *testing.T) {
	// Some Sqills endpoints return results under "data" instead of "trips".
	jsonBody := `{"data":[{"departureTime":"2026-07-15T19:22:00","arrivalTime":"2026-07-16T08:04:00","duration":762,"origin":{"name":"Bruxelles-Midi"},"destination":{"name":"Praha hl.n."},"prices":[{"amount":49.00,"currency":"EUR"}]}]}`

	var tripsResp europeanSleeperTripsResponse
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

func TestNormaliseTimeString(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"2026-07-15T19:22:00+02:00", "2026-07-15T19:22:00"},
		{"2026-07-15T08:04:00Z", "2026-07-15T08:04:00"},
		{"2026-07-15T08:04:00", "2026-07-15T08:04:00"},
		{"2026-07-15T08:04:00.000", "2026-07-15T08:04:00"},
		{"", ""},
		{"invalid", "invalid"},
	}

	for _, tt := range tests {
		got := normaliseTimeString(tt.input)
		if got != tt.want {
			t.Errorf("normaliseTimeString(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestEuropeanSleeperPragueAlias(t *testing.T) {
	s1, ok1 := LookupEuropeanSleeperStation("prague")
	s2, ok2 := LookupEuropeanSleeperStation("praha")
	if !ok1 || !ok2 {
		t.Fatal("both prague and praha should resolve")
	}
	if s1.UIC != s2.UIC {
		t.Errorf("prague UIC=%q != praha UIC=%q", s1.UIC, s2.UIC)
	}
}
