package trip

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

func TestCalculateTripCost_MissingOrigin(t *testing.T) {
	_, err := CalculateTripCost(t.Context(), TripCostInput{
		Destination: "BCN",
		DepartDate:  "2026-07-01",
		ReturnDate:  "2026-07-08",
	})
	if err == nil {
		t.Error("expected error for missing origin")
	}
}

func TestCalculateTripCost_MissingDates(t *testing.T) {
	_, err := CalculateTripCost(t.Context(), TripCostInput{
		Origin:      "HEL",
		Destination: "BCN",
	})
	if err == nil {
		t.Error("expected error for missing dates")
	}
}

func TestCalculateTripCost_InvalidDates(t *testing.T) {
	_, err := CalculateTripCost(t.Context(), TripCostInput{
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "not-a-date",
		ReturnDate:  "2026-07-08",
	})
	if err == nil {
		t.Error("expected error for invalid depart date")
	}
}

func TestCalculateTripCost_ReturnBeforeDepart(t *testing.T) {
	_, err := CalculateTripCost(t.Context(), TripCostInput{
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-08",
		ReturnDate:  "2026-07-01",
	})
	if err == nil {
		t.Error("expected error for return before depart")
	}
}

func TestCalculateTripCost_SameDay(t *testing.T) {
	_, err := CalculateTripCost(t.Context(), TripCostInput{
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-01",
		ReturnDate:  "2026-07-01",
	})
	if err == nil {
		t.Error("expected error for same-day trip")
	}
}

func TestCheapestFlight(t *testing.T) {
	flts := []models.FlightResult{
		{Price: 300, Currency: "EUR", Stops: 1},
		{Price: 150, Currency: "EUR", Stops: 0},
		{Price: 450, Currency: "EUR", Stops: 2},
	}
	best := cheapestFlight(flts)
	if best.Price != 150 {
		t.Errorf("cheapestFlight = %v, want 150", best.Price)
	}
}

func TestCheapestFlight_SkipsZero(t *testing.T) {
	flts := []models.FlightResult{
		{Price: 0, Currency: "EUR"},
		{Price: 200, Currency: "EUR"},
	}
	best := cheapestFlight(flts)
	if best.Price != 200 {
		t.Errorf("cheapestFlight = %v, want 200", best.Price)
	}
}

func TestCheapestHotel(t *testing.T) {
	htls := []models.HotelResult{
		{Price: 120, Currency: "EUR", Name: "Hotel A"},
		{Price: 80, Currency: "EUR", Name: "Hotel B"},
		{Price: 200, Currency: "EUR", Name: "Hotel C"},
	}
	best := cheapestHotel(htls)
	if best.Price != 80 {
		t.Errorf("cheapestHotel = %v, want 80", best.Price)
	}
	if best.Name != "Hotel B" {
		t.Errorf("cheapestHotel name = %q, want Hotel B", best.Name)
	}
}

func TestCheapestHotel_SkipsZero(t *testing.T) {
	htls := []models.HotelResult{
		{Price: 0, Currency: "EUR"},
		{Price: 100, Currency: "EUR", Name: "Hotel A"},
	}
	best := cheapestHotel(htls)
	if best.Price != 100 {
		t.Errorf("cheapestHotel = %v, want 100", best.Price)
	}
}

func TestCheapestHotel_SkipsLeadInPricesForFinalTotals(t *testing.T) {
	htls := []models.HotelResult{
		{
			Price:           60,
			Currency:        "EUR",
			Name:            "Lead-In",
			PriceBasis:      models.PriceBasisLeadIn,
			PriceConfidence: models.PriceConfidenceUnverified,
		},
		{
			Price:           110,
			Currency:        "EUR",
			Name:            "Room-Level",
			PriceBasis:      models.PriceBasisRoomTotal,
			PriceConfidence: models.PriceConfidenceRoomLevel,
		},
	}
	best := cheapestHotel(htls)
	if best.Name != "Room-Level" {
		t.Fatalf("cheapestHotel selected %q, want Room-Level", best.Name)
	}
}

func TestCheapestFlight_SingleElement(t *testing.T) {
	flts := []models.FlightResult{
		{Price: 250, Currency: "EUR", Stops: 1},
	}
	best := cheapestFlight(flts)
	if best.Price != 250 {
		t.Errorf("cheapestFlight = %v, want 250", best.Price)
	}
}

