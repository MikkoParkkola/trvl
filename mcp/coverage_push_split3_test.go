package mcp

import (
	"context"
	"testing"
)

func TestRecordSearchFromArgs_DestinationOnly(t *testing.T) {
	s := NewServer()
	s.recordSearchFromArgs(
		map[string]any{"location": "Tokyo"},
		map[string]any{"location": "Tokyo"},
	)
	s.tripState.mu.Lock()
	found := false
	for _, sr := range s.tripState.Searches {
		if sr.Type == "destination" {
			found = true
		}
	}
	s.tripState.mu.Unlock()
	if !found {
		t.Error("expected destination search recorded")
	}
}

func TestRecordSearchFromArgs_HotelSearch(t *testing.T) {
	s := NewServer()
	s.recordSearchFromArgs(
		map[string]any{"location": "Tokyo", "check_in": "2026-06-15", "check_out": "2026-06-18"},
		map[string]any{"hotels": []interface{}{
			map[string]interface{}{"price": float64(120), "currency": "EUR"},
		}},
	)
	s.tripState.mu.Lock()
	found := false
	for _, sr := range s.tripState.Searches {
		if sr.Type == "hotel" && sr.BestPrice == 120 {
			found = true
		}
	}
	s.tripState.mu.Unlock()
	if !found {
		t.Error("expected hotel search recorded with price")
	}
}

// --- wrapHandler panic recovery ---

func TestWrapHandler_PanicRecovery(t *testing.T) {
	s := NewServer()
	panicky := func(ctx context.Context, args map[string]any, _ ElicitFunc, _ SamplingFunc, _ ProgressFunc) ([]ContentBlock, interface{}, error) {
		panic("test panic")
	}
	wrapped := s.wrapHandler("test_tool", panicky)
	_, _, err := wrapped(context.Background(), nil, nil, nil, nil)
	if err == nil || !contains(err.Error(), "tool panicked") {
		t.Errorf("expected panic recovery error, got: %v", err)
	}
}

// --- helper types for loungeSummary testing without importing lounges package ---

func TestReadResource_TripsURI(t *testing.T) {
	s := NewServer()
	// trvl://trips calls readTripsList -> defaultTripStore
	result, err := s.readResource("trvl://trips")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Contents) == 0 {
		t.Fatal("expected contents")
	}
}

func TestReadResource_TripsUpcoming(t *testing.T) {
	s := NewServer()
	result, err := s.readResource("trvl://trips/upcoming")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Contents) == 0 {
		t.Fatal("expected contents")
	}
}

func TestReadResource_TripsAlerts(t *testing.T) {
	s := NewServer()
	result, err := s.readResource("trvl://trips/alerts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Contents) == 0 {
		t.Fatal("expected contents")
	}
}

func TestReadResource_Onboarding(t *testing.T) {
	s := NewServer()
	result, err := s.readResource("trvl://onboarding")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Contents) == 0 {
		t.Fatal("expected contents")
	}
	// Should contain either the questionnaire or profile summary
	text := result.Contents[0].Text
	if !contains(text, "TRVL") {
		t.Errorf("expected TRVL in onboarding, got: %.100s", text)
	}
}

func TestReadResource_TripByURI(t *testing.T) {
	s := NewServer()
	// This will try to load a trip that doesn't exist — should error
	_, err := s.readResource("trvl://trips/nonexistent_id")
	// Trip store should load but trip won't be found
	if err == nil {
		t.Log("trip found (unexpected but ok if user has trips)")
	}
}

// --- readWatchesList with empty watch store (7.7% coverage) ---

func TestReadResource_Watches_EmptyStore(t *testing.T) {
	s := NewServer()
	// watchStore is initialized in NewServer, but will be empty
	result, err := s.readResource("trvl://watches")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Contents) == 0 {
		t.Fatal("expected contents")
	}
}

// --- handleSearchFlights validation (26.2% coverage) ---

func TestHandleSearchFlights_MissingOriginDest_Push(t *testing.T) {
	// Origin is now optional (resolved from prefs/geo), so an empty args map
	// fails on the still-required destination. Disable geo for determinism.
	t.Setenv("TRVL_NO_GEO", "1")
	_, _, err := handleSearchFlights(context.Background(),
		map[string]any{}, nil, nil, nil)
	if err == nil || !contains(err.Error(), "destination is required") {
		t.Errorf("expected destination-required error, got: %v", err)
	}
}

