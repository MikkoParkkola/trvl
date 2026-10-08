package trip

import (
	"context"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// ============================================================
// PlanTrip — validation edge cases
// ============================================================

func TestPlanTrip_ZeroGuests(t *testing.T) {
	_, err := PlanTrip(t.Context(), PlanInput{
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-01",
		ReturnDate:  "2026-07-08",
		Guests:      0,
	})
	if err == nil {
		t.Error("expected error for zero guests")
	}
}

func TestPlanTrip_NegativeGuests(t *testing.T) {
	_, err := PlanTrip(t.Context(), PlanInput{
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-01",
		ReturnDate:  "2026-07-08",
		Guests:      -1,
	})
	if err == nil {
		t.Error("expected error for negative guests")
	}
}

// ============================================================
// CalculateTripCost — guests validation
// ============================================================

func TestCalculateTripCost_ZeroGuests(t *testing.T) {
	_, err := CalculateTripCost(t.Context(), TripCostInput{
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-01",
		ReturnDate:  "2026-07-08",
		Guests:      0,
	})
	if err == nil {
		t.Error("expected error for zero guests")
	}
}

// ============================================================
// extractTopFlights — edge cases
// ============================================================

func TestExtractTopFlights_Empty(t *testing.T) {
	got := extractTopFlights(nil, 5)
	if len(got) != 0 {
		t.Errorf("expected 0 for nil input, got %d", len(got))
	}
}

func TestExtractTopFlights_AllZeroPrice(t *testing.T) {
	flights := []models.FlightResult{
		{Price: 0, Currency: "EUR"},
		{Price: 0, Currency: "EUR"},
	}
	got := extractTopFlights(flights, 5)
	if len(got) != 0 {
		t.Errorf("expected 0 for all-zero-price flights, got %d", len(got))
	}
}

func TestExtractTopFlights_MoreThanN(t *testing.T) {
	flights := make([]models.FlightResult, 10)
	for i := range flights {
		flights[i] = models.FlightResult{
			Price:    float64(100 + i*10),
			Currency: "EUR",
			Legs: []models.FlightLeg{
				{
					Airline:          "TEST",
					FlightNumber:     "TS100",
					DepartureTime:    "08:00",
					ArrivalTime:      "10:00",
					DepartureAirport: models.AirportInfo{Code: "HEL"},
					ArrivalAirport:   models.AirportInfo{Code: "BCN"},
				},
			},
		}
	}
	got := extractTopFlights(flights, 3)
	if len(got) != 3 {
		t.Fatalf("expected 3 flights, got %d", len(got))
	}
	// Should be sorted by price.
	if got[0].Price != 100 {
		t.Errorf("first flight price = %v, want 100", got[0].Price)
	}
}

func TestExtractTopFlights_NoLegs(t *testing.T) {
	flights := []models.FlightResult{
		{Price: 150, Currency: "EUR", Legs: nil},
	}
	got := extractTopFlights(flights, 5)
	if len(got) != 1 {
		t.Fatalf("expected 1 flight, got %d", len(got))
	}
	if got[0].Airline != "" {
		t.Errorf("airline = %q, want empty for no legs", got[0].Airline)
	}
	if got[0].Route != "" {
		t.Errorf("route = %q, want empty for no legs", got[0].Route)
	}
}

// ============================================================
// extractTopHotels — edge cases
// ============================================================

func TestExtractTopHotels_ExactlyThreeAmenities(t *testing.T) {
	hotels := []models.HotelResult{
		{Name: "H", Price: 100, Currency: "EUR", Amenities: []string{"wifi", "pool", "gym"}},
	}
	got := extractTopHotels(hotels, 2, 5)
	if len(got) != 1 {
		t.Fatalf("expected 1 hotel, got %d", len(got))
	}
	if got[0].Amenities != "wifi, pool, gym" {
		t.Errorf("amenities = %q, want 'wifi, pool, gym'", got[0].Amenities)
	}
}

func TestExtractTopHotels_OneAmenity(t *testing.T) {
	hotels := []models.HotelResult{
		{Name: "H", Price: 100, Currency: "EUR", Amenities: []string{"wifi"}},
	}
	got := extractTopHotels(hotels, 2, 5)
	if len(got) != 1 {
		t.Fatalf("expected 1 hotel, got %d", len(got))
	}
	if got[0].Amenities != "wifi" {
		t.Errorf("amenities = %q, want 'wifi'", got[0].Amenities)
	}
}

func TestExtractTopHotels_NoAmenities(t *testing.T) {
	hotels := []models.HotelResult{
		{Name: "H", Price: 100, Currency: "EUR", Amenities: nil},
	}
	got := extractTopHotels(hotels, 2, 5)
	if len(got) != 1 {
		t.Fatalf("expected 1 hotel, got %d", len(got))
	}
	if got[0].Amenities != "" {
		t.Errorf("amenities = %q, want empty", got[0].Amenities)
	}
}

// ============================================================
// convertPlanFlights — edge cases
// ============================================================

func TestConvertPlanFlights_NoCurrency(t *testing.T) {
	flights := []PlanFlight{
		{Price: 100, Currency: ""},
	}
	convertPlanFlights(context.Background(), flights, "EUR", fakeFX)
	if flights[0].Price != 100 {
		t.Errorf("price = %v, want 100 (no conversion when currency empty)", flights[0].Price)
	}
}

func TestConvertPlanFlights_SameCurrency(t *testing.T) {
	flights := []PlanFlight{
		{Price: 100, Currency: "EUR"},
	}
	convertPlanFlights(context.Background(), flights, "EUR", fakeFX)
	if flights[0].Price != 100 {
		t.Errorf("price = %v, want 100 (no conversion for same currency)", flights[0].Price)
	}
}

func TestConvertPlanFlights_ZeroPrice(t *testing.T) {
	flights := []PlanFlight{
		{Price: 0, Currency: "EUR"},
	}
	convertPlanFlights(context.Background(), flights, "USD", fakeFX)
	if flights[0].Price != 0 {
		t.Errorf("price = %v, want 0 (no conversion for zero price)", flights[0].Price)
	}
}

// ============================================================
// convertPlanHotels — edge cases
// ============================================================

func TestConvertPlanHotels_NoCurrency(t *testing.T) {
	hotels := []PlanHotel{
		{PerNight: 50, Total: 100, Currency: ""},
	}
	convertPlanHotels(context.Background(), hotels, "EUR", fakeFX)
	if hotels[0].PerNight != 50 {
		t.Errorf("per_night = %v, want 50 (no conversion when currency empty)", hotels[0].PerNight)
	}
}

func TestConvertPlanHotels_SameCurrency(t *testing.T) {
	hotels := []PlanHotel{
		{PerNight: 50, Total: 100, Currency: "EUR"},
	}
	convertPlanHotels(context.Background(), hotels, "EUR", fakeFX)
	if hotels[0].PerNight != 50 {
		t.Errorf("per_night = %v, want 50 (no conversion for same currency)", hotels[0].PerNight)
	}
}

// ============================================================
// trimReview — additional edge cases
// ============================================================

func TestTrimReview_AllSpaces(t *testing.T) {
	got := trimReview("   ", 10)
	if got != "" {
		t.Errorf("trimReview all spaces = %q, want empty", got)
	}
}

func TestTrimReview_ExactBoundary(t *testing.T) {
	got := trimReview("12345", 5)
	if got != "12345" {
		t.Errorf("trimReview exact = %q, want '12345'", got)
	}
}

// ============================================================
// trimGuideSection — additional edge cases
// ============================================================

func TestTrimGuideSection_NoPeriodInRange(t *testing.T) {
	// No period found in the cut range; uses char limit.
	got := trimGuideSection("This is a very long guide section without any sentence end markers in the first part and continues further", 30)
	if len(got) > 31 {
		t.Errorf("trimGuideSection should cut near 30, got len=%d: %q", len(got), got)
	}
}

func TestTrimGuideSection_Empty(t *testing.T) {
	got := trimGuideSection("", 100)
	if got != "" {
		t.Errorf("trimGuideSection empty = %q, want empty", got)
	}
}

// ============================================================
// firstSectionByKey — edge cases
// ============================================================

func TestFirstSectionByKey_EmptySections(t *testing.T) {
	got, ok := firstSectionByKey(nil, "See")
	if ok {
		t.Errorf("expected false for nil sections, got %q", got)
	}
}

func TestFirstSectionByKey_EmptyCandidates(t *testing.T) {
	sections := map[string]string{"See": "castle"}
	_, ok := firstSectionByKey(sections)
	if ok {
		t.Error("expected false for no candidates")
	}
}

// ============================================================
// choosePlanSummaryCurrency — coverage for empty outbound
// ============================================================

func TestChoosePlanSummaryCurrency_EmptyFlightsEmptyHotels(t *testing.T) {
	got := choosePlanSummaryCurrency("", &PlanResult{
		OutboundFlights: []PlanFlight{{Currency: ""}},
		ReturnFlights:   []PlanFlight{{Currency: ""}},
		Hotels:          []PlanHotel{{Currency: ""}},
	})
	if got != "EUR" {
		t.Errorf("expected EUR fallback, got %q", got)
	}
}

// ============================================================
// Discover — validation paths
// ============================================================

func TestDiscover_MissingBudget(t *testing.T) {
	_, err := Discover(context.Background(), DiscoverOptions{
		Origin: "HEL",
		From:   "2026-07-01",
		Until:  "2026-07-31",
		Budget: 0,
	})
	if err == nil {
		t.Error("expected error for zero budget")
	}
}

func TestDiscover_MissingDates(t *testing.T) {
	_, err := Discover(context.Background(), DiscoverOptions{
		Origin: "HEL",
		Budget: 500,
	})
	if err == nil {
		t.Error("expected error for missing dates")
	}
}

func TestDiscover_MissingOrigin(t *testing.T) {
	_, err := Discover(context.Background(), DiscoverOptions{
		Budget: 500,
		From:   "2026-07-01",
		Until:  "2026-07-31",
	})
	if err == nil {
		t.Error("expected error for missing origin")
	}
}

// ============================================================
// formatTripCostError — edge cases
// ============================================================

func TestFormatTripCostError_NoErrors(t *testing.T) {
	got := formatTripCostError(nil, true)
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestFormatTripCostError_PartialTrue(t *testing.T) {
	got := formatTripCostError([]string{"hotel fail"}, true)
	if got != "partial failure: hotel fail" {
		t.Errorf("got %q, want 'partial failure: hotel fail'", got)
	}
}

func TestFormatTripCostError_PartialFalse(t *testing.T) {
	got := formatTripCostError([]string{"all fail"}, false)
	if got != "all fail" {
		t.Errorf("got %q, want 'all fail'", got)
	}
}

// ============================================================
// chooseTripCostSummaryCurrency — edge cases
// ============================================================

func TestChooseTripCostSummaryCurrency_AllEmpty(t *testing.T) {
	got := chooseTripCostSummaryCurrency("", "", "")
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestChooseTripCostSummaryCurrency_SecondCurrencyUsed(t *testing.T) {
	got := chooseTripCostSummaryCurrency("", "", "USD")
	if got != "USD" {
		t.Errorf("expected USD, got %q", got)
	}
}

// ============================================================
// joinRoute — edge cases
// ============================================================

func TestJoinRoute_LongChain(t *testing.T) {
	got := joinRoute([]string{"A", "B", "C", "D"})
	if got != "A -> B -> C -> D" {
		t.Errorf("joinRoute = %q, want 'A -> B -> C -> D'", got)
	}
}

// ============================================================
// joinAmenities — edge cases
// ============================================================

func TestJoinAmenities_SingleItem(t *testing.T) {
	got := joinAmenities([]string{"wifi"})
	if got != "wifi" {
		t.Errorf("joinAmenities = %q, want 'wifi'", got)
	}
}

// ============================================================
// applyTripCostCurrencyAndTotals — zero total path
// ============================================================

func TestApplyTripCostCurrencyAndTotals_ZeroTotal(t *testing.T) {
	result := &TripCostResult{Nights: 2}
	applyTripCostCurrencyAndTotals(
		context.Background(), result, "",
		2, 1,
		func(_ context.Context, amount float64, from, to string) (float64, string) {
			return amount, from
		},
	)
	if result.Success {
		t.Error("expected false when total is zero")
	}
	if result.PerPerson != 0 {
		t.Errorf("per_person = %v, want 0", result.PerPerson)
	}
}

// ============================================================
// convertedTripCostAmount
// ============================================================

func TestConvertedTripCostAmount_Rounding(t *testing.T) {
	got, cur := convertedTripCostAmount(
		context.Background(), 100.555, "EUR", "USD",
		func(_ context.Context, amount float64, from, to string) (float64, string) {
			return amount, to
		},
	)
	if got != 100.56 {
		t.Errorf("got %v, want 100.56", got)
	}
	if cur != "USD" {
		t.Errorf("currency = %q, want USD", cur)
	}
}

// ============================================================
// convertPlanHotels — conversion path (was 37%)
// ============================================================

func TestConvertPlanHotels_DifferentCurrency(t *testing.T) {
	hotels := []PlanHotel{
		{PerNight: 50, Total: 200, Currency: "USD"},
	}
	convertPlanHotels(context.Background(), hotels, "EUR", fakeFX)
	if h := hotels[0]; h.PerNight != 25 || h.Total != 100 || h.Currency != "EUR" {
		t.Errorf("got perNight=%v total=%v currency=%q, want 25 / 100 / EUR", h.PerNight, h.Total, h.Currency)
	}
}

func TestConvertPlanHotels_ZeroPerNightSkipped(t *testing.T) {
	hotels := []PlanHotel{
		{PerNight: 0, Total: 200, Currency: "USD"},
	}
	convertPlanHotels(context.Background(), hotels, "EUR", fakeFX)
	if hotels[0].PerNight != 0 || hotels[0].Total != 100 {
		t.Errorf("PerNight = %v, Total = %v, want 0 (zero skipped) and 100", hotels[0].PerNight, hotels[0].Total)
	}
}

func TestConvertPlanHotels_ZeroTotal(t *testing.T) {
	hotels := []PlanHotel{
		{PerNight: 50, Total: 0, Currency: "USD"},
	}
	convertPlanHotels(context.Background(), hotels, "EUR", fakeFX)
	// Total stays 0.
	if hotels[0].Total != 0 {
		t.Errorf("Total = %v, want 0 (should not convert zero)", hotels[0].Total)
	}
}

// ============================================================
// convertPlanFlights — different currency conversion
// ============================================================

func TestConvertPlanFlights_DifferentCurrency(t *testing.T) {
	flights := []PlanFlight{
		{Price: 200, Currency: "USD"},
	}
	convertPlanFlights(context.Background(), flights, "EUR", fakeFX)
	if flights[0].Price != 100 || flights[0].Currency != "EUR" {
		t.Errorf("got price=%v currency=%q, want 100 / EUR", flights[0].Price, flights[0].Currency)
	}
}

// ============================================================
// convertedPlanAmount
// ============================================================

func TestConvertedPlanAmount_SameCurrencyNoOp(t *testing.T) {
	got := convertedPlanAmount(context.Background(), 100.0, "EUR", "EUR")
	if got != 100.0 {
		t.Errorf("got %v, want 100.0", got)
	}
}

// ============================================================
// WeekendOptions.defaults
// ============================================================

// ============================================================
// parseMonth — additional format
// ============================================================

func TestParseMonth_NumericMonthOnly(t *testing.T) {
	dep, ret, display, err := parseMonth("2026-12")
	if err != nil {
		t.Fatalf("parseMonth('2026-12') error: %v", err)
	}
	if dep == "" || ret == "" || display == "" {
		t.Errorf("parseMonth('2026-12') returned empty values: %q, %q, %q", dep, ret, display)
	}
	if display != "December 2026" {
		t.Errorf("display = %q, want 'December 2026'", display)
	}
}