func TestCheapestFlight_AllZero(t *testing.T) {
	flts := []models.FlightResult{
		{Price: 0, Currency: "EUR"},
		{Price: 0, Currency: "EUR"},
	}
	best := cheapestFlight(flts)
	// When all prices are zero, returns the first element (no positive price found).
	if best.Price != 0 {
		t.Errorf("cheapestFlight = %v, want 0", best.Price)
	}
}

func TestCheapestFlight_NegativePrice(t *testing.T) {
	flts := []models.FlightResult{
		{Price: -10, Currency: "EUR"},
		{Price: 200, Currency: "EUR"},
	}
	best := cheapestFlight(flts)
	if best.Price != 200 {
		t.Errorf("cheapestFlight = %v, want 200 (should skip negative)", best.Price)
	}
}

func TestCheapestFlight_FirstZeroThenMultiple(t *testing.T) {
	flts := []models.FlightResult{
		{Price: 0, Currency: "EUR"},
		{Price: 300, Currency: "EUR"},
		{Price: 100, Currency: "EUR"},
	}
	best := cheapestFlight(flts)
	if best.Price != 100 {
		t.Errorf("cheapestFlight = %v, want 100", best.Price)
	}
}

func TestCheapestHotel_SingleElement(t *testing.T) {
	htls := []models.HotelResult{
		{Price: 80, Currency: "EUR", Name: "Solo Hotel"},
	}
	best := cheapestHotel(htls)
	if best.Price != 80 {
		t.Errorf("cheapestHotel = %v, want 80", best.Price)
	}
	if best.Name != "Solo Hotel" {
		t.Errorf("cheapestHotel name = %q, want Solo Hotel", best.Name)
	}
}

func TestCheapestHotel_AllZero(t *testing.T) {
	htls := []models.HotelResult{
		{Price: 0, Currency: "EUR", Name: "Free A"},
		{Price: 0, Currency: "EUR", Name: "Free B"},
	}
	best := cheapestHotel(htls)
	if best.Price != 0 {
		t.Errorf("cheapestHotel = %v, want 0", best.Price)
	}
}

func TestCalculateTripCost_InvalidReturnDate(t *testing.T) {
	_, err := CalculateTripCost(t.Context(), TripCostInput{
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-01",
		ReturnDate:  "bad-date",
	})
	if err == nil {
		t.Error("expected error for invalid return date")
	}
}

func TestCalculateTripCost_MissingDestination(t *testing.T) {
	_, err := CalculateTripCost(t.Context(), TripCostInput{
		Origin:     "HEL",
		DepartDate: "2026-07-01",
		ReturnDate: "2026-07-08",
	})
	if err == nil {
		t.Error("expected error for missing destination")
	}
}

func TestCalculateTripCost_RejectsNonPositiveGuests(t *testing.T) {
	for _, guests := range []int{0, -5} {
		t.Run(fmt.Sprintf("guests=%d", guests), func(t *testing.T) {
			_, err := CalculateTripCost(t.Context(), TripCostInput{
				Origin:      "HEL",
				Destination: "BCN",
				DepartDate:  "2026-07-01",
				ReturnDate:  "2026-07-08",
				Guests:      guests,
			})
			if err == nil {
				t.Fatal("expected error for nonpositive guests")
			}
			if got := err.Error(); got != "guests must be at least 1" {
				t.Fatalf("error = %q, want %q", got, "guests must be at least 1")
			}
		})
	}
}

func TestAssembleTripCost_AllSuccess(t *testing.T) {
	result := &TripCostResult{Currency: "EUR", Nights: 3}
	outResult := &models.FlightSearchResult{
		Success: true,
		Flights: []models.FlightResult{
			{Price: 150, Currency: "EUR", Stops: 0},
			{Price: 250, Currency: "EUR", Stops: 1},
		},
	}
	retResult := &models.FlightSearchResult{
		Success: true,
		Flights: []models.FlightResult{
			{Price: 180, Currency: "EUR", Stops: 1},
		},
	}
	hotelResult := &models.HotelSearchResult{
		Success: true,
		Hotels: []models.HotelResult{
			{Price: 80, Currency: "EUR", Name: "Hotel Test"},
		},
	}

	assembleTripCost(context.Background(), result, "", outResult, nil, retResult, nil, hotelResult, nil, 3, 2)

	if !result.Success {
		t.Fatal("expected success")
	}
	if result.Flights.Outbound != 150 {
		t.Errorf("outbound = %v, want 150", result.Flights.Outbound)
	}
	if result.Flights.Return != 180 {
		t.Errorf("return = %v, want 180", result.Flights.Return)
	}
	if result.Flights.OutboundStops != 0 {
		t.Errorf("outbound stops = %v, want 0", result.Flights.OutboundStops)
	}
	if result.Flights.ReturnStops != 1 {
		t.Errorf("return stops = %v, want 1", result.Flights.ReturnStops)
	}
	if result.Hotels.PerNight != 80 {
		t.Errorf("hotel per night = %v, want 80", result.Hotels.PerNight)
	}
	if result.Hotels.Total != 240 {
		t.Errorf("hotel total = %v, want 240", result.Hotels.Total)
	}
	if result.Hotels.Name != "Hotel Test" {
		t.Errorf("hotel name = %q, want Hotel Test", result.Hotels.Name)
	}
	// Total = (150 + 180) * 2 + 80 * 3 = 660 + 240 = 900
	if result.Total != 900 {
		t.Errorf("total = %v, want 900", result.Total)
	}
	// PerPerson = 900 / 2 = 450
	if result.PerPerson != 450 {
		t.Errorf("per person = %v, want 450", result.PerPerson)
	}
	// PerDay = 900 / 3 = 300
	if result.PerDay != 300 {
		t.Errorf("per day = %v, want 300", result.PerDay)
	}
}