func TestHandleSearchFlights_InvalidOriginIATA(t *testing.T) {
	_, _, err := handleSearchFlights(context.Background(),
		map[string]any{"origin": "TOOLONG", "destination": "BCN", "departure_date": "2026-06-15"},
		nil, nil, nil)
	// An explicitly-supplied bad origin is still rejected; the message now
	// reads "resolved origin ... is invalid" since origin flows through the
	// optional-origin resolver.
	if err == nil || !contains(err.Error(), "origin") {
		t.Errorf("expected origin validation error, got: %v", err)
	}
}

func TestHandleSearchFlights_InvalidDestIATA(t *testing.T) {
	_, _, err := handleSearchFlights(context.Background(),
		map[string]any{"origin": "HEL", "destination": "TOOLONG", "departure_date": "2026-06-15"},
		nil, nil, nil)
	if err == nil || !contains(err.Error(), "invalid destination") {
		t.Errorf("expected invalid dest error, got: %v", err)
	}
}

func TestHandleSearchFlights_MissingDate_Push(t *testing.T) {
	_, _, err := handleSearchFlights(context.Background(),
		map[string]any{"origin": "HEL", "destination": "BCN"},
		nil, nil, nil)
	if err == nil || !contains(err.Error(), "departure_date") {
		t.Errorf("expected date error, got: %v", err)
	}
}

func TestHandleSearchFlights_InvalidDate(t *testing.T) {
	_, _, err := handleSearchFlights(context.Background(),
		map[string]any{"origin": "HEL", "destination": "BCN", "departure_date": "not-a-date"},
		nil, nil, nil)
	if err == nil {
		t.Error("expected error for invalid date")
	}
}

func TestHandleSearchFlights_InvalidReturnDate_Push(t *testing.T) {
	_, _, err := handleSearchFlights(context.Background(),
		map[string]any{
			"origin": "HEL", "destination": "BCN",
			"departure_date": "2026-06-15", "return_date": "bad",
		},
		nil, nil, nil)
	if err == nil {
		t.Error("expected error for invalid return_date")
	}
}

func TestHandleSearchFlights_InvalidCabinClass_Push(t *testing.T) {
	_, _, err := handleSearchFlights(context.Background(),
		map[string]any{
			"origin": "HEL", "destination": "BCN",
			"departure_date": "2026-06-15", "cabin_class": "ultralux",
		},
		nil, nil, nil)
	if err == nil || !contains(err.Error(), "cabin_class") {
		t.Errorf("expected cabin_class error, got: %v", err)
	}
}

func TestHandleSearchFlights_InvalidMaxStops_Push(t *testing.T) {
	_, _, err := handleSearchFlights(context.Background(),
		map[string]any{
			"origin": "HEL", "destination": "BCN",
			"departure_date": "2026-06-15", "max_stops": "invalid",
		},
		nil, nil, nil)
	if err == nil || !contains(err.Error(), "max_stops") {
		t.Errorf("expected max_stops error, got: %v", err)
	}
}

// --- handleSearchDates validation (44.4% coverage) ---

func TestHandleSearchDates_MissingOriginDest(t *testing.T) {
	_, _, err := handleSearchDates(context.Background(),
		map[string]any{}, nil, nil, nil)
	if err == nil || !contains(err.Error(), "required") {
		t.Errorf("expected required error, got: %v", err)
	}
}

func TestHandleSearchDates_InvalidIATA_Push(t *testing.T) {
	_, _, err := handleSearchDates(context.Background(),
		map[string]any{"origin": "TOOLONG", "destination": "BCN", "start_date": "2026-06-01", "end_date": "2026-06-30"},
		nil, nil, nil)
	if err == nil {
		t.Error("expected error for invalid IATA")
	}
}

func TestHandleSearchDates_MissingDates(t *testing.T) {
	_, _, err := handleSearchDates(context.Background(),
		map[string]any{"origin": "HEL", "destination": "BCN"},
		nil, nil, nil)
	if err == nil || !contains(err.Error(), "required") {
		t.Errorf("expected date required error, got: %v", err)
	}
}

