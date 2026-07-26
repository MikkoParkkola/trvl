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
)

func TestLookupEurostarStation(t *testing.T) {
	tests := []struct {
		city    string
		wantUIC string
		wantOK  bool
	}{
		{"London", "7015400", true},
		{"london", "7015400", true},
		{"LONDON", "7015400", true},
		{"  London  ", "7015400", true},
		{"Paris", "8727100", true},
		{"Brussels", "8814001", true},
		{"Amsterdam", "8400058", true},
		{"Rotterdam", "8400530", true},
		{"Cologne", "8015458", true},
		{"Lille", "8722326", true},
		{"Prague", "", false},
		{"", "", false},
		{"Nonexistent", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.city, func(t *testing.T) {
			station, ok := LookupEurostarStation(tt.city)
			if ok != tt.wantOK {
				t.Fatalf("LookupEurostarStation(%q) ok = %v, want %v", tt.city, ok, tt.wantOK)
			}
			if ok && station.UIC != tt.wantUIC {
				t.Errorf("UIC = %q, want %q", station.UIC, tt.wantUIC)
			}
		})
	}
}

func TestLookupEurostarStation_Metadata(t *testing.T) {
	station, ok := LookupEurostarStation("London")
	if !ok {
		t.Fatal("expected London to be found")
	}
	if station.Name != "London St Pancras" {
		t.Errorf("Name = %q, want %q", station.Name, "London St Pancras")
	}
	if station.City != "London" {
		t.Errorf("City = %q, want %q", station.City, "London")
	}
	if station.Country != "GB" {
		t.Errorf("Country = %q, want %q", station.Country, "GB")
	}
}

