package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
)

func TestFlightSummary_SelfConnectWarning(t *testing.T) {
	t.Parallel()
	result := &models.FlightSearchResult{
		Success: true,
		Count:   1,
		Flights: []models.FlightResult{
			{Price: 121, Currency: "EUR", Provider: "kiwi", SelfConnect: true, Warnings: []string{"Self-connect warning"}},
		},
	}

	summary := flightSummary(result, "HEL", "DBV")
	if !strings.Contains(summary, "self-connect") {
		t.Fatalf("summary = %q, want self-connect warning", summary)
	}
}

// --- hotelSummary ---

func TestHotelSummary_NoResults(t *testing.T) {
	t.Parallel()
	result := &models.HotelSearchResult{Success: true, Count: 0}
	summary := hotelSummary(result, "Helsinki")
	if !strings.Contains(summary, "No hotels found") {
		t.Errorf("summary = %q", summary)
	}
}

func TestHotelSummary_WithError(t *testing.T) {
	t.Parallel()
	result := &models.HotelSearchResult{Success: false, Error: "search failed"}
	summary := hotelSummary(result, "Helsinki")
	if !strings.Contains(summary, "search failed") {
		t.Errorf("summary = %q", summary)
	}
}

func TestHotelSummary_WithHotels(t *testing.T) {
	t.Parallel()
	result := &models.HotelSearchResult{
		Success: true,
		Count:   2,
		Hotels: []models.HotelResult{
			{Name: "Budget Inn", Price: 80, Currency: "EUR", Rating: 7.0},
			{Name: "Grand Hotel", Price: 250, Currency: "EUR", Rating: 9.6},
		},
	}
	summary := hotelSummary(result, "Helsinki")
	if !strings.Contains(summary, "Found 2 hotels") {
		t.Errorf("summary = %q", summary)
	}
	if !strings.Contains(summary, "80") {
		t.Error("summary should contain cheapest price")
	}
	if !strings.Contains(summary, "Grand Hotel") {
		t.Error("summary should contain highest-rated hotel")
	}
}

func TestHotelSummary_WithBookingMatches(t *testing.T) {
	t.Parallel()
	result := &models.HotelSearchResult{
		Success: true,
		Count:   1,
		Hotels: []models.HotelResult{{
			Name:     "Grand Hotel",
			Price:    120,
			Currency: "EUR",
			Sources: []models.PriceSource{
				{Provider: "google_hotels", Price: 150, Currency: "EUR"},
				{Provider: "booking", Price: 120, Currency: "EUR"},
			},
		}},
	}

	summary := hotelSummary(result, "Helsinki")
	if !strings.Contains(summary, "Booking.com") {
		t.Fatalf("summary = %q, want Booking.com provider note", summary)
	}
}

// --- flightSuggestions ---

func TestFlightSuggestions_NoResults(t *testing.T) {
	t.Parallel()
	result := &models.FlightSearchResult{Success: false, Count: 0}
	suggestions := flightSuggestions(result, "HEL", "NRT", "2026-06-15", flights.SearchOptions{})
	if suggestions != nil {
		t.Errorf("expected nil suggestions for no results, got %d", len(suggestions))
	}
}

func TestFlightSuggestions_OneWay(t *testing.T) {
	t.Parallel()
	result := &models.FlightSearchResult{
		Success: true,
		Count:   1,
		Flights: []models.FlightResult{{Price: 500, Stops: 0}},
	}
	suggestions := flightSuggestions(result, "HEL", "NRT", "2026-06-15", flights.SearchOptions{})
	// Should suggest round-trip.
	hasRoundTrip := false
	for _, s := range suggestions {
		if strings.Contains(s.Description, "round-trip") {
			hasRoundTrip = true
		}
	}
	if !hasRoundTrip {
		t.Error("should suggest round-trip for one-way search")
	}
}

func TestFlightSuggestions_Economy(t *testing.T) {
	t.Parallel()
	result := &models.FlightSearchResult{
		Success: true,
		Count:   1,
		Flights: []models.FlightResult{{Price: 500, Stops: 0}},
	}
	suggestions := flightSuggestions(result, "HEL", "NRT", "2026-06-15", flights.SearchOptions{})
	// Should suggest business class.
	hasBusiness := false
	for _, s := range suggestions {
		if strings.Contains(s.Description, "business") {
			hasBusiness = true
		}
	}
	if !hasBusiness {
		t.Error("should suggest business class for economy search")
	}
}

// --- hotelSuggestions ---

func TestHotelSuggestions_NoResults(t *testing.T) {
	t.Parallel()
	result := &models.HotelSearchResult{Success: false, Count: 0}
	suggestions := hotelSuggestions(result, hotels.HotelSearchOptions{})
	if suggestions != nil {
		t.Errorf("expected nil suggestions for no results, got %d", len(suggestions))
	}
}