func TestAssembleTripCost_FlightsOnlyNoHotel(t *testing.T) {
	result := &TripCostResult{Currency: "EUR", Nights: 2}
	outResult := &models.FlightSearchResult{
		Success: true,
		Flights: []models.FlightResult{{Price: 100, Currency: "EUR"}},
	}
	retResult := &models.FlightSearchResult{
		Success: true,
		Flights: []models.FlightResult{{Price: 120, Currency: "EUR"}},
	}

	assembleTripCost(context.Background(), result, "", outResult, nil, retResult, nil, nil, fmt.Errorf("hotel error"), 2, 1)

	if !result.Success {
		t.Fatal("should succeed with flights only")
	}
	// Total = (100 + 120) * 1 + 0 = 220
	if result.Total != 220 {
		t.Errorf("total = %v, want 220", result.Total)
	}
	if result.Error != "partial failure: hotels: hotel error" {
		t.Errorf("error = %q, want partial hotel failure", result.Error)
	}
}

func TestAssembleTripCost_SuccessKeepsMultiplePartialFailures(t *testing.T) {
	result := &TripCostResult{Currency: "EUR", Nights: 1}
	outResult := &models.FlightSearchResult{
		Success: true,
		Flights: []models.FlightResult{{Price: 100, Currency: "EUR"}},
	}

	assembleTripCost(context.Background(), result, "", outResult, nil, nil, fmt.Errorf("return fail"), nil, fmt.Errorf("hotel fail"), 1, 1)

	if !result.Success {
		t.Fatal("should succeed with outbound pricing available")
	}
	if result.Total != 100 {
		t.Errorf("total = %v, want 100", result.Total)
	}
	if result.Error != "partial failure: return flights: return fail; hotels: hotel fail" {
		t.Errorf("error = %q, want combined partial failures", result.Error)
	}
}

func TestAssembleTripCost_AllErrors(t *testing.T) {
	result := &TripCostResult{Currency: "EUR", Nights: 2}
	outErr := fmt.Errorf("outbound fail")
	retErr := fmt.Errorf("return fail")
	hotelErr := fmt.Errorf("hotel fail")

	assembleTripCost(context.Background(), result, "", nil, outErr, nil, retErr, nil, hotelErr, 2, 1)

	if result.Success {
		t.Error("expected failure when all searches fail")
	}
	if result.Error != "outbound flights: outbound fail; return flights: return fail; hotels: hotel fail" {
		t.Errorf("error = %q, want combined failures", result.Error)
	}
}

func TestAssembleTripCost_NilResults(t *testing.T) {
	result := &TripCostResult{Currency: "EUR", Nights: 1}
	assembleTripCost(context.Background(), result, "", nil, nil, nil, nil, nil, nil, 1, 1)

	if result.Success {
		t.Error("expected failure with nil results and no errors")
	}
}

func TestAssembleTripCost_EmptyFlightResults(t *testing.T) {
	result := &TripCostResult{Currency: "EUR", Nights: 2}
	outResult := &models.FlightSearchResult{Success: true, Flights: nil}
	retResult := &models.FlightSearchResult{Success: true, Flights: []models.FlightResult{}}

	assembleTripCost(context.Background(), result, "", outResult, nil, retResult, nil, nil, nil, 2, 1)

	if result.Success {
		t.Error("expected failure when no flights found")
	}
}

