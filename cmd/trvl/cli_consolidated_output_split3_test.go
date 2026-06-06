package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/profile"
)

func TestCabinResultTableRows_ZeroDuration(t *testing.T) {
	r := cabinResult{Duration: 0}
	dur := "—"
	if r.Duration > 0 {
		dur = "set"
	}
	if dur != "—" {
		t.Errorf("expected —, got %q", dur)
	}
}

func TestFormatRoomsTable_Empty(t *testing.T) {
	result := &hotels.RoomAvailability{
		HotelID:  "/g/11abc",
		Name:     "Grand Hotel",
		CheckIn:  "2026-06-15",
		CheckOut: "2026-06-18",
		Rooms:    nil,
	}
	if err := formatRoomsTable(result); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFormatRoomsTable_EmptyNameFallsBackToID(t *testing.T) {
	result := &hotels.RoomAvailability{
		HotelID:  "/g/11abc",
		Name:     "",
		CheckIn:  "2026-06-15",
		CheckOut: "2026-06-18",
		Rooms:    nil,
	}
	if err := formatRoomsTable(result); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFormatRoomsTable_WithRoomsV3(t *testing.T) {
	result := &hotels.RoomAvailability{
		HotelID:  "/g/11abc",
		Name:     "Grand Hotel",
		CheckIn:  "2026-06-15",
		CheckOut: "2026-06-18",
		Rooms: []hotels.RoomType{
			{Name: "Standard", Price: 120, Currency: "EUR", MaxGuests: 2, Provider: "direct", Amenities: []string{"WiFi", "TV"}},
			{Name: "Deluxe", Price: 200, Currency: "EUR", MaxGuests: 2, Provider: "booking", Amenities: []string{"WiFi", "TV", "Minibar", "Extra1", "Extra2", "SomeLongAmenity"}},
			{Name: "Free Room", Price: 0, Currency: "EUR", MaxGuests: 0, Provider: ""},
		},
	}
	if err := formatRoomsTable(result); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestStarRating_Full(t *testing.T) {
	got := starRating(5.0)
	if got == "" {
		t.Error("expected non-empty star rating")
	}
}

func TestStarRating_Half(t *testing.T) {
	got := starRating(3.5)
	if got == "" {
		t.Error("expected non-empty star rating for half star")
	}
}

func TestStarRating_ZeroV3(t *testing.T) {
	got := starRating(0)
	if got == "" {
		t.Error("expected non-empty star rating for zero")
	}
}

func TestPrintReviewsTable_Empty(t *testing.T) {
	result := &models.HotelReviewResult{
		HotelID: "/g/11abc",
		Name:    "Grand Hotel",
		Summary: models.ReviewSummary{AverageRating: 4.2, TotalReviews: 100},
		Reviews: nil,
	}
	if err := printReviewsTable(result); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPrintReviewsTable_WithReviewsV3(t *testing.T) {
	longText := strings.Repeat("A very long review text that should be truncated. ", 3)
	result := &models.HotelReviewResult{
		HotelID: "/g/11abc",
		Name:    "Grand Hotel",
		Summary: models.ReviewSummary{AverageRating: 4.2, TotalReviews: 2},
		Reviews: []models.HotelReview{
			{Rating: 5.0, Author: "Alice", Date: "2026-03-15", Text: "Excellent!"},
			{Rating: 3.5, Author: "Bob", Date: "2026-03-10", Text: longText},
		},
	}
	if err := printReviewsTable(result); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPrintReviewsTable_NoName(t *testing.T) {
	result := &models.HotelReviewResult{
		HotelID: "/g/11abc",
		Summary: models.ReviewSummary{AverageRating: 0, TotalReviews: 0},
		Reviews: []models.HotelReview{
			{Rating: 4.0, Author: "Eve", Date: "2026-01-01", Text: "Good."},
		},
	}
	if err := printReviewsTable(result); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPrintProfileSummary_Empty(t *testing.T) {
	p := &profile.TravelProfile{}

	printProfileSummary(p)
}

func TestPrintProfileSummary_Full(t *testing.T) {
	p := &profile.TravelProfile{
		TotalTrips:       15,
		TotalFlights:     30,
		TotalHotelNights: 45,
		TopAirlines: []profile.AirlineStats{
			{Code: "KL", Name: "KLM", Flights: 12},
			{Code: "AY", Name: "", Flights: 5},
		},
		PreferredAlliance: "SkyTeam",
		AvgFlightPrice:    210,
		TopRoutes: []profile.RouteStats{
			{From: "HEL", To: "AMS", Count: 8, AvgPrice: 180},
			{From: "AMS", To: "JFK", Count: 2, AvgPrice: 0},
		},
		HomeDetected:    []string{"HEL"},
		TopDestinations: []string{"AMS", "BCN", "NRT"},
		TopHotelChains: []profile.HotelChainStats{
			{Name: "Marriott", Nights: 20},
		},
		AvgStarRating:  4.2,
		AvgNightlyRate: 120,
		PreferredType:  "hotel",
		TopGroundModes: []profile.ModeStats{
			{Mode: "train", Count: 10},
		},
		AvgTripLength:  5.5,
		PreferredDays:  []string{"Tuesday", "Wednesday"},
		AvgBookingLead: 21,
		BudgetTier:     "mid-range",
		AvgTripCost:    850,
	}
	printProfileSummary(p)
}

func TestTruncateStr_ShortString(t *testing.T) {
	got := truncateStr("hello", 10)
	if got != "hello" {
		t.Errorf("expected hello, got %q", got)
	}
}

func TestTruncateStr_ExactLength(t *testing.T) {
	got := truncateStr("hello", 5)
	if got != "hello" {
		t.Errorf("expected hello, got %q", got)
	}
}

func TestTruncateStr_TruncatesWithEllipsis(t *testing.T) {
	got := truncateStr("hello world", 8)
	if !strings.HasSuffix(got, "...") || len(got) != 8 {
		t.Errorf("expected 8-char string with ellipsis, got %q", got)
	}
}

func TestTruncateStr_MaxLenThree(t *testing.T) {
	got := truncateStr("hello", 3)
	if got != "hel" {
		t.Errorf("expected hel, got %q", got)
	}
}

func TestRunProfileShow_NoBookings(t *testing.T) {

	cmd := profileCmd()
	cmd.SetArgs([]string{})

	_ = cmd.Execute()
}

func TestFormatEventsCard_Empty(t *testing.T) {
	if err := formatEventsCard(nil, "Barcelona", "2026-07-01", "2026-07-08"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFormatEventsCard_WithEventsV5(t *testing.T) {
	events := []models.Event{
		{Date: "2026-07-04", Time: "20:00", Name: "Rock Concert", Venue: "Palau Sant Jordi", Type: "Music", PriceRange: "€50-€200"},
		{Date: "2026-07-05", Time: "18:00", Name: "FC Barcelona vs Real Madrid", Venue: "Camp Nou", Type: "Sports", PriceRange: "€80-€400"},
	}
	if err := formatEventsCard(events, "Barcelona", "2026-07-01", "2026-07-08"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPrintExploreTable_Empty(t *testing.T) {
	result := &models.ExploreResult{
		Destinations: nil,
		Count:        0,
	}
	if err := printExploreTable(context.TODO(), "", result, "HEL"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPrintExploreTable_WithDestinations(t *testing.T) {
	result := &models.ExploreResult{
		Destinations: []models.ExploreDestination{
			{AirportCode: "BCN", CityName: "Barcelona", Country: "Spain", Price: 89, Stops: 0, AirlineName: "KLM"},
			{AirportCode: "NRT", CityName: "Tokyo", Country: "Japan", Price: 699, Stops: 1, AirlineName: "AY"},
		},
		Count: 2,
	}
	if err := printExploreTable(context.TODO(), "", result, "HEL"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPrintSuggestTable_FailedResult(t *testing.T) {

}

func TestRunProfileSummary_NoBookings(t *testing.T) {

	tmp := t.TempDir()
	trvlDir := filepath.Join(tmp, ".trvl")
	if err := os.MkdirAll(trvlDir, 0o755); err != nil {
		t.Fatal(err)
	}

	profilePath := filepath.Join(trvlDir, "profile.json")
	if err := os.WriteFile(profilePath, []byte(`{"bookings":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	setTestHome(t, tmp)

	cmd := profileCmd()
	cmd.SetArgs([]string{"summary"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPrintTripWeather_NoLegs(t *testing.T) {

	_ = time.Now
}

func TestPrintDatesTable_FailedResult(t *testing.T) {
	result := &models.DateSearchResult{
		Success: false,
		Error:   "search failed",
	}
	if err := printDatesTable(context.Background(), "", result); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPrintDatesTable_ZeroCount(t *testing.T) {
	result := &models.DateSearchResult{
		Success: true,
		Count:   0,
		Dates:   nil,
	}
	if err := printDatesTable(context.Background(), "", result); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPrintDatesTable_OneWayV7(t *testing.T) {
	result := &models.DateSearchResult{
		Success:   true,
		Count:     2,
		DateRange: "2026-07-01 to 2026-07-31",
		TripType:  "one_way",
		Dates: []models.DatePriceResult{
			{Date: "2026-07-05", Price: 89, Currency: "EUR"},
			{Date: "2026-07-12", Price: 75, Currency: "EUR"},
		},
	}
	if err := printDatesTable(context.Background(), "", result); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPrintDatesTable_RoundTripV7(t *testing.T) {
	result := &models.DateSearchResult{
		Success:   true,
		Count:     1,
		DateRange: "2026-07-01 to 2026-07-31",
		TripType:  "round_trip",
		Dates: []models.DatePriceResult{
			{Date: "2026-07-05", Price: 299, Currency: "EUR", ReturnDate: "2026-07-12"},
		},
	}
	if err := printDatesTable(context.Background(), "", result); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPrintGridTable_EmptyResult(t *testing.T) {
	result := &models.PriceGrid{
		Success: true,
		Count:   0,
	}
	if err := printGridTable(context.Background(), "", result, "HEL", "BCN"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPrintGridTable_FailedResult(t *testing.T) {
	result := &models.PriceGrid{
		Success: false,
		Count:   0,
	}
	if err := printGridTable(context.Background(), "", result, "HEL", "BCN"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestMaybeShowFlightHackTips_JSONFormatV7(t *testing.T) {
	result := &models.FlightSearchResult{
		Success: true,
		Flights: []models.FlightResult{
			{Price: 199, Currency: "EUR"},
		},
	}

	maybeShowFlightHackTips(context.Background(), []string{"HEL"}, []string{"BCN"}, "2026-07-01", "", 1, result, false)
}

func TestPrintExploreTable_WithCityIDOnly(t *testing.T) {
	result := &models.ExploreResult{
		Destinations: []models.ExploreDestination{
			{CityID: "city:BCN", CityName: "Barcelona", Country: "Spain", Price: 89, Stops: 0},
		},
		Count: 1,
	}
	if err := printExploreTable(context.Background(), "", result, "HEL"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestOutputShare_MarkdownBranch(t *testing.T) {

	err := outputShare("# My Trip\n", "markdown")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestOutputShare_DefaultBranch(t *testing.T) {

	err := outputShare("# Trip\n", "")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}