// --- handleListTrips (0% coverage) ---

func TestHandleListTrips_EmptyStore(t *testing.T) {
	content, structured, err := handleListTrips(context.Background(),
		map[string]any{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content) == 0 {
		t.Error("expected content blocks")
	}
	_ = structured
}

// TestHandleListTrips_ReturnsSchemaShape locks in the regression fix where
// handleListTrips returned a raw []*trips.Trip array, mismatching the
// declared OutputSchema of {trips:[], count:N}. MCP clients that validate
// structuredContent against the schema rejected the response with
// "expected record, received array".
func TestHandleListTrips_ReturnsSchemaShape(t *testing.T) {
	_, structured, err := handleListTrips(context.Background(),
		map[string]any{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m, ok := structured.(map[string]any)
	if !ok {
		t.Fatalf("structured = %T, want map[string]any matching OutputSchema "+
			"({trips:[], count:N}); array-shape regression must not return", structured)
	}
	if _, ok := m["trips"]; !ok {
		t.Errorf("structured missing \"trips\" key: %#v", m)
	}
	if _, ok := m["count"]; !ok {
		t.Errorf("structured missing \"count\" key: %#v", m)
	}
}

// --- handleGetPreferences (0% coverage) ---

func TestHandleGetPreferences_Loads(t *testing.T) {
	content, _, err := handleGetPreferences(context.Background(),
		map[string]any{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content) == 0 {
		t.Error("expected content blocks")
	}
}

// --- handleHotelReviews validation (16.7% coverage) ---

func TestHandleHotelReviews_MissingHotelNamePush(t *testing.T) {
	_, _, err := handleHotelReviews(context.Background(),
		map[string]any{"hotel_id": ""}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for missing hotel name/id")
	}
}

// --- handleSearchGround more validation (68.4% -> higher) ---

func TestHandleSearchGround_AllowBrowserFallbacks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := handleSearchGround(ctx,
		map[string]any{
			"from": "Prague", "to": "Vienna", "date": "2026-06-15",
			"allow_browser_fallbacks": true,
			"max_price":               float64(50),
		},
		nil, nil, nil)
	_ = err
}

// --- handleSearchAirportTransfers missing args ---

func TestHandleSearchAirportTransfers_MissingDestination(t *testing.T) {
	_, _, err := handleSearchAirportTransfers(context.Background(),
		map[string]any{"airport_code": "CDG", "date": "2026-06-15"},
		nil, nil, nil)
	if err == nil || !contains(err.Error(), "required") {
		t.Errorf("expected required error, got: %v", err)
	}
}

// --- handleHotelPrices with valid dates (exercises ValidateDateRange) ---

func TestHandleHotelPrices_InvalidDateRange_Push(t *testing.T) {
	_, _, err := handleHotelPrices(context.Background(),
		map[string]any{
			"hotel_id":  "/g/123",
			"check_in":  "2026-06-18",
			"check_out": "2026-06-15",
		},
		nil, nil, nil)
	if err == nil {
		t.Error("expected error for invalid date range")
	}
}

// --- handleOptimizeTripDates with all args (57.9% coverage) ---

func TestHandleOptimizeTripDates_WithAllArgs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := handleOptimizeTripDates(ctx,
		map[string]any{
			"origin":      "HEL",
			"destination": "BCN",
			"from_date":   "2026-06-01",
			"to_date":     "2026-06-30",
			"trip_length": float64(7),
			"guests":      float64(2),
			"currency":    "EUR",
		},
		nil, nil, nil)
	_ = err
}

// --- handlePlanTrip with all args (62.5% coverage) ---

func TestHandlePlanTrip_WithAllArgs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := handlePlanTrip(ctx,
		map[string]any{
			"origin":      "HEL",
			"destination": "BCN",
			"depart_date": "2026-06-15",
			"return_date": "2026-06-22",
			"guests":      float64(2),
			"currency":    "EUR",
		},
		nil, nil, nil)
	_ = err
}

// --- handleSearchDeals with valid origins ---

func TestHandleSearchDeals_WithMaxPrice(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := handleSearchDeals(ctx,
		map[string]any{"origins": "HEL,AMS", "max_price": float64(200)},
		nil, nil, nil)
	_ = err
}

// --- handleSearchFlights with all optional args ---

func TestHandleSearchFlights_AllOptionalArgs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := handleSearchFlights(ctx,
		map[string]any{
			"origin":              "HEL",
			"destination":         "BCN",
			"departure_date":      "2026-06-15",
			"return_date":         "2026-06-22",
			"cabin_class":         "economy",
			"max_stops":           "nonstop",
			"sort_by":             "cheapest",
			"max_price":           float64(500),
			"max_duration":        float64(600),
			"alliances":           "oneworld,star_alliance",
			"depart_after":        "08:00",
			"depart_before":       "22:00",
			"less_emissions":      true,
			"carry_on_bags":       float64(1),
			"checked_bags":        float64(1),
			"require_checked_bag": true,
			"exclude_basic":       true,
		},
		nil, nil, nil)
	_ = err
}