func TestAssembleTripCost_ReturnSetsCurrencyWhenOutboundEmpty(t *testing.T) {
	result := &TripCostResult{Currency: "EUR", Nights: 1}
	// Outbound fails, return succeeds: currency from return should be used for flights.
	retResult := &models.FlightSearchResult{
		Success: true,
		Flights: []models.FlightResult{{Price: 200, Currency: "USD"}},
	}

	assembleTripCost(context.Background(), result, "", nil, fmt.Errorf("out fail"), retResult, nil, nil, nil, 1, 1)

	if result.Flights.Currency != "USD" {
		t.Errorf("flights currency = %q, want USD", result.Flights.Currency)
	}
}

func TestAssembleTripCost_SuccessFalse(t *testing.T) {
	result := &TripCostResult{Currency: "EUR", Nights: 1}
	// Unsuccessful search result should be ignored.
	outResult := &models.FlightSearchResult{
		Success: false,
		Flights: []models.FlightResult{{Price: 100, Currency: "EUR"}},
	}

	assembleTripCost(context.Background(), result, "", outResult, nil, nil, nil, nil, nil, 1, 1)

	if result.Flights.Outbound != 0 {
		t.Errorf("outbound should be 0 when success=false, got %v", result.Flights.Outbound)
	}
}

func TestApplyTripCostCurrencyAndTotals_UsesRequestedCurrency(t *testing.T) {
	result := &TripCostResult{
		Flights: FlightCost{
			Outbound: 100,
			Return:   120,
			Currency: "EUR",
		},
		Hotels: HotelCost{
			PerNight: 50,
			Total:    100,
			Currency: "USD",
		},
		Nights: 2,
	}

	applyTripCostCurrencyAndTotals(
		context.Background(),
		result,
		"USD",
		2,
		1,
		func(_ context.Context, amount float64, from, to string) (float64, string) {
			if from == to || to == "" {
				return amount, from
			}
			if from == "EUR" && to == "USD" {
				return amount * 2, "USD"
			}
			return amount, to
		},
	)

	if result.Flights.Outbound != 200 {
		t.Errorf("outbound = %v, want 200", result.Flights.Outbound)
	}
	if result.Flights.Return != 240 {
		t.Errorf("return = %v, want 240", result.Flights.Return)
	}
	if result.Flights.Currency != "USD" {
		t.Errorf("flight currency = %q, want USD", result.Flights.Currency)
	}
	if result.Hotels.Total != 100 {
		t.Errorf("hotel total = %v, want 100", result.Hotels.Total)
	}
	if result.Hotels.Currency != "USD" {
		t.Errorf("hotel currency = %q, want USD", result.Hotels.Currency)
	}
	if result.Total != 540 {
		t.Errorf("total = %v, want 540", result.Total)
	}
	if result.Currency != "USD" {
		t.Errorf("currency = %q, want USD", result.Currency)
	}
	if result.PerPerson != 540 {
		t.Errorf("per person = %v, want 540", result.PerPerson)
	}
	if result.PerDay != 270 {
		t.Errorf("per day = %v, want 270", result.PerDay)
	}
}

func TestApplyTripCostCurrencyAndTotals_UsesAvailableCurrencyWhenUnrequested(t *testing.T) {
	result := &TripCostResult{
		Flights: FlightCost{
			Outbound: 100,
			Return:   120,
			Currency: "EUR",
		},
		Hotels: HotelCost{
			PerNight: 50,
			Total:    100,
			Currency: "USD",
		},
		Nights: 2,
	}

	applyTripCostCurrencyAndTotals(
		context.Background(),
		result,
		"",
		2,
		1,
		func(_ context.Context, amount float64, from, to string) (float64, string) {
			if from == to || to == "" {
				return amount, from
			}
			if from == "USD" && to == "EUR" {
				return amount / 2, "EUR"
			}
			return amount, to
		},
	)

	if result.Currency != "EUR" {
		t.Errorf("currency = %q, want EUR", result.Currency)
	}
	if result.Hotels.Total != 50 {
		t.Errorf("hotel total = %v, want 50", result.Hotels.Total)
	}
	if result.Hotels.PerNight != 25 {
		t.Errorf("hotel per night = %v, want 25", result.Hotels.PerNight)
	}
	if result.Hotels.Currency != "EUR" {
		t.Errorf("hotel currency = %q, want EUR", result.Hotels.Currency)
	}
	if result.Total != 270 {
		t.Errorf("total = %v, want 270", result.Total)
	}
}

// ============================================================
// Currency honesty tests for applyTripCostCurrencyAndTotals (conversion failure)
// ============================================================

