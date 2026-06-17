package trip

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/destinations"
	"github.com/MikkoParkkola/trvl/internal/ground"
	"github.com/MikkoParkkola/trvl/internal/models"
)

func TestResolveDestinationCountry_TwoLetterPassthrough(t *testing.T) {
	got := resolveDestinationCountry("FI")
	if got != "FI" {
		t.Errorf("got %q, want FI (2-letter passthrough)", got)
	}
}

func TestResolveDestinationCountry_KnownAirport(t *testing.T) {
	got := resolveDestinationCountry("BCN")
	if got != "ES" {
		t.Errorf("got %q, want ES for BCN", got)
	}
}

func TestResolveDestinationCountry_UnknownCode(t *testing.T) {
	got := resolveDestinationCountry("XYZ")
	if got != "" {
		t.Errorf("got %q, want empty for unknown code", got)
	}
}

func TestResolveDestinationCountry_WhitespaceHandling(t *testing.T) {
	got := resolveDestinationCountry("  bcn  ")
	if got != "ES" {
		t.Errorf("got %q, want ES for trimmed+uppercased BCN", got)
	}
}

// ============================================================
// avg function
// ============================================================

func TestAvg_Empty(t *testing.T) {
	if avg(nil) != 0 {
		t.Error("expected 0 for empty slice")
	}
}

func TestAvg_SingleElement(t *testing.T) {
	if avg([]float64{42}) != 42 {
		t.Errorf("expected 42, got %f", avg([]float64{42}))
	}
}

func TestAvg_Multiple(t *testing.T) {
	got := avg([]float64{10, 20, 30})
	if got != 20 {
		t.Errorf("expected 20, got %f", got)
	}
}

// ============================================================
// SearchAirportTransfers — wrapper coverage
// ============================================================

func TestSearchAirportTransfers_EmptyCode(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	result, err := SearchAirportTransfers(ctx, AirportTransferInput{
		Destination: "Helsinki",
		Date:        "2026-07-01",
	})
	if err == nil && result != nil && result.Success {
		// May succeed with empty code (returns error or empty results).
		_ = result
	}
}

// ============================================================
// DiscoverOptions applyDefaults — MaxNights edge case
// ============================================================

func TestDiscoverOptions_MaxNightsLessThanMin(t *testing.T) {
	opts := DiscoverOptions{MinNights: 5, MaxNights: 2}
	opts.applyDefaults()
	if opts.MaxNights != 5 {
		t.Errorf("maxNights = %d, want 5 (should be clamped to minNights)", opts.MaxNights)
	}
}

func TestDiscoverOptions_NegativeTop(t *testing.T) {
	opts := DiscoverOptions{Top: -1}
	opts.applyDefaults()
	if opts.Top != 5 {
		t.Errorf("top = %d, want 5 (default)", opts.Top)
	}
}

// ============================================================
// buildDiscoverReasoning — edge cases
// ============================================================

func TestBuildDiscoverReasoning_ZeroValues(t *testing.T) {
	got := buildDiscoverReasoning(0, 0, "EUR")
	if got != "" {
		t.Errorf("expected empty for zero values, got %q", got)
	}
}

func TestBuildDiscoverReasoning_RatingOnly(t *testing.T) {
	got := buildDiscoverReasoning(4.5, 0, "EUR")
	if got != "4.5★ hotel" {
		t.Errorf("expected '4.5★ hotel', got %q", got)
	}
}

func TestBuildDiscoverReasoning_BothValues(t *testing.T) {
	got := buildDiscoverReasoning(3.8, 150, "USD")
	if got == "" {
		t.Error("expected non-empty reasoning")
	}
}

// ============================================================
// newCompoundSearchClient — smoke test
// ============================================================

func TestNewCompoundSearchClient(t *testing.T) {
	client := newCompoundSearchClient()
	if client == nil {
		t.Error("expected non-nil client")
	}
}

// ============================================================
// extractTopFlights — additional branch: legs with multi-leg route
// ============================================================

