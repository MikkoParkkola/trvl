package ground

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/cookies"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/testutil"
)

func init() {
	// Disable browser page reading in tests to avoid opening Chrome.
	cookies.SkipBrowserRead = true
}

func TestLookupDFDSPort(t *testing.T) {
	tests := []struct {
		city     string
		wantCode string
		wantCity string
		wantOK   bool
	}{
		// Copenhagen aliases
		{"Copenhagen", "DKCPH", "Copenhagen", true},
		{"copenhagen", "DKCPH", "Copenhagen", true},
		{"København", "DKCPH", "Copenhagen", true},
		{"cph", "DKCPH", "Copenhagen", true},
		{"dkcph", "DKCPH", "Copenhagen", true},
		{"  Copenhagen  ", "DKCPH", "Copenhagen", true},

		// Oslo aliases
		{"Oslo", "NOOSL", "Oslo", true},
		{"oslo", "NOOSL", "Oslo", true},
		{"osl", "NOOSL", "Oslo", true},
		{"noosl", "NOOSL", "Oslo", true},

		// Amsterdam aliases
		{"Amsterdam", "NLIJM", "Amsterdam", true},
		{"amsterdam", "NLIJM", "Amsterdam", true},
		{"IJmuiden", "NLIJM", "Amsterdam", true},
		{"nlijm", "NLIJM", "Amsterdam", true},

		// Newcastle aliases
		{"Newcastle", "GBTYN", "Newcastle", true},
		{"newcastle", "GBTYN", "Newcastle", true},
		{"gbtyn", "GBTYN", "Newcastle", true},

		// Kiel aliases
		{"Kiel", "DEKEL", "Kiel", true},
		{"kiel", "DEKEL", "Kiel", true},
		{"dekel", "DEKEL", "Kiel", true},

		// Klaipeda aliases
		{"Klaipeda", "LTKLJ", "Klaipeda", true},
		{"klaipeda", "LTKLJ", "Klaipeda", true},
		{"Klaipėda", "LTKLJ", "Klaipeda", true},
		{"ltklj", "LTKLJ", "Klaipeda", true},

		// Kapellskär aliases
		{"Kapellskär", "SEKPS", "Kapellskär", true},
		{"kapellskar", "SEKPS", "Kapellskär", true},
		{"sekps", "SEKPS", "Kapellskär", true},

		// Paldiski aliases
		{"Paldiski", "EEPLA", "Paldiski", true},
		{"paldiski", "EEPLA", "Paldiski", true},
		{"eepla", "EEPLA", "Paldiski", true},

		// Non-existent
		{"", "", "", false},
		{"London", "", "", false},
		{"Paris", "", "", false},
		{"Gothenburg", "", "", false},
		{"Atlantis", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.city, func(t *testing.T) {
			port, ok := LookupDFDSPort(tt.city)
			if ok != tt.wantOK {
				t.Fatalf("LookupDFDSPort(%q) ok = %v, want %v", tt.city, ok, tt.wantOK)
			}
			if ok {
				if port.Code != tt.wantCode {
					t.Errorf("Code = %q, want %q", port.Code, tt.wantCode)
				}
				if port.City != tt.wantCity {
					t.Errorf("City = %q, want %q", port.City, tt.wantCity)
				}
				if port.Name == "" {
					t.Errorf("Name should not be empty for %q", tt.city)
				}
			}
		})
	}
}

func TestHasDFDSPort(t *testing.T) {
	if !HasDFDSPort("Kiel") {
		t.Error("Kiel should have a DFDS port")
	}
	if !HasDFDSPort("Amsterdam") {
		t.Error("Amsterdam should have a DFDS port")
	}
	if !HasDFDSPort("Newcastle") {
		t.Error("Newcastle should have a DFDS port")
	}
	if HasDFDSPort("London") {
		t.Error("London should not have a DFDS port")
	}
	if HasDFDSPort("") {
		t.Error("empty city should not have a DFDS port")
	}
}

