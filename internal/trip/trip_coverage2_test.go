package trip

import (
	"context"
	"fmt"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/visa"
	"github.com/MikkoParkkola/trvl/internal/weather"
)

// ============================================================
// extractTopHotels — additional branch coverage
// ============================================================

func TestExtractTopHotels_MoreThanThreeAmenities(t *testing.T) {
	hotels := []models.HotelResult{
		{Name: "H", Price: 100, Currency: "EUR", Amenities: []string{"wifi", "pool", "gym", "spa", "parking"}},
	}
	got := extractTopHotels(hotels, 2, 5)
	if len(got) != 1 {
		t.Fatalf("expected 1 hotel, got %d", len(got))
	}
	if got[0].Amenities != "wifi, pool, gym +2 more" {
		t.Errorf("amenities = %q, want 'wifi, pool, gym +2 more'", got[0].Amenities)
	}
}

func TestExtractTopHotels_SortsByPrice(t *testing.T) {
	hotels := []models.HotelResult{
		{Name: "Expensive", Price: 300, Currency: "EUR"},
		{Name: "Cheap", Price: 50, Currency: "EUR"},
		{Name: "Mid", Price: 150, Currency: "EUR"},
	}
	got := extractTopHotels(hotels, 2, 2)
	if len(got) != 2 {
		t.Fatalf("expected 2 hotels, got %d", len(got))
	}
	if got[0].Name != "Cheap" {
		t.Errorf("first hotel = %q, want Cheap (cheapest)", got[0].Name)
	}
	if got[1].Name != "Mid" {
		t.Errorf("second hotel = %q, want Mid", got[1].Name)
	}
}

func TestExtractTopHotels_WithHotelID(t *testing.T) {
	hotels := []models.HotelResult{
		{Name: "H", Price: 100, Currency: "EUR", HotelID: "abc123", Rating: 4.5, ReviewCount: 200, Lat: 48.85, Lon: 2.35},
	}
	got := extractTopHotels(hotels, 2, 5)
	if len(got) != 1 {
		t.Fatalf("expected 1 hotel, got %d", len(got))
	}
	if got[0].HotelID != "abc123" {
		t.Errorf("hotel_id = %q, want abc123", got[0].HotelID)
	}
	if got[0].Rating != 4.5 {
		t.Errorf("rating = %v, want 4.5", got[0].Rating)
	}
	if got[0].Reviews != 200 {
		t.Errorf("reviews = %d, want 200", got[0].Reviews)
	}
	if got[0].Total != 200 {
		t.Errorf("total = %v, want 200 (100*2 nights)", got[0].Total)
	}
}

// ============================================================
// extractTopFlights — price sort and route building
// ============================================================

func TestExtractTopFlights_MultiLegRoute(t *testing.T) {
	flights := []models.FlightResult{
		{
			Price:    250,
			Currency: "EUR",
			Stops:    1,
			Duration: 300,
			Legs: []models.FlightLeg{
				{
					Airline:          "LH",
					FlightNumber:     "LH100",
					DepartureTime:    "08:00",
					ArrivalTime:      "10:00",
					DepartureAirport: models.AirportInfo{Code: "HEL"},
					ArrivalAirport:   models.AirportInfo{Code: "FRA"},
				},
				{
					Airline:          "LH",
					FlightNumber:     "LH200",
					DepartureTime:    "11:00",
					ArrivalTime:      "13:00",
					DepartureAirport: models.AirportInfo{Code: "FRA"},
					ArrivalAirport:   models.AirportInfo{Code: "BCN"},
				},
			},
		},
	}
	got := extractTopFlights(flights, 5)
	if len(got) != 1 {
		t.Fatalf("expected 1 flight, got %d", len(got))
	}
	if got[0].Route != "HEL -> FRA -> BCN" {
		t.Errorf("route = %q, want 'HEL -> FRA -> BCN'", got[0].Route)
	}
	if got[0].Airline != "LH" {
		t.Errorf("airline = %q, want LH", got[0].Airline)
	}
	if got[0].Stops != 1 {
		t.Errorf("stops = %d, want 1", got[0].Stops)
	}
	if got[0].Duration != 300 {
		t.Errorf("duration = %d, want 300", got[0].Duration)
	}
}