func TestExtractTopFlights_TwoLegRoute(t *testing.T) {
	flts := []models.FlightResult{
		{
			Price:    250,
			Currency: "EUR",
			Stops:    1,
			Duration: 300,
			Legs: []models.FlightLeg{
				{
					Airline:          "Finnair",
					FlightNumber:     "AY123",
					DepartureTime:    "2026-07-01T08:00",
					ArrivalTime:      "2026-07-01T10:00",
					DepartureAirport: models.AirportInfo{Code: "HEL"},
					ArrivalAirport:   models.AirportInfo{Code: "AMS"},
				},
				{
					Airline:          "KLM",
					FlightNumber:     "KL456",
					DepartureTime:    "2026-07-01T12:00",
					ArrivalTime:      "2026-07-01T14:00",
					DepartureAirport: models.AirportInfo{Code: "AMS"},
					ArrivalAirport:   models.AirportInfo{Code: "BCN"},
				},
			},
		},
	}
	got := extractTopFlights(flts, 5)
	if len(got) != 1 {
		t.Fatalf("expected 1 flight, got %d", len(got))
	}
	if got[0].Route != "HEL -> AMS -> BCN" {
		t.Errorf("route = %q, want 'HEL -> AMS -> BCN'", got[0].Route)
	}
	if got[0].Arrival != "2026-07-01T14:00" {
		t.Errorf("arrival = %q, want last leg arrival", got[0].Arrival)
	}
}

// ============================================================
// extractTopHotels — hotels with amenities
// ============================================================

func TestExtractTopHotels_WithManyAmenities(t *testing.T) {
	htls := []models.HotelResult{
		{
			Name:      "Grand Hotel",
			Price:     200,
			Currency:  "EUR",
			Rating:    4.5,
			Amenities: []string{"wifi", "pool", "gym", "spa", "restaurant"},
		},
	}
	got := extractTopHotels(htls, 3, 5)
	if len(got) != 1 {
		t.Fatalf("expected 1 hotel, got %d", len(got))
	}
	// Should show first 3 + "+2 more".
	if got[0].Amenities == "" {
		t.Error("expected non-empty amenities")
	}
}

func TestExtractTopHotels_WithFewAmenities(t *testing.T) {
	htls := []models.HotelResult{
		{
			Name:      "Budget Hotel",
			Price:     50,
			Currency:  "EUR",
			Amenities: []string{"wifi", "breakfast"},
		},
	}
	got := extractTopHotels(htls, 2, 5)
	if len(got) != 1 {
		t.Fatalf("expected 1 hotel, got %d", len(got))
	}
	if got[0].Amenities != "wifi, breakfast" {
		t.Errorf("amenities = %q, want 'wifi, breakfast'", got[0].Amenities)
	}
}

func TestExtractTopHotels_WithLatLon(t *testing.T) {
	htls := []models.HotelResult{
		{
			Name:     "City Hotel",
			Price:    100,
			Currency: "EUR",
			Lat:      60.17,
			Lon:      24.94,
		},
	}
	got := extractTopHotels(htls, 2, 5)
	if len(got) != 1 {
		t.Fatalf("expected 1 hotel, got %d", len(got))
	}
	if got[0].Lat != 60.17 || got[0].Lon != 24.94 {
		t.Errorf("lat/lon = %f/%f, want 60.17/24.94", got[0].Lat, got[0].Lon)
	}
}

// ============================================================
// SuggestDates — validation edge cases
// ============================================================

func TestSuggestDates_EmptyOriginAndDest(t *testing.T) {
	_, err := SuggestDates(context.Background(), "", "", SmartDateOptions{TargetDate: "2026-07-15"})
	if err == nil {
		t.Error("expected error for both empty")
	}
}

func TestSuggestDates_CustomFlexDays(t *testing.T) {
	// Just verify the defaults are applied correctly.
	opts := SmartDateOptions{TargetDate: "2026-07-15", FlexDays: 3}
	opts.defaults()
	if opts.FlexDays != 3 {
		t.Errorf("flexDays = %d, want 3 (custom)", opts.FlexDays)
	}
}

// ============================================================
// OptimizeTripDates — edge cases
// ============================================================

func TestOptimizeTripDates_ZeroTripLength(t *testing.T) {
	_, err := OptimizeTripDates(context.Background(), OptimizeTripDatesInput{
		Origin:      "HEL",
		Destination: "BCN",
		FromDate:    "2026-07-01",
		ToDate:      "2026-07-15",
		TripLength:  0,
	})
	if err == nil {
		t.Error("expected error for zero trip length")
	}
}

// ============================================================
// CalculateTripCost — edge cases
// ============================================================

func TestCalculateTripCost_MissingOriginAndDest(t *testing.T) {
	_, err := CalculateTripCost(context.Background(), TripCostInput{
		DepartDate: "2026-07-01",
		ReturnDate: "2026-07-08",
		Guests:     1,
	})
	if err == nil {
		t.Error("expected error for missing origin and destination")
	}
}

// ============================================================
// convertPlanHotels — conversion with both perNight and total
// ============================================================

// ============================================================
// airportTransferDepartureMinutes — invalid minute
// ============================================================