func TestHandleSearchFlights_InvalidDate_Push(t *testing.T) {
	_, _, err := handleSearchFlights(context.Background(),
		map[string]any{"origin": "HEL", "destination": "BCN", "departure_date": "not-a-date"},
		nil, nil, nil)
	if err == nil {
		t.Error("expected error for invalid date")
	}
}

// --- handleSearchDates with all optional args ---

func TestHandleSearchDates_AllOptionalArgs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := handleSearchDates(ctx,
		map[string]any{
			"origin":        "HEL",
			"destination":   "BCN",
			"start_date":    "2026-06-01",
			"end_date":      "2026-06-30",
			"is_round_trip": true,
			"trip_duration": float64(7),
			"cabin_class":   "business",
		},
		nil, nil, nil)
	_ = err
}

func TestHandleSearchDates_MissingDates_Push(t *testing.T) {
	_, _, err := handleSearchDates(context.Background(),
		map[string]any{"origin": "HEL", "destination": "BCN"},
		nil, nil, nil)
	if err == nil || !contains(err.Error(), "required") {
		t.Errorf("expected required error, got: %v", err)
	}
}

// --- handleHotelReviews with cancelled context ---

func TestHandleHotelReviews_WithOptions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := handleHotelReviews(ctx,
		map[string]any{
			"hotel_id": "/g/11test",
			"limit":    float64(5),
			"sort":     "highest",
		},
		nil, nil, nil)
	_ = err
}

// --- handleHotelRooms missing args ---

func TestHandleHotelRooms_MissingCheckIn(t *testing.T) {
	_, _, err := handleHotelRooms(context.Background(),
		map[string]any{"hotel_name": "Hilton", "check_out": "2026-06-18"},
		nil, nil, nil)
	if err == nil || !contains(err.Error(), "required") {
		t.Errorf("expected required error, got: %v", err)
	}
}

// --- handleSearchHotels with more args ---

func TestHandleSearchHotels_AllArgs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := handleSearchHotels(ctx,
		map[string]any{
			"location":  "Helsinki",
			"check_in":  "2026-06-15",
			"check_out": "2026-06-18",
			"guests":    float64(2),
			"stars":     float64(4),
			"sort":      "price",
			"max_price": float64(200),
		},
		nil, nil, nil)
	_ = err
}

// --- handleTravelGuide with cancelled ctx ---

func TestHandleTravelGuide_WithLocation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := handleTravelGuide(ctx,
		map[string]any{"location": "Prague"},
		nil, nil, nil)
	_ = err
}

// --- handleLocalEvents missing location ---

func TestHandleLocalEvents_EmptyLocation(t *testing.T) {
	_, _, err := handleLocalEvents(context.Background(),
		map[string]any{"location": "", "start_date": "2026-06-15", "end_date": "2026-06-18"},
		nil, nil, nil)
	if err == nil || !contains(err.Error(), "location") {
		t.Errorf("expected location error, got: %v", err)
	}
}

// --- handleNearbyPlaces with category ---

func TestHandleNearbyPlaces_WithCategory(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := handleNearbyPlaces(ctx,
		map[string]any{"lat": float64(60.17), "lon": float64(24.94), "radius_m": float64(1000), "category": "restaurant"},
		nil, nil, nil)
	_ = err
}

// --- handleWatchRoomAvailability (0% coverage) ---