// ============================================================
// buildViabilityChecks — return-only flight warning
// ============================================================

func TestBuildViabilityChecks_ReturnOnlyWarning(t *testing.T) {
	cost := &TripCostResult{
		Success: true,
		Flights: FlightCost{
			Outbound: 0,
			Return:   120,
			Currency: "EUR",
		},
		Hotels: HotelCost{
			PerNight: 80,
			Total:    560,
			Currency: "EUR",
			Name:     "Test Hotel",
		},
		Total: 680,
	}
	checks, _, hasWarning := buildViabilityChecks(cost, nil, visa.Result{}, "", nil, nil)
	if !hasWarning {
		t.Error("expected warning when only return flights found")
	}
	found := false
	for _, c := range checks {
		if c.Dimension == "flights" && c.Status == "warning" {
			found = true
		}
	}
	if !found {
		t.Error("expected flight check with warning status")
	}
}

func TestBuildViabilityChecks_CostNilResult(t *testing.T) {
	checks, _, hasWarning := buildViabilityChecks(nil, nil, visa.Result{}, "", nil, nil)
	if !hasWarning {
		t.Error("expected warning when cost result is nil")
	}
	if len(checks) == 0 {
		t.Error("expected at least one check")
	}
	if checks[0].Dimension != "flights" {
		t.Errorf("first check dimension = %q, want flights", checks[0].Dimension)
	}
}

func TestBuildViabilityChecks_CostError(t *testing.T) {
	checks, _, hasWarning := buildViabilityChecks(nil, fmt.Errorf("network timeout"), visa.Result{}, "", nil, nil)
	if !hasWarning {
		t.Error("expected warning when cost error present")
	}
	if checks[0].Summary != "network timeout" {
		t.Errorf("summary = %q, want 'network timeout'", checks[0].Summary)
	}
}

func TestBuildViabilityChecks_WithWeatherAndTotalCost(t *testing.T) {
	cost := &TripCostResult{
		Success:   true,
		Flights:   FlightCost{Outbound: 200, Return: 200, Currency: "EUR"},
		Hotels:    HotelCost{PerNight: 80, Total: 560, Currency: "EUR", Name: "Test"},
		Total:     960,
		Currency:  "EUR",
		PerPerson: 480,
		PerDay:    137,
	}
	weatherRes := &weather.WeatherResult{
		Success: true,
		Forecasts: []weather.Forecast{
			{TempMax: 30, TempMin: 20, Precipitation: 0},
			{TempMax: 28, TempMin: 19, Precipitation: 0},
		},
	}
	checks, _, _ := buildViabilityChecks(cost, nil, visa.Result{}, "", weatherRes, nil)

	hasWeather := false
	hasTotalCost := false
	for _, c := range checks {
		if c.Dimension == "weather" {
			hasWeather = true
			if c.Status != "ok" {
				t.Errorf("weather status = %q, want ok (no rain)", c.Status)
			}
		}
		if c.Dimension == "total_cost" {
			hasTotalCost = true
			if c.Cost != 960 {
				t.Errorf("total cost = %v, want 960", c.Cost)
			}
		}
	}
	if !hasWeather {
		t.Error("expected weather check")
	}
	if !hasTotalCost {
		t.Error("expected total_cost check")
	}
}

// ============================================================
// buildVisaCheck — additional edge cases
// ============================================================

func TestBuildVisaCheck_UnknownStatus(t *testing.T) {
	check := buildVisaCheck(visa.Result{
		Success: true,
		Requirement: visa.Requirement{
			Status: "unknown-status",
		},
	})
	if check.Status != "warning" {
		t.Errorf("status = %q, want warning for unknown status", check.Status)
	}
	if check.Summary != "unknown-status" {
		t.Errorf("summary = %q, want 'unknown-status'", check.Summary)
	}
}

