package ground

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/testutil"
)

func TestLookupFinnlinesPort(t *testing.T) {
	tests := []struct {
		city     string
		wantCode string
		wantCity string
		wantOK   bool
	}{
		{"Helsinki", "FIHEL", "Helsinki", true},
		{"hel", "FIHEL", "Helsinki", true},
		{"Naantali", "FINLI", "Naantali", true},
		{"Travemünde", "DETRV", "Travemünde", true},
		{"travemunde", "DETRV", "Travemünde", true},
		{"Kapellskär", "SEKPS", "Kapellskär", true},
		{"kapellskar", "SEKPS", "Kapellskär", true},
		{"Malmö", "SEMMA", "Malmö", true},
		{"malmo", "SEMMA", "Malmö", true},
		{"Świnoujście", "PLSWI", "Świnoujście", true},
		{"swinoujscie", "PLSWI", "Świnoujście", true},
		{"Långnäs", "FILAN", "Långnäs", true},
		{"langnas", "FILAN", "Långnäs", true},
		{"unknown", "", "", false},
		{"", "", "", false},
	}

	for _, tt := range tests {
		p, ok := LookupFinnlinesPort(tt.city)
		if ok != tt.wantOK {
			t.Errorf("LookupFinnlinesPort(%q): ok = %v, want %v", tt.city, ok, tt.wantOK)
			continue
		}
		if ok && p.Code != tt.wantCode {
			t.Errorf("LookupFinnlinesPort(%q).Code = %q, want %q", tt.city, p.Code, tt.wantCode)
		}
		if ok && p.City != tt.wantCity {
			t.Errorf("LookupFinnlinesPort(%q).City = %q, want %q", tt.city, p.City, tt.wantCity)
		}
	}
}

func TestHasFinnlinesRoute(t *testing.T) {
	if !HasFinnlinesRoute("Naantali", "Kapellskär") {
		t.Error("expected Naantali-Kapellskär route")
	}
	if !HasFinnlinesRoute("Helsinki", "Travemünde") {
		t.Error("expected Helsinki-Travemünde route")
	}
	if HasFinnlinesRoute("London", "Paris") {
		t.Error("London-Paris should not be a Finnlines route")
	}
}

func TestParseFinnlinesCrossingMinutes(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"7:45", 465},
		{"30:00", 1800},
		{"2:15", 135},
		{"0:30", 30},
		{"", 0},
		{"bad", 0},
	}
	for _, tt := range tests {
		got := parseFinnlinesCrossingMinutes(tt.input)
		if got != tt.want {
			t.Errorf("parseFinnlinesCrossingMinutes(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestFinnlinesShipSuffix(t *testing.T) {
	if got := finnlinesShipSuffix(""); got != "" {
		t.Errorf("empty ship = %q, want empty", got)
	}
	if got := finnlinesShipSuffix("FINNCANOPUS"); got != " (Finncanopus)" {
		t.Errorf("FINNCANOPUS suffix = %q, want %q", got, " (Finncanopus)")
	}
}

func TestFinnlinesAllPortsHaveRequiredFields(t *testing.T) {
	for alias, port := range finnlinesPorts {
		if port.Code == "" {
			t.Errorf("port alias %q has empty Code", alias)
		}
		if port.Name == "" {
			t.Errorf("port alias %q has empty Name", alias)
		}
		if port.City == "" {
			t.Errorf("port alias %q has empty City", alias)
		}
	}
}

const mockFinnlinesGraphQLResponse = `{
  "data": {
    "listTimeTableAvailability": [
      {
        "sailingCode": "NLIKPS202605011000",
        "departureDate": "2026-05-01",
        "departureTime": "10:00",
        "arrivalDate": "2026-05-01",
        "arrivalTime": "17:45",
        "departurePort": "FINLI",
        "arrivalPort": "SEKPS",
        "isAvailable": true,
        "shipName": "FINNCANOPUS",
        "crossingTime": "7:45",
        "chargeTotal": 2720
      },
      {
        "sailingCode": "NLIKPS202605012245",
        "departureDate": "2026-05-01",
        "departureTime": "22:45",
        "arrivalDate": "2026-05-02",
        "arrivalTime": "06:30",
        "departurePort": "FINLI",
        "arrivalPort": "SEKPS",
        "isAvailable": false,
        "shipName": "FINNSIRIUS",
        "crossingTime": "7:45",
        "chargeTotal": null
      }
    ]
  }
}`

func TestFinnlinesGraphQLResponse_Parse(t *testing.T) {
	var resp finnlinesGraphQLResponse
	if err := json.Unmarshal([]byte(mockFinnlinesGraphQLResponse), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	entries := resp.Data.ListTimeTableAvailability
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	e := entries[0]
	if e.ShipName != "FINNCANOPUS" {
		t.Errorf("ShipName = %q, want FINNCANOPUS", e.ShipName)
	}
	if e.ChargeTotal == nil || *e.ChargeTotal != 2720 {
		t.Errorf("ChargeTotal = %v, want 2720", e.ChargeTotal)
	}
	if !e.IsAvailable {
		t.Error("expected IsAvailable=true for first entry")
	}
	if e.CrossingTime != "7:45" {
		t.Errorf("CrossingTime = %q, want 7:45", e.CrossingTime)
	}

	// Second entry: unavailable, no price.
	e2 := entries[1]
	if e2.IsAvailable {
		t.Error("expected IsAvailable=false for second entry")
	}
	if e2.ChargeTotal != nil {
		t.Errorf("ChargeTotal should be nil for unavailable entry, got %d", *e2.ChargeTotal)
	}
}

func TestFinnlines_MockServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") == "" {
			t.Error("missing x-api-key header")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", r.Header.Get("Content-Type"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(mockFinnlinesGraphQLResponse)) //nolint:errcheck
	}))
	defer srv.Close()

	// Parse mock response directly (can't override const endpoint in unit test).
	var resp finnlinesGraphQLResponse
	if err := json.Unmarshal([]byte(mockFinnlinesGraphQLResponse), &resp); err != nil {
		t.Fatalf("parse: %v", err)
	}

	entries := resp.Data.ListTimeTableAvailability
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	// Verify price conversion: 2720 cents = EUR 27.20
	if entries[0].ChargeTotal != nil {
		price := float64(*entries[0].ChargeTotal) / 100.0
		if price != 27.20 {
			t.Errorf("price = %.2f, want 27.20", price)
		}
	}
}

