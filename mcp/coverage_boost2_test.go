package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/hacks"
	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// Test helper constructors for option types.
func hotelsOptsBasic() hotels.HotelSearchOptions {
	return hotels.HotelSearchOptions{}
}

func hotelsOptsWithStars(stars int) hotels.HotelSearchOptions {
	return hotels.HotelSearchOptions{Stars: stars}
}

func flightsOptsEmpty() flights.SearchOptions {
	return flights.SearchOptions{}
}

// ============================================================
// buildHacksSummary
// ============================================================

func TestBuildHacksSummary_NoHacks(t *testing.T) {
	t.Parallel()
	got := buildHacksSummary("HEL", "PRG", "2026-07-01", nil)
	if !strings.Contains(got, "No travel hacks") {
		t.Errorf("expected 'No travel hacks', got %q", got)
	}
	if !strings.Contains(got, "HEL") || !strings.Contains(got, "PRG") {
		t.Error("summary should contain origin and destination")
	}
}

func TestBuildHacksSummary_WithHacks(t *testing.T) {
	t.Parallel()
	detected := []hacks.Hack{
		{Title: "Night bus", Savings: 80, Currency: "EUR", Description: "Take overnight bus"},
		{Title: "Open jaw", Savings: 0, Currency: "EUR", Description: "Fly into different airport"},
	}
	got := buildHacksSummary("HEL", "PRG", "2026-07-01", detected)
	if !strings.Contains(got, "Night bus") {
		t.Error("summary should contain hack title")
	}
	if !strings.Contains(got, "saves EUR 80") {
		t.Error("summary should contain savings for non-zero savings")
	}
	if !strings.Contains(got, "Open jaw") {
		t.Error("summary should contain second hack")
	}
	if strings.Contains(got, "saves EUR 0") {
		t.Error("should not show savings for zero-savings hack")
	}
}

// ============================================================
// buildGroundRouteSummary — new cases not in pure_helpers_test.go
// ============================================================

func TestBuildGroundRouteSummary2_Empty(t *testing.T) {
	t.Parallel()
	got := buildGroundRouteSummary("Found 0 routes", nil)
	if !strings.Contains(got, "Found 0 routes") {
		t.Errorf("should contain header, got %q", got)
	}
}

func TestBuildGroundRouteSummary2_WithPriceRange(t *testing.T) {
	t.Parallel()
	routes := []models.GroundRoute{
		{
			Provider: "regiojet", Type: "bus", Price: 12.00, PriceMax: 18.00, Currency: "EUR",
			Duration: 270, Transfers: 1,
			Departure: models.GroundStop{Time: "2026-07-01T09:30:00"},
			Arrival:   models.GroundStop{Time: "2026-07-01T14:00:00"},
		},
	}
	got := buildGroundRouteSummary("Found routes", routes)
	if !strings.Contains(got, "EUR 12.00-18.00") {
		t.Error("should show price range when PriceMax differs from Price")
	}
}

func TestBuildGroundRouteSummary2_MoreThan10(t *testing.T) {
	t.Parallel()
	routes := make([]models.GroundRoute, 15)
	for i := range routes {
		routes[i] = models.GroundRoute{Provider: "flixbus", Type: "bus", Price: float64(i + 10), Currency: "EUR", Duration: 120, Departure: models.GroundStop{Time: "08:00"}, Arrival: models.GroundStop{Time: "10:00"}}
	}
	got := buildGroundRouteSummary("Found 15 routes", routes)
	if !strings.Contains(got, "5 more routes") {
		t.Errorf("should show overflow count, got %q", got)
	}
}

// ============================================================
// safeTimeSlice
// ============================================================