func TestBuildVisaCheck_VisaOnArrivalWithNotes(t *testing.T) {
	check := buildVisaCheck(visa.Result{
		Success: true,
		Requirement: visa.Requirement{
			Status: "visa-on-arrival",
			Notes:  "Fee required",
		},
	})
	if check.Status != "warning" {
		t.Errorf("status = %q, want warning", check.Status)
	}
	if check.Summary != "visa-on-arrival — apply before travel. Fee required" {
		t.Errorf("summary = %q", check.Summary)
	}
}

func TestBuildVisaCheck_VisaRequiredWithNotes(t *testing.T) {
	check := buildVisaCheck(visa.Result{
		Success: true,
		Requirement: visa.Requirement{
			Status: "visa-required",
			Notes:  "Embassy appointment needed",
		},
	})
	if check.Status != "blocker" {
		t.Errorf("status = %q, want blocker", check.Status)
	}
	if check.Summary != "visa required — check processing times before booking. Embassy appointment needed" {
		t.Errorf("summary = %q", check.Summary)
	}
}

// ============================================================
// buildWeatherCheck — edge cases
// ============================================================

func TestBuildWeatherCheck_ExactlyHalfRain(t *testing.T) {
	forecasts := []weather.Forecast{
		{TempMax: 20, TempMin: 10, Precipitation: 10},
		{TempMax: 22, TempMin: 12, Precipitation: 0},
		{TempMax: 21, TempMin: 11, Precipitation: 10},
		{TempMax: 23, TempMin: 13, Precipitation: 0},
	}
	check := buildWeatherCheck(forecasts)
	// 2 rain days of 4 = exactly half, which is NOT > half, so status should be ok.
	if check.Status != "ok" {
		t.Errorf("status = %q, want ok (exactly half rain is not 'mostly')", check.Status)
	}
}

func TestBuildWeatherCheck_AllRain(t *testing.T) {
	forecasts := []weather.Forecast{
		{TempMax: 15, TempMin: 8, Precipitation: 20},
		{TempMax: 14, TempMin: 7, Precipitation: 15},
		{TempMax: 13, TempMin: 6, Precipitation: 25},
	}
	check := buildWeatherCheck(forecasts)
	if check.Status != "warning" {
		t.Errorf("status = %q, want warning (all rain)", check.Status)
	}
}

// ============================================================
// resolveDestinationCountry — additional edge cases
// ============================================================

func TestResolveDestinationCountry_ThreeLetterUnknown(t *testing.T) {
	got := resolveDestinationCountry("ZZZ")
	if got != "" {
		t.Errorf("expected empty for unknown IATA code, got %q", got)
	}
}