func TestHasEurostarRoute(t *testing.T) {
	tests := []struct {
		from string
		to   string
		want bool
	}{
		{"London", "Paris", true},
		{"Paris", "Brussels", true},
		{"Amsterdam", "London", true},
		{"Cologne", "Lille", true},
		{"London", "Prague", false}, // Prague has no station
		{"Prague", "Vienna", false}, // Neither has a station
		{"", "Paris", false},
		{"London", "", false},
	}

	for _, tt := range tests {
		name := tt.from + "->" + tt.to
		t.Run(name, func(t *testing.T) {
			got := HasEurostarRoute(tt.from, tt.to)
			if got != tt.want {
				t.Errorf("HasEurostarRoute(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.want)
			}
		})
	}
}

func TestEurostarBuildBody(t *testing.T) {
	raw, err := eurostarBuildBody("7015400", "8727100", "2026-04-10", "2026-04-30", "GBP", false)
	if err != nil {
		t.Fatalf("eurostarBuildBody: %v", err)
	}

	var body eurostarGQLBody
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if body.OperationName != "cheapestFaresSearch" {
		t.Errorf("operationName = %q, want %q", body.OperationName, "cheapestFaresSearch")
	}
	if !strings.Contains(body.Query, "cheapestFaresSearch") {
		t.Error("query should contain cheapestFaresSearch")
	}
	if !strings.Contains(body.Query, "$cheapestFaresLists") {
		t.Error("query should use $cheapestFaresLists variable")
	}
	if body.Variables["currency"] != "GBP" {
		t.Errorf("currency = %v, want GBP", body.Variables["currency"])
	}
	faresRaw, ok := body.Variables["cheapestFaresLists"].([]interface{})
	if !ok || len(faresRaw) != 1 {
		t.Fatalf("cheapestFaresLists variable malformed: %v", body.Variables["cheapestFaresLists"])
	}
	fare, ok := faresRaw[0].(map[string]interface{})
	if !ok {
		t.Fatalf("cheapestFaresLists[0] malformed: %v", faresRaw[0])
	}
	for field, want := range map[string]string{
		"origin":      "7015400",
		"destination": "8727100",
		"startDate":   "2026-04-10",
		"endDate":     "2026-04-30",
		"direction":   "OUTBOUND",
	} {
		if got, _ := fare[field].(string); got != want {
			t.Errorf("cheapestFaresLists[0][%q] = %q, want %q", field, got, want)
		}
	}
}

func TestEurostarBuildBody_CurrencyUppercase(t *testing.T) {
	raw, err := eurostarBuildBody("7015400", "8727100", "2026-04-10", "2026-04-30", "eur", false)
	if err != nil {
		t.Fatalf("eurostarBuildBody: %v", err)
	}
	if !strings.Contains(string(raw), `"EUR"`) {
		t.Error("currency should be uppercased to EUR")
	}
}

func TestEurostarBuildBody_SnapFilter(t *testing.T) {
	raw, err := eurostarBuildBody("7015400", "8727100", "2026-04-10", "2026-04-30", "GBP", true)
	if err != nil {
		t.Fatalf("eurostarBuildBody: %v", err)
	}
	if !strings.Contains(string(raw), `"SNAP"`) {
		t.Error("snap body should contain SNAP in productFamiliesSearch")
	}
	if strings.Contains(string(raw), `"PUB_STANDARD"`) {
		t.Error("snap body should not contain PUB_STANDARD")
	}
}

func TestEurostarBuildBody_NoSnapFilter(t *testing.T) {
	raw, err := eurostarBuildBody("7015400", "8727100", "2026-04-10", "2026-04-30", "GBP", false)
	if err != nil {
		t.Fatalf("eurostarBuildBody: %v", err)
	}
	if strings.Contains(string(raw), `"SNAP"`) {
		t.Error("non-snap body should not contain SNAP")
	}
	if !strings.Contains(string(raw), `"PUB_STANDARD"`) {
		t.Error("non-snap body should contain PUB_STANDARD")
	}
}

func TestBuildEurostarBookingURL(t *testing.T) {
	url := buildEurostarBookingURL("7015400", "8727100", "2026-04-10")
	if url == "" {
		t.Fatal("booking URL should not be empty")
	}
	if !strings.Contains(url, "eurostar.com") {
		t.Error("should contain eurostar.com")
	}
	if !strings.Contains(url, "origin=7015400") {
		t.Error("should contain origin UIC")
	}
	if !strings.Contains(url, "destination=8727100") {
		t.Error("should contain destination UIC")
	}
	if !strings.Contains(url, "outbound=2026-04-10") {
		t.Error("should contain outbound date")
	}
}

func TestEurostarRateLimiterConfiguration(t *testing.T) {
	assertLimiterConfiguration(t, eurostarLimiter, 20*time.Second, 1)
}

func TestFlixbusRateLimiterConfiguration(t *testing.T) {
	assertLimiterConfiguration(t, flixbusLimiter, 100*time.Millisecond, 1)
}

func TestRegiojetRateLimiterConfiguration(t *testing.T) {
	assertLimiterConfiguration(t, regiojetLimiter, 100*time.Millisecond, 1)
}

func TestEurostarNotSearchedForNonEurostarCities(t *testing.T) {
	// SearchByName for Prague->Vienna should not trigger Eurostar.
	// We verify by checking HasEurostarRoute returns false.
	if HasEurostarRoute("Prague", "Vienna") {
		t.Error("Prague-Vienna should not have a Eurostar route")
	}
}

func TestSearchEurostar_UsesNabFallbackOn403(t *testing.T) {
	origDo := eurostarDo
	origFetchViaNab := eurostarFetchViaNab
	origBrowserCookies := eurostarBrowserCookies
	t.Cleanup(func() {
		eurostarDo = origDo
		eurostarFetchViaNab = origFetchViaNab
		eurostarBrowserCookies = origBrowserCookies
	})

	eurostarDo = func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Body:       io.NopCloser(strings.NewReader("blocked")),
			Header:     make(http.Header),
		}, nil
	}
	eurostarBrowserCookies = func(_ context.Context, _ string) string { return "" }
	eurostarFetchViaNab = func(context.Context, []byte, EurostarStation, EurostarStation, string, string, bool) ([]models.GroundRoute, error) {
		return []models.GroundRoute{
			{
				Provider:  "eurostar",
				Type:      "train",
				Price:     49.0,
				Currency:  "GBP",
				Departure: models.GroundStop{City: "London"},
				Arrival:   models.GroundStop{City: "Paris"},
			},
		}, nil
	}

	routes, err := SearchEurostar(context.Background(), "London", "Paris", "2026-04-10", "2026-04-30", "GBP", false)
	if err != nil {
		t.Fatalf("SearchEurostar returned error: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}
	if routes[0].Provider != "eurostar" {
		t.Fatalf("provider = %q, want %q", routes[0].Provider, "eurostar")
	}
	if routes[0].Departure.City != "London" || routes[0].Arrival.City != "Paris" {
		t.Fatalf("unexpected route cities: %+v", routes[0])
	}
}

func TestEurostarBuildTimetableBody(t *testing.T) {
	raw, err := eurostarBuildTimetableBody("7015400", "8727100", "2026-04-10")
	if err != nil {
		t.Fatalf("eurostarBuildTimetableBody: %v", err)
	}

	var body eurostarGQLBody
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if body.OperationName != "timetableServices" {
		t.Errorf("operationName = %q, want timetableServices", body.OperationName)
	}
	if !strings.Contains(body.Query, "timetableServices") {
		t.Error("query should contain timetableServices")
	}
	if body.Variables["originUic"] != "7015400" {
		t.Errorf("originUic = %v, want 7015400", body.Variables["originUic"])
	}
	if body.Variables["destinationUic"] != "8727100" {
		t.Errorf("destinationUic = %v, want 8727100", body.Variables["destinationUic"])
	}
	date, _ := body.Variables["date"].(string)
	if !strings.HasPrefix(date, "2026-04-10") {
		t.Errorf("date = %q, should start with 2026-04-10", date)
	}
}

func TestBuildEurostarRoutes_WithTimetable(t *testing.T) {
	fromStation := EurostarStation{UIC: "7015400", Name: "London St Pancras", City: "London", Country: "GB"}
	toStation := EurostarStation{UIC: "8727100", Name: "Paris Gare du Nord", City: "Paris", Country: "FR"}

	gqlResp := eurostarGQLResponse{}
	gqlResp.Data.CheapestFaresSearch = []struct {
		CheapestFares []struct {
			Date  string  `json:"date"`
			Price float64 `json:"price"`
		} `json:"cheapestFares"`
	}{
		{
			CheapestFares: []struct {
				Date  string  `json:"date"`
				Price float64 `json:"price"`
			}{
				{Date: "2026-04-10", Price: 49.00},
			},
		},
	}

	timetable := []eurostarTimetableEntry{
		{TrainNumber: "9002", DepartureTime: "2026-04-10 06:31:00", ArrivalTime: "2026-04-10 09:47:00"},
		{TrainNumber: "9010", DepartureTime: "2026-04-10 08:31:00", ArrivalTime: "2026-04-10 11:47:00"},
	}

	routes, err := buildEurostarRoutes(gqlResp, fromStation, toStation, "GBP", false, timetable)
	if err != nil {
		t.Fatalf("buildEurostarRoutes: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("expected 2 routes (one per train), got %d", len(routes))
	}
	for i, r := range routes {
		if r.Price != 49.00 {
			t.Errorf("routes[%d].Price = %.2f, want 49.00", i, r.Price)
		}
		if r.Departure.City != "London" {
			t.Errorf("routes[%d].Departure.City = %q, want London", i, r.Departure.City)
		}
		if r.Arrival.City != "Paris" {
			t.Errorf("routes[%d].Arrival.City = %q, want Paris", i, r.Arrival.City)
		}
		if r.Duration <= 0 {
			t.Errorf("routes[%d].Duration = %d, should be > 0", i, r.Duration)
		}
	}
	if routes[0].Departure.Time != "2026-04-10 06:31:00" {
		t.Errorf("routes[0].Departure.Time = %q, want 2026-04-10 06:31:00", routes[0].Departure.Time)
	}
	if routes[1].Departure.Time != "2026-04-10 08:31:00" {
		t.Errorf("routes[1].Departure.Time = %q, want 2026-04-10 08:31:00", routes[1].Departure.Time)
	}
}

func TestBuildEurostarRoutes_NoTimetable(t *testing.T) {
	fromStation := EurostarStation{UIC: "7015400", Name: "London St Pancras", City: "London", Country: "GB"}
	toStation := EurostarStation{UIC: "8727100", Name: "Paris Gare du Nord", City: "Paris", Country: "FR"}

	gqlResp := eurostarGQLResponse{}
	gqlResp.Data.CheapestFaresSearch = []struct {
		CheapestFares []struct {
			Date  string  `json:"date"`
			Price float64 `json:"price"`
		} `json:"cheapestFares"`
	}{
		{
			CheapestFares: []struct {
				Date  string  `json:"date"`
				Price float64 `json:"price"`
			}{
				{Date: "2026-06-01", Price: 59.00},
				{Date: "2026-06-02", Price: 65.00},
			},
		},
	}

	routes, err := buildEurostarRoutes(gqlResp, fromStation, toStation, "GBP", false, nil)
	if err != nil {
		t.Fatalf("buildEurostarRoutes: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(routes))
	}
	// Without timetable, times should be formatted dates like "Jun 01".
	if routes[0].Departure.Time != "Jun 01" {
		t.Errorf("routes[0].Departure.Time = %q, want Jun 01", routes[0].Departure.Time)
	}
	if routes[1].Departure.Time != "Jun 02" {
		t.Errorf("routes[1].Departure.Time = %q, want Jun 02", routes[1].Departure.Time)
	}
}

func TestBuildEurostarRoutes_TimetableDifferentDate(t *testing.T) {
	// Timetable trains on a different date should not pollute the fare's date.
	fromStation := EurostarStation{UIC: "7015400", Name: "London St Pancras", City: "London", Country: "GB"}
	toStation := EurostarStation{UIC: "8727100", Name: "Paris Gare du Nord", City: "Paris", Country: "FR"}

	gqlResp := eurostarGQLResponse{}
	gqlResp.Data.CheapestFaresSearch = []struct {
		CheapestFares []struct {
			Date  string  `json:"date"`
			Price float64 `json:"price"`
		} `json:"cheapestFares"`
	}{
		{
			CheapestFares: []struct {
				Date  string  `json:"date"`
				Price float64 `json:"price"`
			}{
				{Date: "2026-04-10", Price: 49.00},
			},
		},
	}

	// Timetable only has trains on a different date — should fall back to "Apr 10" display.
	timetable := []eurostarTimetableEntry{
		{TrainNumber: "9002", DepartureTime: "2026-04-11 06:31:00", ArrivalTime: "2026-04-11 09:47:00"},
	}

	routes, err := buildEurostarRoutes(gqlResp, fromStation, toStation, "GBP", false, timetable)
	if err != nil {
		t.Fatalf("buildEurostarRoutes: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("expected 1 route (fallback), got %d", len(routes))
	}
	if routes[0].Departure.Time != "Apr 10" {
		t.Errorf("Departure.Time = %q, want Apr 10", routes[0].Departure.Time)
	}
}

func TestAllEurostarStationsHaveRequiredFields(t *testing.T) {
	for city, station := range eurostarStations {
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
		if len(station.UIC) != 7 {
			t.Errorf("station %q UIC %q should be 7 digits", city, station.UIC)
		}
	}
}