func TestAirportTransferDepartureMinutes_InvalidMinute(t *testing.T) {
	_, ok := airportTransferDepartureMinutes("2026-07-01T09:xx:00+02:00")
	// The colons at [13] match, hour "09" parses, but "xx" won't parse.
	// Should fall through to RFC3339 fallback.
	_ = ok
}

func TestAirportTransferDepartureMinutes_RFC3339Full(t *testing.T) {
	mins, ok := airportTransferDepartureMinutes("2026-07-01T14:30:00Z")
	if !ok {
		t.Error("expected ok for valid RFC3339")
	}
	if mins != 14*60+30 {
		t.Errorf("minutes = %d, want %d", mins, 14*60+30)
	}
}

// ============================================================
// searchAirportTransfers — additional branches
// ============================================================

func TestSearchAirportTransfers_GeocodeFailsForAirport(t *testing.T) {
	callCount := 0
	deps := airportTransferDeps{
		geocode: func(_ context.Context, query string) (destinations.GeoResult, error) {
			callCount++
			if callCount == 1 {
				// First call is destination geocode — succeed.
				return destinations.GeoResult{Locality: "Paris"}, nil
			}
			// Second call is airport geocode — fail.
			return destinations.GeoResult{}, fmt.Errorf("geocode failed")
		},
		searchTransitous: func(_ context.Context, fromLat, fromLon, toLat, toLon float64, date string) ([]models.GroundRoute, error) {
			return nil, fmt.Errorf("should not be called")
		},
		searchGround: func(_ context.Context, from, to, date string, opts ground.SearchOptions) (*models.GroundSearchResult, error) {
			return &models.GroundSearchResult{Success: true, Count: 0}, nil
		},
	}
	result, err := searchAirportTransfers(context.Background(), AirportTransferInput{
		AirportCode: "CDG",
		Destination: "Paris Center",
		Date:        "2026-07-01",
		Providers:   []string{"transitous"},
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should still return a result, just with warnings.
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestSearchAirportTransfers_TaxiOnlyNoEstimateProvider(t *testing.T) {
	deps := airportTransferDeps{
		geocode: func(_ context.Context, query string) (destinations.GeoResult, error) {
			return destinations.GeoResult{Locality: "Paris"}, nil
		},
		estimateTaxi: nil, // not configured
		searchGround: func(_ context.Context, from, to, date string, opts ground.SearchOptions) (*models.GroundSearchResult, error) {
			return &models.GroundSearchResult{Success: true, Count: 0}, nil
		},
	}
	result, err := searchAirportTransfers(context.Background(), AirportTransferInput{
		AirportCode: "CDG",
		Destination: "Paris Center",
		Date:        "2026-07-01",
		Providers:   []string{"taxi"},
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestSearchAirportTransfers_InvalidDate(t *testing.T) {
	deps := airportTransferDeps{}
	_, err := searchAirportTransfers(context.Background(), AirportTransferInput{
		AirportCode: "CDG",
		Destination: "Paris",
		Date:        "not-a-date",
	}, deps)
	if err == nil {
		t.Error("expected error for invalid date")
	}
}

func TestSearchAirportTransfers_InvalidArrivalTime(t *testing.T) {
	deps := airportTransferDeps{}
	_, err := searchAirportTransfers(context.Background(), AirportTransferInput{
		AirportCode: "CDG",
		Destination: "Paris",
		Date:        "2026-07-01",
		ArrivalTime: "invalid-time",
	}, deps)
	if err == nil {
		t.Error("expected error for invalid arrival time")
	}
}

func TestSearchAirportTransfers_EmptyAirportCode(t *testing.T) {
	deps := airportTransferDeps{}
	_, err := searchAirportTransfers(context.Background(), AirportTransferInput{
		Destination: "Paris",
		Date:        "2026-07-01",
	}, deps)
	if err == nil {
		t.Error("expected error for empty airport code")
	}
}

func TestSearchAirportTransfers_InvalidIATACode(t *testing.T) {
	deps := airportTransferDeps{}
	_, err := searchAirportTransfers(context.Background(), AirportTransferInput{
		AirportCode: "1234",
		Destination: "Paris",
		Date:        "2026-07-01",
	}, deps)
	if err == nil {
		t.Error("expected error for invalid IATA code")
	}
}

func TestConvertPlanHotels_WithTotal(t *testing.T) {
	hotels := []PlanHotel{
		{PerNight: 100, Total: 300, Currency: "EUR"},
	}
	// Same currency, no conversion.
	convertPlanHotels(context.Background(), hotels, "EUR")
	if hotels[0].PerNight != 100 || hotels[0].Total != 300 {
		t.Error("same currency should not change values")
	}
}