func TestResolveDestinationCountry_KnownCodes(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"HEL", "FI"},
		{"LHR", "GB"},
		{"JFK", "US"},
		{"NRT", "JP"},
		{"SYD", "AU"},
		{"GRU", "BR"},
		{"DXB", "AE"},
	}
	for _, tt := range tests {
		got := resolveDestinationCountry(tt.input)
		if got != tt.want {
			t.Errorf("resolveDestinationCountry(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ============================================================
// AssessTrip — validation edge cases
// ============================================================

func TestAssessTrip_DefaultGuests2(t *testing.T) {
	// With guests=0, should default to 1 (not error).
	// This actually hits the network, so we just check validation passes.
	// The function sets guests=1 internally when <=0.
	// We already have TestAssessTrip_DefaultGuests, but let's test -1.
	_, err := AssessTrip(context.Background(), ViabilityInput{
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-01",
		ReturnDate:  "2026-07-08",
		Guests:      -1,
	})
	// Should not return a validation error (defaults to 1).
	if err != nil {
		t.Errorf("expected no validation error for negative guests (defaults to 1), got: %v", err)
	}
}

// ============================================================
// CalculateTripCost — additional validation edge cases
// ============================================================

func TestCalculateTripCost_BadDepartDate(t *testing.T) {
	_, err := CalculateTripCost(context.Background(), TripCostInput{
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "not-a-date",
		ReturnDate:  "2026-07-08",
		Guests:      1,
	})
	if err == nil {
		t.Fatal("expected error for invalid depart date")
	}
}

func TestCalculateTripCost_BadReturnDate(t *testing.T) {
	_, err := CalculateTripCost(context.Background(), TripCostInput{
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-01",
		ReturnDate:  "not-a-date",
		Guests:      1,
	})
	if err == nil {
		t.Fatal("expected error for invalid return date")
	}
}

func TestCalculateTripCost_ReturnBeforeDepart2(t *testing.T) {
	_, err := CalculateTripCost(context.Background(), TripCostInput{
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-08",
		ReturnDate:  "2026-07-01",
		Guests:      1,
	})
	if err == nil {
		t.Fatal("expected error when return before depart")
	}
}

func TestCalculateTripCost_SameDayTrip(t *testing.T) {
	_, err := CalculateTripCost(context.Background(), TripCostInput{
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-01",
		ReturnDate:  "2026-07-01",
		Guests:      1,
	})
	if err == nil {
		t.Fatal("expected error for same-day trip")
	}
}

func TestCalculateTripCost_NegativeGuestsVal(t *testing.T) {
	_, err := CalculateTripCost(context.Background(), TripCostInput{
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-01",
		ReturnDate:  "2026-07-08",
		Guests:      -1,
	})
	if err == nil {
		t.Fatal("expected error for negative guests")
	}
}

// ============================================================
// PlanTrip — additional validation edge cases
// ============================================================

func TestPlanTrip_SameDay2(t *testing.T) {
	_, err := PlanTrip(context.Background(), PlanInput{
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-01",
		ReturnDate:  "2026-07-01",
		Guests:      1,
	})
	if err == nil {
		t.Fatal("expected error for same-day trip")
	}
}

// ============================================================
// choosePlanSummaryCurrency — additional branches
// ============================================================

func TestChoosePlanSummaryCurrency_Requested(t *testing.T) {
	got := choosePlanSummaryCurrency("USD", &PlanResult{})
	if got != "USD" {
		t.Errorf("expected USD when explicitly requested, got %q", got)
	}
}

func TestChoosePlanSummaryCurrency_FromReturnFlights(t *testing.T) {
	got := choosePlanSummaryCurrency("", &PlanResult{
		OutboundFlights: []PlanFlight{{Currency: ""}},
		ReturnFlights:   []PlanFlight{{Currency: "GBP"}},
	})
	if got != "GBP" {
		t.Errorf("expected GBP from return flights, got %q", got)
	}
}

func TestChoosePlanSummaryCurrency_FromHotels(t *testing.T) {
	got := choosePlanSummaryCurrency("", &PlanResult{
		OutboundFlights: []PlanFlight{{Currency: ""}},
		ReturnFlights:   []PlanFlight{{Currency: ""}},
		Hotels:          []PlanHotel{{Currency: "SEK"}},
	})
	if got != "SEK" {
		t.Errorf("expected SEK from hotels, got %q", got)
	}
}

// ============================================================
// Discover — additional validation edge cases
// ============================================================

func TestDiscover_NegativeBudget2(t *testing.T) {
	_, err := Discover(context.Background(), DiscoverOptions{
		Origin: "HEL",
		From:   "2026-07-01",
		Until:  "2026-07-31",
		Budget: -100,
	})
	if err == nil {
		t.Error("expected error for negative budget")
	}
}

func TestDiscover_EmptyFromDate(t *testing.T) {
	_, err := Discover(context.Background(), DiscoverOptions{
		Origin: "HEL",
		Until:  "2026-07-31",
		Budget: 500,
	})
	if err == nil {
		t.Error("expected error for missing from date")
	}
}

func TestDiscover_EmptyUntilDate(t *testing.T) {
	_, err := Discover(context.Background(), DiscoverOptions{
		Origin: "HEL",
		From:   "2026-07-01",
		Budget: 500,
	})
	if err == nil {
		t.Error("expected error for missing until date")
	}
}

// ============================================================
// formatTripCostError — multiple errors
// ============================================================

func TestFormatTripCostError_MultiplePartial(t *testing.T) {
	got := formatTripCostError([]string{"flight fail", "hotel fail"}, true)
	if got != "partial failure: flight fail; hotel fail" {
		t.Errorf("got %q", got)
	}
}

func TestFormatTripCostError_MultipleFull(t *testing.T) {
	got := formatTripCostError([]string{"err1", "err2"}, false)
	if got != "err1; err2" {
		t.Errorf("got %q", got)
	}
}

// ============================================================
// FindWeekendGetaways — validation edge cases
// ============================================================

func TestFindWeekendGetaways_BadMonth(t *testing.T) {
	_, err := FindWeekendGetaways(context.Background(), "HEL", WeekendOptions{Month: "notamonth"})
	if err == nil {
		t.Error("expected error for invalid month")
	}
}

func TestFindWeekendGetaways_EmptyOrigin2(t *testing.T) {
	_, err := FindWeekendGetaways(context.Background(), "", WeekendOptions{Month: "july-2026"})
	if err == nil {
		t.Error("expected error for empty origin")
	}
}

// ============================================================
// OptimizeMultiCity — additional edge cases
// ============================================================

func TestOptimizeMultiCity_SingleCity2(t *testing.T) {
	_, err := OptimizeMultiCity(context.Background(), "HEL", []string{"BCN"}, MultiCityOptions{DepartDate: "2026-07-01"})
	// Single city is valid.
	if err != nil {
		t.Errorf("unexpected error for single city: %v", err)
	}
}

func TestOptimizeMultiCity_EmptyDate2(t *testing.T) {
	_, err := OptimizeMultiCity(context.Background(), "HEL", []string{"BCN", "CDG"}, MultiCityOptions{})
	if err == nil {
		t.Error("expected error for empty date")
	}
}

// ============================================================
// parseMonth — additional formats
// ============================================================

func TestParseMonth_JulyDash2026(t *testing.T) {
	dep, ret, display, err := parseMonth("July-2026")
	if err != nil {
		t.Fatalf("parseMonth('July-2026') error: %v", err)
	}
	if dep == "" || ret == "" {
		t.Error("expected non-empty dates")
	}
	if display != "July 2026" {
		t.Errorf("display = %q, want 'July 2026'", display)
	}
}

func TestParseMonth_LowercaseJulDash2026(t *testing.T) {
	_, _, display, err := parseMonth("jul-2026")
	if err != nil {
		t.Fatalf("parseMonth('jul-2026') error: %v", err)
	}
	if display != "July 2026" {
		t.Errorf("display = %q, want 'July 2026'", display)
	}
}

// ============================================================
// WeekendOptions.defaults
// ============================================================

func TestWeekendOptions_DefaultsZeroNights(t *testing.T) {
	opts := WeekendOptions{Nights: 0}
	opts.defaults()
	if opts.Nights != 2 {
		t.Errorf("nights = %d, want 2 (default)", opts.Nights)
	}
}

func TestWeekendOptions_DefaultsPositivePreserved(t *testing.T) {
	opts := WeekendOptions{Nights: 3}
	opts.defaults()
	if opts.Nights != 3 {
		t.Errorf("nights = %d, want 3 (preserved)", opts.Nights)
	}
}

// ============================================================
// joinRoute — edge cases
// ============================================================

func TestJoinRoute_Empty(t *testing.T) {
	got := joinRoute(nil)
	if got != "" {
		t.Errorf("joinRoute(nil) = %q, want empty", got)
	}
}

func TestJoinRoute_Single(t *testing.T) {
	got := joinRoute([]string{"HEL"})
	if got != "HEL" {
		t.Errorf("joinRoute([HEL]) = %q, want 'HEL'", got)
	}
}

// ============================================================
// joinAmenities — edge cases
// ============================================================

func TestJoinAmenities_Empty(t *testing.T) {
	got := joinAmenities(nil)
	if got != "" {
		t.Errorf("joinAmenities(nil) = %q, want empty", got)
	}
}

func TestJoinAmenities_Multiple(t *testing.T) {
	got := joinAmenities([]string{"wifi", "pool", "gym"})
	if got != "wifi, pool, gym" {
		t.Errorf("joinAmenities = %q, want 'wifi, pool, gym'", got)
	}
}

// ============================================================
// trimReview — word boundary cut
// ============================================================

func TestTrimReview_CutsAtWordBoundary(t *testing.T) {
	got := trimReview("This is a great hotel with excellent service", 20)
	if len(got) > 23 { // allows for "..." suffix
		t.Errorf("trimReview too long: len=%d, text=%q", len(got), got)
	}
	if got == "" {
		t.Error("trimReview should not be empty")
	}
}

// ============================================================
// trimGuideSection — within limit
// ============================================================

func TestTrimGuideSection_WithinLimit(t *testing.T) {
	input := "Short text."
	got := trimGuideSection(input, 100)
	if got != input {
		t.Errorf("trimGuideSection = %q, want %q (unchanged within limit)", got, input)
	}
}

// ============================================================
// firstSectionByKey — hit
// ============================================================

func TestFirstSectionByKey_Match(t *testing.T) {
	sections := map[string]string{
		"See":   "castle",
		"Do":    "hiking",
		"Sleep": "hostel",
	}
	got, ok := firstSectionByKey(sections, "Do", "See")
	if !ok {
		t.Fatal("expected match")
	}
	if got != "hiking" {
		t.Errorf("got %q, want 'hiking' (first match)", got)
	}
}

func TestFirstSectionByKey_SecondCandidate(t *testing.T) {
	sections := map[string]string{
		"Sleep": "hostel",
	}
	got, ok := firstSectionByKey(sections, "Do", "Sleep")
	if !ok {
		t.Fatal("expected match on second candidate")
	}
	if got != "hostel" {
		t.Errorf("got %q, want 'hostel'", got)
	}
}

// ============================================================
// applyTripCostCurrencyAndTotals — with conversion
// ============================================================

func TestApplyTripCostCurrencyAndTotals_WithCurrency(t *testing.T) {
	result := &TripCostResult{
		Nights: 3,
		Flights: FlightCost{
			Outbound: 200,
			Return:   180,
			Currency: "EUR",
		},
		Hotels: HotelCost{
			PerNight: 80,
			Total:    240,
			Currency: "EUR",
		},
	}
	applyTripCostCurrencyAndTotals(
		context.Background(), result, "EUR",
		3, 1,
		func(_ context.Context, amount float64, from, to string) (float64, string) {
			return amount, from
		},
	)
	if !result.Success {
		t.Error("expected success when total > 0")
	}
	// Total = (200 + 180) * 1 guests + 240 hotel = 620
	if result.Total != 620 {
		t.Errorf("total = %v, want 620", result.Total)
	}
	if result.PerPerson != 620 {
		t.Errorf("per_person = %v, want 620 (1 guest)", result.PerPerson)
	}
}

func TestApplyTripCostCurrencyAndTotals_MultipleGuests(t *testing.T) {
	result := &TripCostResult{
		Nights: 2,
		Flights: FlightCost{
			Outbound: 100,
			Return:   100,
			Currency: "EUR",
		},
		Hotels: HotelCost{
			PerNight: 80,
			Total:    160,
			Currency: "EUR",
		},
	}
	applyTripCostCurrencyAndTotals(
		context.Background(), result, "EUR",
		2, 2,
		func(_ context.Context, amount float64, from, to string) (float64, string) {
			return amount, from
		},
	)
	// Total = (100 + 100) * 2 guests + 160 hotel = 560
	if result.Total != 560 {
		t.Errorf("total = %v, want 560", result.Total)
	}
	if result.PerPerson != 280 {
		t.Errorf("per_person = %v, want 280 (560/2)", result.PerPerson)
	}
	if result.PerDay != 280 {
		t.Errorf("per_day = %v, want 280 (560/2 nights)", result.PerDay)
	}
}