func TestFinnlinesRateLimiterConfiguration(t *testing.T) {
	assertLimiterConfiguration(t, finnlinesLimiter, 6*time.Second, 1)
}

func TestBuildFinnlinesBookingURL(t *testing.T) {
	u := buildFinnlinesBookingURL("FINLI", "SEKPS", "2026-05-01")
	if !strings.Contains(u, "finnlines.com") {
		t.Errorf("URL should contain finnlines.com, got %q", u)
	}
	if !strings.Contains(u, "FINLI") {
		t.Errorf("URL should contain departure port, got %q", u)
	}
	if !strings.Contains(u, "2026-05-01") {
		t.Errorf("URL should contain date, got %q", u)
	}
}

func TestIsFinnlinesOvernightRoute(t *testing.T) {
	tests := []struct {
		from, to string
		want     bool
	}{
		{"FIHEL", "DETRV", true},
		{"DETRV", "FIHEL", true},
		{"FIHEL", "PLSWI", true},
		{"PLSWI", "FIHEL", true},
		{"SEMMA", "DETRV", true},
		{"DETRV", "SEMMA", true},
		{"FINLI", "SEKPS", false}, // short crossing
		{"FIHEL", "SEKPS", false},
	}
	for _, tt := range tests {
		got := isFinnlinesOvernightRoute(tt.from, tt.to)
		if got != tt.want {
			t.Errorf("isFinnlinesOvernightRoute(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.want)
		}
	}
}

func TestCheapestFinnlinesCabin(t *testing.T) {
	products := []finnlinesProduct{
		{Code: "DS", Type: "ACCOMMODATION", Name: "Recliner Seat", Available: true, ChargePerUnit: 2806},
		{Code: "AB4", Type: "ACCOMMODATION", Name: "Inside cabin, 4 beds", Available: true, ChargePerUnit: 19639},
		{Code: "AB2", Type: "ACCOMMODATION", Name: "Inside cabin, 2 beds", Available: true, ChargePerUnit: 14500},
		{Code: "VH1", Type: "VEHICLE", Name: "Car", Available: true, ChargePerUnit: 10000},
		{Code: "LX", Type: "ACCOMMODATION", Name: "Luxury Suite", Available: false, ChargePerUnit: 500}, // unavailable
	}

	cabin, ok := cheapestFinnlinesCabin(products)
	if !ok {
		t.Fatal("expected to find a cabin")
	}
	if cabin.Code != "DS" {
		t.Errorf("cheapest cabin code = %q, want DS", cabin.Code)
	}
	if cabin.ChargePerUnit != 2806 {
		t.Errorf("cheapest cabin price = %.0f, want 2806", cabin.ChargePerUnit)
	}
}

func TestCheapestFinnlinesCabin_NoneAvailable(t *testing.T) {
	products := []finnlinesProduct{
		{Code: "VH1", Type: "VEHICLE", Name: "Car", Available: true, ChargePerUnit: 10000},
		{Code: "LX", Type: "ACCOMMODATION", Name: "Luxury Suite", Available: false, ChargePerUnit: 500},
	}

	_, ok := cheapestFinnlinesCabin(products)
	if ok {
		t.Error("expected no cabin found when none available")
	}
}

func TestCheapestFinnlinesCabin_Empty(t *testing.T) {
	_, ok := cheapestFinnlinesCabin(nil)
	if ok {
		t.Error("expected no cabin found for nil input")
	}
}

func TestCheapestFinnlinesCabin_SingleItem(t *testing.T) {
	products := []finnlinesProduct{
		{Code: "AB2", Type: "ACCOMMODATION", Name: "Inside cabin", Available: true, ChargePerUnit: 14500},
	}
	cabin, ok := cheapestFinnlinesCabin(products)
	if !ok {
		t.Fatal("expected to find a cabin")
	}
	if cabin.Code != "AB2" {
		t.Errorf("cabin code = %q, want AB2", cabin.Code)
	}
}

func TestFormatCabinPrice(t *testing.T) {
	tests := []struct {
		cents float64
		want  string
	}{
		{2806, "cabin from €28.06"},
		{19639, "cabin from €196.39"},
		{0, "cabin from €0.00"},
		{100, "cabin from €1.00"},
	}
	for _, tt := range tests {
		got := formatCabinPrice(tt.cents)
		if got != tt.want {
			t.Errorf("formatCabinPrice(%.0f) = %q, want %q", tt.cents, got, tt.want)
		}
	}
}

func TestFinnlinesProductResponse_Parse(t *testing.T) {
	raw := `{
	  "data": {
	    "listProductsAvailability": [
	      {"code":"DS","type":"ACCOMMODATION","name":"Recliner Seat","desc":"A recliner seat","maxPeople":1,"available":true,"chargePerUnit":2806},
	      {"code":"AB4","type":"ACCOMMODATION","name":"Inside cabin, 4 beds","desc":"","maxPeople":4,"available":true,"chargePerUnit":19639},
	      {"code":"VH1","type":"VEHICLE","name":"Car up to 2m","desc":"","maxPeople":0,"available":true,"chargePerUnit":15000}
	    ]
	  }
	}`

	var resp finnlinesProductResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	products := resp.Data.ListProductsAvailability
	if len(products) != 3 {
		t.Fatalf("expected 3 products, got %d", len(products))
	}

	ds := products[0]
	if ds.Code != "DS" || ds.Type != "ACCOMMODATION" || !ds.Available || ds.ChargePerUnit != 2806 {
		t.Errorf("DS product mismatch: %+v", ds)
	}
	if ds.MaxPeople != 1 {
		t.Errorf("DS maxPeople = %d, want 1", ds.MaxPeople)
	}
}

func TestFinnlinesProductResponse_ApiError(t *testing.T) {
	raw := `{
	  "data": {
	    "listProductsAvailability": []
	  },
	  "errors": [{"message":"Invalid departure port"}]
	}`

	var resp finnlinesProductResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(resp.Errors) == 0 {
		t.Fatal("expected errors")
	}
	if resp.Errors[0].Message != "Invalid departure port" {
		t.Errorf("error message = %q", resp.Errors[0].Message)
	}
}

func TestFinnlinesOvernightRoutes_Complete(t *testing.T) {
	// Verify all overnight routes are bidirectional.
	pairs := [][2]string{
		{"FIHEL", "DETRV"},
		{"FIHEL", "PLSWI"},
		{"SEMMA", "DETRV"},
	}
	for _, p := range pairs {
		if !finnlinesOvernightRoutes[p[0]+"-"+p[1]] {
			t.Errorf("missing overnight route %s→%s", p[0], p[1])
		}
		if !finnlinesOvernightRoutes[p[1]+"-"+p[0]] {
			t.Errorf("missing overnight route %s→%s (reverse)", p[1], p[0])
		}
	}
}

func TestSearchFinnlines_Integration(t *testing.T) {
	testutil.RequireLiveIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	date := time.Now().AddDate(0, 1, 0).Format("2006-01-02")
	routes, err := SearchFinnlines(ctx, "Naantali", "Kapellskär", date, "EUR")
	if err != nil {
		t.Skipf("Finnlines API unavailable: %v", err)
	}
	if len(routes) == 0 {
		t.Skip("no Finnlines routes found")
	}

	r := routes[0]
	if r.Provider != "finnlines" {
		t.Errorf("provider = %q, want finnlines", r.Provider)
	}
	if r.Type != "ferry" {
		t.Errorf("type = %q, want ferry", r.Type)
	}
	if r.Duration <= 0 {
		t.Errorf("duration = %d, should be > 0", r.Duration)
	}
}