func TestApplyTripCostCurrencyAndTotals_MixedCurrencyFlightFailsToConvert(t *testing.T) {
	// Flight in JPY, stub fails convert (returns orig amount + from), hotel EUR succeeds.
	result := &TripCostResult{
		Flights: FlightCost{
			Outbound: 100,
			Return:   120,
			Currency: "JPY",
		},
		Hotels: HotelCost{
			PerNight: 50,
			Total:    100,
			Currency: "EUR",
		},
		Nights: 2,
	}

	applyTripCostCurrencyAndTotals(
		context.Background(),
		result,
		"EUR",
		2,
		1,
		func(_ context.Context, amount float64, from, to string) (float64, string) {
			if from == to || to == "" {
				return amount, from
			}
			if from == "EUR" && to == "EUR" {
				return amount, "EUR"
			}
			// JPY to EUR: simulate unknown rate -> return orig, from (as destinations.ConvertCurrency does)
			return amount, from
		},
	)

	if result.Success {
		t.Fatal("expected Success==false when a component could not be converted to target")
	}
	if !strings.Contains(result.Error, "needs_price_verification") {
		t.Errorf("error = %q, want contains 'needs_price_verification'", result.Error)
	}
	if strings.Contains(result.Error, "EUR") && strings.Contains(result.Error, "JPY") {
		// at least the example phrasing
	}
	// Total must not be a clean sum presented as EUR (we zero it on dishonesty)
	if result.Total != 0 {
		t.Errorf("Total = %v, want 0 (no clean total when not all in target)", result.Total)
	}
	if result.Currency != "" {
		t.Errorf("Currency = %q, want empty (not falsely claiming EUR)", result.Currency)
	}
	// per-component left intact with real currencies
	if result.Flights.Outbound != 100 || result.Flights.Currency != "JPY" {
		t.Errorf("flight kept as 100 JPY, got %v %s", result.Flights.Outbound, result.Flights.Currency)
	}
	if result.Hotels.Total != 100 || result.Hotels.Currency != "EUR" {
		t.Errorf("hotel kept as 100 EUR, got %v %s", result.Hotels.Total, result.Hotels.Currency)
	}
}

func TestApplyTripCostCurrencyAndTotals_AllComponentsConvertSuccessfully(t *testing.T) {
	result := &TripCostResult{
		Flights: FlightCost{
			Outbound: 150,
			Return:   300,
			Currency: "JPY",
		},
		Hotels: HotelCost{
			PerNight: 50,
			Total:    100,
			Currency: "EUR",
		},
		Nights: 2,
	}

	applyTripCostCurrencyAndTotals(
		context.Background(),
		result,
		"EUR",
		2,
		1,
		func(_ context.Context, amount float64, from, to string) (float64, string) {
			if from == to || to == "" {
				return amount, from
			}
			if from == "JPY" && to == "EUR" {
				return amount / 150, "EUR" // fake rate: 150JPY=1EUR
			}
			if from == "EUR" && to == "EUR" {
				return amount, "EUR"
			}
			return amount, from
		},
	)

	if !result.Success {
		t.Fatal("expected Success==true when all convert to target")
	}
	if result.Currency != "EUR" {
		t.Errorf("currency = %q, want EUR", result.Currency)
	}
	// Outbound 150/150=1, return 300/150=2, hotel 100; total per person flights 3 + hotel 100 = 103
	if result.Total != 103 {
		t.Errorf("total = %v, want 103 (sum of converted amounts)", result.Total)
	}
}

func TestCheapestHotel_UsesPriceForRankingAcrossCurrencies(t *testing.T) {
	// Both must be eligible per HotelPriceEligibleForFinalTripCost rules.
	// Use explicit valid basis/conf so fixtures are production-like.
	htls := []models.HotelResult{
		{
			Price:           15000,
			Currency:        "JPY",
			Name:            "CheapInComparable JPY",
			ComparablePrice: 90,
			PriceBasis:      models.PriceBasisRoomTotal,
			PriceConfidence: models.PriceConfidenceRoomLevel,
		},
		{
			Price:           100,
			Currency:        "EUR",
			Name:            "EUR ExpensiveOnComparable",
			ComparablePrice: 100,
			PriceBasis:      models.PriceBasisRoomTotal,
			PriceConfidence: models.PriceConfidenceRoomLevel,
		},
	}
	best := cheapestHotel(htls)
	if best.Name != "CheapInComparable JPY" {
		t.Errorf("cheapestHotel picked %q, want the JPY one (PriceForRanking 90 < 100)", best.Name)
	}
}