func TestHotelSuggestions_NoStarFilter(t *testing.T) {
	t.Parallel()
	result := &models.HotelSearchResult{
		Success: true,
		Count:   1,
		Hotels:  []models.HotelResult{{Name: "Hotel", Price: 100}},
	}
	suggestions := hotelSuggestions(result, hotels.HotelSearchOptions{})
	hasStar := false
	for _, s := range suggestions {
		if strings.Contains(s.Description, "4+ star") {
			hasStar = true
		}
	}
	if !hasStar {
		t.Error("should suggest star filter when none applied")
	}
}

func TestHotelSuggestions_HighRatedHotel(t *testing.T) {
	t.Parallel()
	result := &models.HotelSearchResult{
		Success: true,
		Count:   1,
		Hotels: []models.HotelResult{
			{Name: "Awesome Hotel", Price: 200, Rating: 9.4, HotelID: "/g/123"},
		},
	}
	suggestions := hotelSuggestions(result, hotels.HotelSearchOptions{
		CheckIn:  "2026-06-15",
		CheckOut: "2026-06-18",
	})
	hasPricing := false
	for _, s := range suggestions {
		if strings.Contains(s.Description, "pricing") {
			hasPricing = true
		}
	}
	if !hasPricing {
		t.Error("should suggest detailed pricing for highly rated hotel")
	}
}

// --- handleSearchFlights validation ---

func TestHandleSearchFlights_MissingOriginDest(t *testing.T) {
	t.Parallel()
	_, _, err := handleSearchFlights(context.Background(), map[string]any{
		"departure_date": "2026-06-15",
	}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for missing origin/destination")
	}
}

func TestHandleSearchFlights_InvalidIATA(t *testing.T) {
	t.Parallel()
	_, _, err := handleSearchFlights(context.Background(), map[string]any{
		"origin":         "XX", // too short, even uppercased
		"destination":    "NRT",
		"departure_date": "2026-06-15",
	}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for invalid IATA code")
	}
}

func TestHandleSearchFlights_MissingDate(t *testing.T) {
	t.Parallel()
	_, _, err := handleSearchFlights(context.Background(), map[string]any{
		"origin":      "HEL",
		"destination": "NRT",
	}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for missing date")
	}
}

func TestHandleSearchFlights_InvalidCabinClass(t *testing.T) {
	t.Parallel()
	_, _, err := handleSearchFlights(context.Background(), map[string]any{
		"origin":         "HEL",
		"destination":    "NRT",
		"departure_date": "2026-06-15",
		"cabin_class":    "invalid_class",
	}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for invalid cabin class")
	}
}

func TestHandleSearchFlights_InvalidMaxStops(t *testing.T) {
	t.Parallel()
	_, _, err := handleSearchFlights(context.Background(), map[string]any{
		"origin":         "HEL",
		"destination":    "NRT",
		"departure_date": "2026-06-15",
		"max_stops":      "invalid_stops",
	}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for invalid max stops")
	}
}

func TestHandleSearchFlights_InvalidSortBy(t *testing.T) {
	t.Parallel()
	_, _, err := handleSearchFlights(context.Background(), map[string]any{
		"origin":         "HEL",
		"destination":    "NRT",
		"departure_date": "2026-06-15",
		"sort_by":        "invalid_sort",
	}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for invalid sort_by")
	}
}

func TestHandleSearchFlights_PastDate(t *testing.T) {
	t.Parallel()
	_, _, err := handleSearchFlights(context.Background(), map[string]any{
		"origin":         "HEL",
		"destination":    "NRT",
		"departure_date": "2020-01-01",
	}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for past date")
	}
}

func TestHandleSearchFlights_RejectsBadProvider(t *testing.T) {
	t.Parallel()
	// Provider validation runs after schema validation; supply a future
	// date so the dispatch layer is reached and the unsupported-provider
	// error path is exercised.
	_, _, err := handleSearchFlights(context.Background(), map[string]any{
		"origin":         "HEL",
		"destination":    "NRT",
		"departure_date": "2099-06-15",
		"provider":       "not_a_real_provider",
	}, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for unsupported provider")
	}
	if got := err.Error(); !strings.Contains(got, "unsupported provider") {
		t.Errorf("error %q should mention 'unsupported provider'", got)
	}
}

func TestHandleSearchFlights_InvalidReturnDate(t *testing.T) {
	t.Parallel()
	_, _, err := handleSearchFlights(context.Background(), map[string]any{
		"origin":         "HEL",
		"destination":    "NRT",
		"departure_date": "2026-06-15",
		"return_date":    "invalid",
	}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for invalid return date")
	}
}

// --- handleSearchDates validation ---

func TestHandleSearchDates_MissingParams(t *testing.T) {
	t.Parallel()
	_, _, err := handleSearchDates(context.Background(), map[string]any{}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for missing params")
	}
}