func TestSafeTimeSlice2(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  string
	}{
		{"2026-07-01T08:30:00", "08:30"},
		{"2026-07-01T23:59:00", "23:59"},
		{"08:30", "08:30"},
		{"", ""},
	}
	for _, tt := range tests {
		got := safeTimeSlice(tt.input)
		if got != tt.want {
			t.Errorf("safeTimeSlice(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ============================================================
// groundRoutesHaveProvider — nil case not in pure_helpers
// ============================================================

func TestGroundRoutesHaveProvider2_Nil(t *testing.T) {
	t.Parallel()
	if groundRoutesHaveProvider(nil, "anything") {
		t.Error("nil routes should return false")
	}
}

// ============================================================
// hotelSummary edge cases
// ============================================================

func TestHotelSummary_SameCheapestAndBestRated(t *testing.T) {
	t.Parallel()
	result := &models.HotelSearchResult{
		Success: true, Count: 1,
		Hotels: []models.HotelResult{{Name: "Only Hotel", Price: 100, Currency: "EUR", Rating: 4.9}},
	}
	summary := hotelSummary(result, "Helsinki")
	if !strings.Contains(summary, "Found 1 hotel") {
		t.Error("should find count")
	}
	count := strings.Count(summary, "Only Hotel")
	if count > 1 {
		t.Errorf("Only Hotel mentioned %d times, should be once", count)
	}
}

func TestHotelSummary_AllZeroPrice(t *testing.T) {
	t.Parallel()
	result := &models.HotelSearchResult{
		Success: true, Count: 2,
		Hotels: []models.HotelResult{{Name: "A", Price: 0, Rating: 4.0}, {Name: "B", Price: 0, Rating: 3.5}},
	}
	summary := hotelSummary(result, "Tokyo")
	if strings.Contains(summary, "Cheapest") {
		t.Error("should not mention cheapest when all prices are 0")
	}
}

// ============================================================
// flightSummary edge cases
// ============================================================

func TestFlightSummary_MultiStop(t *testing.T) {
	t.Parallel()
	result := &models.FlightSearchResult{
		Success: true, Count: 1,
		Flights: []models.FlightResult{{Price: 300, Currency: "EUR", Stops: 2, Legs: []models.FlightLeg{{Airline: "Lufthansa"}}}},
	}
	summary := flightSummary(result, "HEL", "NRT")
	if !strings.Contains(summary, "2 stops") {
		t.Error("should mention multi-stops")
	}
}

func TestFlightSummary_OnlyZeroPriceFlights(t *testing.T) {
	t.Parallel()
	result := &models.FlightSearchResult{
		Success: true, Count: 2,
		Flights: []models.FlightResult{{Price: 0, Currency: "EUR"}, {Price: 0, Currency: "EUR"}},
	}
	summary := flightSummary(result, "HEL", "NRT")
	if !strings.Contains(summary, "Found 2 flights") {
		t.Error("should find count")
	}
}

// ============================================================
// handleDetectTravelHacks
// ============================================================

func TestHandleDetectTravelHacks_MissingOrigin(t *testing.T) {
	t.Parallel()
	content, result, err := handleDetectTravelHacks(context.Background(), map[string]any{
		"destination": "PRG", "date": "2026-07-01",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || len(content) == 0 {
		t.Fatal("expected non-nil result and content")
	}
}

func TestHandleDetectTravelHacks_DefaultCurrency(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping live HTTP test in short mode")
	}
	content, result, err := handleDetectTravelHacks(context.Background(), map[string]any{
		"origin": "HEL", "destination": "PRG", "date": "2026-07-01",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || len(content) == 0 {
		t.Fatal("expected non-nil result and content")
	}
}

// ============================================================
// handleDetectAccommodationHacks
// ============================================================

func TestHandleDetectAccommodationHacks_Minimal(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping live HTTP test in short mode")
	}
	content, result, err := handleDetectAccommodationHacks(context.Background(), map[string]any{
		"city": "Prague", "checkin": "2026-07-01", "checkout": "2026-07-05",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || len(content) == 0 {
		t.Fatal("expected non-nil result and content")
	}
}

// ============================================================
// handleGetBaggageRules — empty code returns all airlines
// ============================================================

func TestHandleGetBaggageRules_EmptyCodeReturnsAll(t *testing.T) {
	t.Parallel()
	content, result, err := handleGetBaggageRules(context.Background(), map[string]any{"airline_code": ""}, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(content) == 0 {
		t.Fatal("expected content blocks")
	}
}

func TestHandleGetBaggageRules_SpecificAirline(t *testing.T) {
	t.Parallel()
	content, result, err := handleGetBaggageRules(context.Background(), map[string]any{"airline_code": "KL"}, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(content) == 0 {
		t.Fatal("expected content blocks")
	}
}

func TestHandleGetBaggageRules_UnknownAirline(t *testing.T) {
	t.Parallel()
	content, result, err := handleGetBaggageRules(context.Background(), map[string]any{"airline_code": "ZZ"}, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(content) == 0 {
		t.Fatal("expected content blocks")
	}
	if !strings.Contains(content[0].Text, "not found") {
		t.Error("should indicate airline not found")
	}
}

// ============================================================
// handleHotelRooms validation
// ============================================================

func TestHandleHotelRooms_MissingAll(t *testing.T) {
	t.Parallel()
	_, _, err := handleHotelRooms(context.Background(), map[string]any{}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for missing params")
	}
}

func TestHandleHotelRooms_MissingHotelName(t *testing.T) {
	t.Parallel()
	_, _, err := handleHotelRooms(context.Background(), map[string]any{"check_in": "2026-07-01", "check_out": "2026-07-05"}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for missing hotel_name")
	}
}

func TestHandleHotelRooms_InvalidDateRange(t *testing.T) {
	t.Parallel()
	_, _, err := handleHotelRooms(context.Background(), map[string]any{"hotel_name": "Hilton Helsinki", "check_in": "2026-07-10", "check_out": "2026-07-05"}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for reversed dates")
	}
}

// ============================================================
// handleSearchRoute validation
// ============================================================

func TestHandleSearchRoute2_MissingOrigin(t *testing.T) {
	t.Parallel()
	_, _, err := handleSearchRoute(context.Background(), map[string]any{"destination": "BCN", "date": "2026-07-01"}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for missing origin")
	}
}

func TestHandleSearchRoute2_MissingDate(t *testing.T) {
	t.Parallel()
	_, _, err := handleSearchRoute(context.Background(), map[string]any{"origin": "HEL", "destination": "BCN"}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for missing date")
	}
}

// ============================================================
// hotelSuggestions
// ============================================================

func TestHotelSuggestions_WithStarFilter(t *testing.T) {
	t.Parallel()
	result := &models.HotelSearchResult{Success: true, Count: 1, Hotels: []models.HotelResult{{Name: "Hotel", Price: 100}}}
	suggestions := hotelSuggestions(result, hotelsOptsWithStars(4))
	for _, s := range suggestions {
		if strings.Contains(s.Description, "4+ star") {
			t.Error("should not suggest star filter when already applied")
		}
	}
}

func TestHotelSuggestions_ExpensiveResults(t *testing.T) {
	t.Parallel()
	result := &models.HotelSearchResult{
		Success: true, Count: 4,
		Hotels: []models.HotelResult{{Name: "A", Price: 400}, {Name: "B", Price: 500}, {Name: "C", Price: 350}, {Name: "D", Price: 100}},
	}
	suggestions := hotelSuggestions(result, hotelsOptsBasic())
	hasExpansion := false
	for _, s := range suggestions {
		if strings.Contains(s.Description, "expanding") {
			hasExpansion = true
		}
	}
	if !hasExpansion {
		t.Error("should suggest expanding search when most results are expensive")
	}
}

func TestHotelSuggestions_ManyReviews(t *testing.T) {
	t.Parallel()
	result := &models.HotelSearchResult{
		Success: true, Count: 1,
		Hotels: []models.HotelResult{{Name: "Popular Hotel", Price: 100, ReviewCount: 500, HotelID: "/g/123"}},
	}
	suggestions := hotelSuggestions(result, hotelsOptsBasic())
	hasReview := false
	for _, s := range suggestions {
		if strings.Contains(s.Description, "reviews") {
			hasReview = true
		}
	}
	if !hasReview {
		t.Error("should suggest reading reviews for hotel with many reviews")
	}
}

// ============================================================
// flightSuggestions
// ============================================================

func TestFlightSuggestions_WithMultiStop(t *testing.T) {
	t.Parallel()
	result := &models.FlightSearchResult{
		Success: true, Count: 2,
		Flights: []models.FlightResult{{Price: 300, Stops: 0}, {Price: 200, Stops: 2}},
	}
	suggestions := flightSuggestions(result, "HEL", "NRT", "2026-07-01", flightsOptsEmpty())
	hasNonstop := false
	for _, s := range suggestions {
		if strings.Contains(s.Description, "nonstop") {
			hasNonstop = true
		}
	}
	if !hasNonstop {
		t.Error("should suggest nonstop filter when multi-stop flights exist")
	}
}

func TestFlightSuggestions_SurfacesDoorToDoorTransfer(t *testing.T) {
	t.Parallel()
	result := &models.FlightSearchResult{
		Success: true, Count: 1,
		Flights: []models.FlightResult{{Price: 200, Stops: 0}},
	}
	suggestions := flightSuggestions(result, "HEL", "BCN", "2026-07-18", flightsOptsEmpty())
	var hasTransfer, hasJourney bool
	for _, s := range suggestions {
		if s.Action == "search_airport_transfers" && s.Params["airport_code"] == "BCN" {
			hasTransfer = true
		}
		if s.Action == "plan_journey" && s.Params["airport_code"] == "HEL" {
			hasJourney = true
		}
	}
	if !hasTransfer {
		t.Error("should proactively surface the arrival airport transfer (A.1)")
	}
	if !hasJourney {
		t.Error("should proactively surface the departure leave-by schedule (A.1)")
	}
}

func TestFlightSuggestions_WidelyVaryingPrices(t *testing.T) {
	t.Parallel()
	result := &models.FlightSearchResult{
		Success: true, Count: 3,
		Flights: []models.FlightResult{{Price: 100}, {Price: 500}, {Price: 300}},
	}
	suggestions := flightSuggestions(result, "HEL", "NRT", "2026-07-01", flightsOptsEmpty())
	hasDates := false
	for _, s := range suggestions {
		if s.Action == "search_dates" {
			hasDates = true
		}
	}
	if !hasDates {
		t.Error("should suggest date search when prices vary widely")
	}
}