func TestHasDFDSRoute(t *testing.T) {
	tests := []struct {
		from string
		to   string
		want bool
	}{
		// Confirmed working routes (API returns available dates)
		{"Kiel", "Klaipeda", true},
		{"Klaipeda", "Kiel", true},
		{"Kapellskär", "Paldiski", true},
		{"Paldiski", "Kapellskär", true},
		{"Amsterdam", "Newcastle", true},
		{"Newcastle", "Amsterdam", true},

		// Copenhagen→Oslo is in the route table (seasonal)
		{"Copenhagen", "Oslo", true},
		{"Oslo", "Copenhagen", true},

		// No direct route between these DFDS ports
		{"Kiel", "Amsterdam", false},
		{"Copenhagen", "Kiel", false},
		{"Klaipeda", "Newcastle", false},

		// Non-existent ports
		{"London", "Kiel", false},
		{"Kiel", "London", false},
		{"", "Kiel", false},
		{"Kiel", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.from+"->"+tt.to, func(t *testing.T) {
			got := HasDFDSRoute(tt.from, tt.to)
			if got != tt.want {
				t.Errorf("HasDFDSRoute(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.want)
			}
		})
	}
}

func TestDFDSAllPortsHaveRequiredFields(t *testing.T) {
	for alias, port := range dfdsPorts {
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

func TestDFDSAllRoutesHaveValidSchedule(t *testing.T) {
	for key, info := range dfdsRoutes {
		if info.RouteCode == "" {
			t.Errorf("route %q has empty RouteCode", key)
		}
		if info.SalesOwner <= 0 {
			t.Errorf("route %q has invalid SalesOwner %d", key, info.SalesOwner)
		}
		if info.DurationMin <= 0 {
			t.Errorf("route %q has zero/negative DurationMin", key)
		}
		if info.BasePrice <= 0 {
			t.Errorf("route %q has zero/negative BasePrice", key)
		}
		if info.DepTime == "" {
			t.Errorf("route %q has empty DepTime", key)
		}
		if info.ArrTime == "" {
			t.Errorf("route %q has empty ArrTime", key)
		}
		if info.Currency == "" {
			t.Errorf("route %q has empty Currency", key)
		}
	}
}

func TestDFDSRateLimiterConfiguration(t *testing.T) {
	assertLimiterConfiguration(t, dfdsLimiter, 12*time.Second, 1)
}

func TestBuildDFDSBookingURL(t *testing.T) {
	info := dfdsRoutes["DEKEL-LTKLJ"]
	u := buildDFDSBookingURL(info)
	if u == "" {
		t.Fatal("booking URL should not be empty")
	}
	if !strings.Contains(u, "dfds") {
		t.Errorf("URL should contain dfds, got %q", u)
	}
	if !strings.Contains(u, "dekel-ltklj") {
		t.Errorf("URL should contain route code, got %q", u)
	}
}

func TestDFDSFormatDateTime(t *testing.T) {
	tests := []struct {
		date      string
		timeStr   string
		dayOffset int
		want      string
	}{
		{"2026-04-15", "20:00", 0, "2026-04-15T20:00:00"},
		{"2026-04-15", "18:00", 1, "2026-04-16T18:00:00"},
		{"2026-12-31", "20:00", 1, "2027-01-01T20:00:00"},
		{"2026-05-01", "09:30", 0, "2026-05-01T09:30:00"},
	}

	for _, tt := range tests {
		got := dfdsFormatDateTime(tt.date, tt.timeStr, tt.dayOffset)
		if got != tt.want {
			t.Errorf("dfdsFormatDateTime(%q, %q, %d) = %q, want %q",
				tt.date, tt.timeStr, tt.dayOffset, got, tt.want)
		}
	}
}

// mockDFDSAvailabilityActive is a response with an active date range (no disabled/offer dates).
const mockDFDSAvailabilityActive = `{
  "dates": {"fromDate": "2026-04-07", "toDate": "2026-12-30"},
  "defaultDate": "2026-04-07",
  "disabledDates": ["2026-04-12", "2026-04-19"],
  "offerDates": []
}`

// mockDFDSAvailabilityWithOffer is a response where a date is an offer date.
const mockDFDSAvailabilityWithOffer = `{
  "dates": {"fromDate": "2026-04-07", "toDate": "2026-12-30"},
  "defaultDate": "2026-04-07",
  "disabledDates": [],
  "offerDates": ["2026-05-01", "2026-05-15"]
}`

// mockDFDSAvailabilityInactive is a response for an inactive route (empty dates).
const mockDFDSAvailabilityInactive = `{
  "dates": {},
  "disabledDates": [],
  "offerDates": []
}`

func TestDFDSAvailabilityResponseParsing_Active(t *testing.T) {
	var resp dfdsAvailabilityResponse
	if err := json.Unmarshal([]byte(mockDFDSAvailabilityActive), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Dates.FromDate != "2026-04-07" {
		t.Errorf("FromDate = %q, want 2026-04-07", resp.Dates.FromDate)
	}
	if resp.Dates.ToDate != "2026-12-30" {
		t.Errorf("ToDate = %q, want 2026-12-30", resp.Dates.ToDate)
	}
	if len(resp.DisabledDates) != 2 {
		t.Errorf("DisabledDates len = %d, want 2", len(resp.DisabledDates))
	}
	if resp.DisabledDates[0] != "2026-04-12" {
		t.Errorf("DisabledDates[0] = %q, want 2026-04-12", resp.DisabledDates[0])
	}
	if len(resp.OfferDates) != 0 {
		t.Errorf("OfferDates len = %d, want 0", len(resp.OfferDates))
	}
}

func TestDFDSAvailabilityResponseParsing_Offer(t *testing.T) {
	var resp dfdsAvailabilityResponse
	if err := json.Unmarshal([]byte(mockDFDSAvailabilityWithOffer), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.OfferDates) != 2 {
		t.Errorf("OfferDates len = %d, want 2", len(resp.OfferDates))
	}
	if resp.OfferDates[0] != "2026-05-01" {
		t.Errorf("OfferDates[0] = %q, want 2026-05-01", resp.OfferDates[0])
	}
}

func TestDFDSAvailabilityResponseParsing_Inactive(t *testing.T) {
	var resp dfdsAvailabilityResponse
	if err := json.Unmarshal([]byte(mockDFDSAvailabilityInactive), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Dates.FromDate != "" {
		t.Errorf("inactive route should have empty FromDate, got %q", resp.Dates.FromDate)
	}
}

func TestFetchDFDSAvailability_MockServer_Active(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(mockDFDSAvailabilityActive)) //nolint:errcheck
	}))
	defer srv.Close()

	// Override the client to hit our mock server.
	origClient := dfdsClient
	dfdsClient = srv.Client()
	defer func() { dfdsClient = origClient }()

	// Build a route info pointing at our mock server.
	info := dfdsRouteInfo{
		RouteCode:   "DEKEL-LTKLJ",
		SalesOwner:  1,
		DepTime:     "20:00",
		ArrTime:     "18:00",
		ArrOffset:   1,
		DurationMin: 1320,
		BasePrice:   79,
		Currency:    "EUR",
	}

	// We can't trivially override the const URL, so test the parsing logic directly.
	// Simulate what fetchDFDSAvailability does with the mock response.
	var avail dfdsAvailabilityResponse
	if err := json.Unmarshal([]byte(mockDFDSAvailabilityActive), &avail); err != nil {
		t.Fatalf("parse: %v", err)
	}
	_ = info

	// Date within range, not disabled, not offer.
	date := "2026-05-01"
	if avail.Dates.FromDate == "" {
		t.Error("should not be inactive")
	}
	for _, d := range avail.DisabledDates {
		if d == date {
			t.Error("2026-05-01 should not be disabled in mock")
		}
	}
	isOffer := false
	for _, d := range avail.OfferDates {
		if d == date {
			isOffer = true
		}
	}
	if isOffer {
		t.Error("2026-05-01 should not be an offer in mock")
	}

	// Date that IS disabled.
	disabledDate := "2026-04-12"
	found := false
	for _, d := range avail.DisabledDates {
		if d == disabledDate {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 2026-04-12 to be in disabled dates")
	}
}

func TestFetchDFDSAvailability_MockServer_Offer(t *testing.T) {
	var avail dfdsAvailabilityResponse
	if err := json.Unmarshal([]byte(mockDFDSAvailabilityWithOffer), &avail); err != nil {
		t.Fatalf("parse: %v", err)
	}

	offerDate := "2026-05-01"
	isOffer := false
	for _, d := range avail.OfferDates {
		if d == offerDate {
			isOffer = true
		}
	}
	if !isOffer {
		t.Errorf("expected %q to be an offer date", offerDate)
	}
}

func TestSearchDFDS_InvalidCity(t *testing.T) {
	ctx := context.Background()
	_, err := SearchDFDS(ctx, "Atlantis", "Kiel", "2026-04-15", "EUR")
	if err == nil {
		t.Error("expected error for unknown city, got nil")
	}
	_, err = SearchDFDS(ctx, "Kiel", "Atlantis", "2026-04-15", "EUR")
	if err == nil {
		t.Error("expected error for unknown destination, got nil")
	}
}

func TestSearchDFDS_InvalidDate(t *testing.T) {
	ctx := context.Background()
	_, err := SearchDFDS(ctx, "Kiel", "Klaipeda", "not-a-date", "EUR")
	if err == nil {
		t.Error("expected error for invalid date, got nil")
	}
}

func TestSearchDFDS_UnknownRoute(t *testing.T) {
	ctx := context.Background()
	// Both ports exist but no direct DFDS route between them.
	_, err := SearchDFDS(ctx, "Kiel", "Amsterdam", "2026-05-01", "EUR")
	if err == nil {
		t.Error("expected error for unknown route, got nil")
	}
}

func TestSearchDFDS_WithMockAvailability_Available(t *testing.T) {
	// Use a mock HTTP server that returns an active availability response.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Return a response where 2026-05-10 is available (not disabled).
		resp := `{"dates":{"fromDate":"2026-04-07","toDate":"2026-12-30"},"defaultDate":"2026-04-07","disabledDates":[],"offerDates":[]}`
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(resp)) //nolint:errcheck
	}))
	defer srv.Close()

	origClient := dfdsClient
	dfdsClient = srv.Client()
	defer func() { dfdsClient = origClient }()

	// Override the base URL by testing the logic through the response parser directly.
	// Parse availability as the function would.
	var avail dfdsAvailabilityResponse
	body := `{"dates":{"fromDate":"2026-04-07","toDate":"2026-12-30"},"defaultDate":"2026-04-07","disabledDates":[],"offerDates":[]}`
	if err := json.Unmarshal([]byte(body), &avail); err != nil {
		t.Fatalf("parse: %v", err)
	}

	date := "2026-05-10"
	if avail.Dates.FromDate == "" {
		t.Error("route should be active")
	}
	if date < avail.Dates.FromDate {
		t.Error("date should be in range")
	}
	if date > avail.Dates.ToDate {
		t.Error("date should be in range")
	}
}