func TestHandleSearchDates_InvalidIATA(t *testing.T) {
	t.Parallel()
	_, _, err := handleSearchDates(context.Background(), map[string]any{
		"origin":      "XX", // too short, even uppercased
		"destination": "NRT",
		"start_date":  "2026-06-01",
		"end_date":    "2026-06-30",
	}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for invalid origin IATA")
	}
}

func TestHandleSearchDates_InvalidDestIATA(t *testing.T) {
	t.Parallel()
	_, _, err := handleSearchDates(context.Background(), map[string]any{
		"origin":      "HEL",
		"destination": "12", // too short, even uppercased
		"start_date":  "2026-06-01",
		"end_date":    "2026-06-30",
	}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for invalid destination IATA")
	}
}

func TestHandleSearchDates_InvalidDateRange(t *testing.T) {
	t.Parallel()
	_, _, err := handleSearchDates(context.Background(), map[string]any{
		"origin":      "HEL",
		"destination": "NRT",
		"start_date":  "2026-06-30",
		"end_date":    "2026-06-01",
	}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for reversed date range")
	}
}

func TestHandleSearchDates_RoundTripRequiresDuration(t *testing.T) {
	t.Parallel()
	_, _, err := handleSearchDates(context.Background(), map[string]any{
		"origin":        "HEL",
		"destination":   "NRT",
		"start_date":    "2026-06-01",
		"end_date":      "2026-06-30",
		"is_round_trip": true,
		// trip_duration deliberately omitted
	}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for round-trip search without trip_duration")
	}
}

func TestHandleSearchDates_NegativeDurationRejected(t *testing.T) {
	t.Parallel()
	_, _, err := handleSearchDates(context.Background(), map[string]any{
		"origin":        "HEL",
		"destination":   "NRT",
		"start_date":    "2026-06-01",
		"end_date":      "2026-06-30",
		"trip_duration": -3,
	}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for negative trip_duration")
	}
}

// --- handleSearchHotels validation ---

func TestHandleSearchHotels_MissingParams(t *testing.T) {
	t.Parallel()
	_, _, err := handleSearchHotels(context.Background(), map[string]any{}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for missing params")
	}
}

func TestHandleSearchHotels_InvalidDateRange(t *testing.T) {
	t.Parallel()
	_, _, err := handleSearchHotels(context.Background(), map[string]any{
		"location":  "Helsinki",
		"check_in":  "2026-06-22",
		"check_out": "2026-06-15",
	}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for reversed dates")
	}
}

func TestHandleSearchHotels_MissingLocation(t *testing.T) {
	t.Parallel()
	_, _, err := handleSearchHotels(context.Background(), map[string]any{
		"check_in":  "2026-06-15",
		"check_out": "2026-06-18",
	}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for missing location")
	}
}

// --- handleHotelPrices validation ---

func TestHandleHotelPrices_MissingParams(t *testing.T) {
	t.Parallel()
	_, _, err := handleHotelPrices(context.Background(), map[string]any{}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for missing params")
	}
}

func TestHandleHotelPrices_InvalidDateRange(t *testing.T) {
	t.Parallel()
	_, _, err := handleHotelPrices(context.Background(), map[string]any{
		"hotel_id":  "/g/123",
		"check_in":  "2026-06-22",
		"check_out": "2026-06-15",
	}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for reversed dates")
	}
}

func TestHandleHotelPrices_MissingHotelID(t *testing.T) {
	t.Parallel()
	_, _, err := handleHotelPrices(context.Background(), map[string]any{
		"check_in":  "2026-06-15",
		"check_out": "2026-06-18",
	}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for missing hotel_id")
	}
}

func TestHandleHotelPrices_DefaultCurrency(t *testing.T) {
	t.Parallel()
	// Empty currency should default to "USD", not error.
	_, _, err := handleHotelPrices(context.Background(), map[string]any{
		"hotel_id":  "/g/abc",
		"check_in":  "2026-06-15",
		"check_out": "2026-06-18",
	}, nil, nil, nil)
	// Will fail because it hits real API, but should not fail on parameter validation.
	if err != nil && strings.Contains(err.Error(), "currency") {
		t.Error("should not error on missing currency (defaults to USD)")
	}
}

// --- isLocalhostOrigin ---

func TestIsLocalhostOrigin(t *testing.T) {
	t.Parallel()
	tests := []struct {
		origin string
		want   bool
	}{
		{"http://localhost:3000", true},
		{"http://localhost:8080", true},
		{"http://127.0.0.1:3000", true},
		{"http://[::1]:3000", true},
		{"https://evil.com", false},
		{"", false},
		{"http://example.com", false},
		{"not-a-url", false},
	}
	for _, tt := range tests {
		got := isLocalhostOrigin(tt.origin)
		if got != tt.want {
			t.Errorf("isLocalhostOrigin(%q) = %v, want %v", tt.origin, got, tt.want)
		}
	}
}