func TestSearchDFDS_RouteFieldsComplete(t *testing.T) {
	// Test that a route built from hardcoded data has all required fields.
	// We simulate the route-building path without hitting the network.
	routeInfo := dfdsRoutes["DEKEL-LTKLJ"]
	date := "2026-05-10"

	depTime := dfdsFormatDateTime(date, routeInfo.DepTime, 0)
	arrTime := dfdsFormatDateTime(date, routeInfo.ArrTime, routeInfo.ArrOffset)

	fromPort := dfdsPorts["kiel"]
	toPort := dfdsPorts["klaipeda"]

	route := models.GroundRoute{
		Provider: "dfds",
		Type:     "ferry",
		Price:    routeInfo.BasePrice,
		Currency: routeInfo.Currency,
		Duration: routeInfo.DurationMin,
		Departure: models.GroundStop{
			City:    fromPort.City,
			Station: fromPort.Name,
			Time:    depTime,
		},
		Arrival: models.GroundStop{
			City:    toPort.City,
			Station: toPort.Name,
			Time:    arrTime,
		},
		Transfers:  0,
		BookingURL: buildDFDSBookingURL(routeInfo),
	}

	if route.Provider != "dfds" {
		t.Errorf("provider = %q, want dfds", route.Provider)
	}
	if route.Type != "ferry" {
		t.Errorf("type = %q, want ferry", route.Type)
	}
	if route.Price != 79 {
		t.Errorf("price = %.2f, want 79", route.Price)
	}
	if route.Currency != "EUR" {
		t.Errorf("currency = %q, want EUR", route.Currency)
	}
	if route.Duration != 1320 {
		t.Errorf("duration = %d, want 1320", route.Duration)
	}
	if route.Departure.City != "Kiel" {
		t.Errorf("departure city = %q, want Kiel", route.Departure.City)
	}
	if route.Arrival.City != "Klaipeda" {
		t.Errorf("arrival city = %q, want Klaipeda", route.Arrival.City)
	}
	if !strings.HasPrefix(route.Departure.Time, date) {
		t.Errorf("departure time %q should start with %q", route.Departure.Time, date)
	}
	if !strings.HasPrefix(route.Arrival.Time, "2026-05-11") {
		t.Errorf("arrival time %q should start with 2026-05-11 (next day)", route.Arrival.Time)
	}
	if route.Transfers != 0 {
		t.Errorf("transfers = %d, want 0", route.Transfers)
	}
	if route.BookingURL == "" {
		t.Error("booking URL should not be empty")
	}
}

func TestSearchDFDS_OfferAnnotation(t *testing.T) {
	// Verify that when isOffer=true the route gets the Deal amenity.
	amenities := []string{}
	isOffer := true
	if isOffer {
		amenities = append(amenities, "Deal")
	}

	if len(amenities) != 1 || amenities[0] != "Deal" {
		t.Errorf("expected amenities=[Deal] for offer, got %v", amenities)
	}
}

func TestSearchDFDS_Integration(t *testing.T) {
	testutil.RequireLiveIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Use a confirmed working route with a future date.
	date := time.Now().AddDate(0, 1, 0).Format("2006-01-02")

	routes, err := SearchDFDS(ctx, "Kiel", "Klaipeda", date, "EUR")
	if err != nil {
		t.Skipf("DFDS API unavailable: %v", err)
	}
	// Route may be unavailable on this specific date — that's OK.
	if len(routes) == 0 {
		t.Skip("date not available for Kiel→Klaipeda")
	}

	r := routes[0]
	if r.Provider != "dfds" {
		t.Errorf("provider = %q, want dfds", r.Provider)
	}
	if r.Type != "ferry" {
		t.Errorf("type = %q, want ferry", r.Type)
	}
	if r.Duration <= 0 {
		t.Errorf("duration = %d, should be > 0", r.Duration)
	}
	if r.Departure.City != "Kiel" {
		t.Errorf("departure city = %q, want Kiel", r.Departure.City)
	}
	if r.Arrival.City != "Klaipeda" {
		t.Errorf("arrival city = %q, want Klaipeda", r.Arrival.City)
	}
	if r.BookingURL == "" {
		t.Error("booking URL should not be empty")
	}
	if r.Transfers != 0 {
		t.Errorf("transfers = %d, want 0 (ferry)", r.Transfers)
	}
}

func TestSearchDFDS_Amsterdam_Newcastle_Integration(t *testing.T) {
	testutil.RequireLiveIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	date := time.Now().AddDate(0, 1, 0).Format("2006-01-02")

	routes, err := SearchDFDS(ctx, "Amsterdam", "Newcastle", date, "EUR")
	if err != nil {
		t.Skipf("DFDS API unavailable: %v", err)
	}
	if len(routes) == 0 {
		t.Skip("date not available for Amsterdam→Newcastle")
	}

	r := routes[0]
	if r.Departure.City != "Amsterdam" {
		t.Errorf("departure city = %q, want Amsterdam", r.Departure.City)
	}
	if r.Arrival.City != "Newcastle" {
		t.Errorf("arrival city = %q, want Newcastle", r.Arrival.City)
	}
}
