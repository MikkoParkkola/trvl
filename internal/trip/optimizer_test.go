package trip

import (
	"context"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

func TestOptimizeTripDates_MissingOrigin(t *testing.T) {
	_, err := OptimizeTripDates(t.Context(), OptimizeTripDatesInput{
		Destination: "BCN",
		FromDate:    "2026-07-01",
		ToDate:      "2026-07-31",
		TripLength:  7,
	})
	if err == nil {
		t.Error("expected error for missing origin")
	}
}

func TestOptimizeTripDates_MissingDates(t *testing.T) {
	_, err := OptimizeTripDates(t.Context(), OptimizeTripDatesInput{
		Origin:      "HEL",
		Destination: "BCN",
		TripLength:  7,
	})
	if err == nil {
		t.Error("expected error for missing dates")
	}
}

func TestOptimizeTripDates_InvalidTripLength(t *testing.T) {
	_, err := OptimizeTripDates(t.Context(), OptimizeTripDatesInput{
		Origin:      "HEL",
		Destination: "BCN",
		FromDate:    "2026-07-01",
		ToDate:      "2026-07-31",
		TripLength:  0,
	})
	if err == nil {
		t.Error("expected error for zero trip length")
	}
}

func TestOptimizeTripDates_ToBeforeFrom(t *testing.T) {
	_, err := OptimizeTripDates(t.Context(), OptimizeTripDatesInput{
		Origin:      "HEL",
		Destination: "BCN",
		FromDate:    "2026-07-31",
		ToDate:      "2026-07-01",
		TripLength:  7,
	})
	if err == nil {
		t.Error("expected error for to_date before from_date")
	}
}

func TestOptimizeTripDates_InvalidFromDate(t *testing.T) {
	_, err := OptimizeTripDates(t.Context(), OptimizeTripDatesInput{
		Origin:      "HEL",
		Destination: "BCN",
		FromDate:    "bad",
		ToDate:      "2026-07-31",
		TripLength:  7,
	})
	if err == nil {
		t.Error("expected error for invalid from_date")
	}
}

func TestOptimizeTripDates_InvalidToDate(t *testing.T) {
	_, err := OptimizeTripDates(t.Context(), OptimizeTripDatesInput{
		Origin:      "HEL",
		Destination: "BCN",
		FromDate:    "2026-07-01",
		ToDate:      "bad",
		TripLength:  7,
	})
	if err == nil {
		t.Error("expected error for invalid to_date")
	}
}

func TestOptimizeTripDates_DefaultGuests(t *testing.T) {
	// Guests defaults to 1 when 0. This attempts a network call; bound it
	// with a short deadline so CI does not hang on provider retry/backoff
	// (matches the _NoNetworkFallback idiom). The default-guests validation
	// path still executes for coverage.
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	_, _ = OptimizeTripDates(ctx, OptimizeTripDatesInput{
		Origin:      "HEL",
		Destination: "BCN",
		FromDate:    "2026-07-01",
		ToDate:      "2026-07-31",
		TripLength:  7,
		Guests:      0,
	})
}

func TestBuildDateOptions_BasicSorting(t *testing.T) {
	dates := []models.DatePriceResult{
		{Date: "2026-07-05", Price: 300, Currency: "EUR"},
		{Date: "2026-07-10", Price: 150, Currency: "EUR"},
		{Date: "2026-07-15", Price: 200, Currency: "EUR"},
	}
	input := OptimizeTripDatesInput{
		TripLength: 7,
		Guests:     1,
	}

	options := buildDateOptions(dates, input)

	if len(options) != 3 {
		t.Fatalf("expected 3 options, got %d", len(options))
	}
	// Verify return dates are TripLength days after depart dates.
	if options[0].ReturnDate != "2026-07-12" {
		t.Errorf("option[0] return = %q, want 2026-07-12", options[0].ReturnDate)
	}
	if options[1].ReturnDate != "2026-07-17" {
		t.Errorf("option[1] return = %q, want 2026-07-17", options[1].ReturnDate)
	}
}

func TestBuildDateOptions_SkipsZeroPrice(t *testing.T) {
	dates := []models.DatePriceResult{
		{Date: "2026-07-05", Price: 0, Currency: "EUR"},
		{Date: "2026-07-10", Price: 150, Currency: "EUR"},
	}
	input := OptimizeTripDatesInput{
		TripLength: 3,
		Guests:     1,
	}

	options := buildDateOptions(dates, input)

	if len(options) != 1 {
		t.Fatalf("expected 1 option (zero-price skipped), got %d", len(options))
	}
	if options[0].DepartDate != "2026-07-10" {
		t.Errorf("option date = %q, want 2026-07-10", options[0].DepartDate)
	}
}

func TestBuildDateOptions_GuestsMultiplier(t *testing.T) {
	dates := []models.DatePriceResult{
		{Date: "2026-07-05", Price: 100, Currency: "EUR"},
	}
	input := OptimizeTripDatesInput{
		TripLength: 5,
		Guests:     3,
	}

	options := buildDateOptions(dates, input)

	if len(options) != 1 {
		t.Fatalf("expected 1 option, got %d", len(options))
	}
	if options[0].FlightCost != 300 {
		t.Errorf("flight cost = %v, want 300 (100 * 3 guests)", options[0].FlightCost)
	}
}

func TestBuildDateOptions_UsesCurrencyOverride(t *testing.T) {
	dates := []models.DatePriceResult{
		{Date: "2026-07-05", Price: 100, Currency: "USD"},
	}
	input := OptimizeTripDatesInput{
		TripLength: 5,
		Guests:     1,
		Currency:   "EUR",
	}

	options := buildDateOptions(dates, input)

	if len(options) != 1 {
		t.Fatalf("expected 1 option, got %d", len(options))
	}
	if options[0].Currency != "EUR" {
		t.Errorf("currency = %q, want EUR (overridden)", options[0].Currency)
	}
}

func TestBuildDateOptions_FallsBackToSourceCurrency(t *testing.T) {
	dates := []models.DatePriceResult{
		{Date: "2026-07-05", Price: 100, Currency: "USD"},
	}
	input := OptimizeTripDatesInput{
		TripLength: 5,
		Guests:     1,
		Currency:   "", // no override
	}

	options := buildDateOptions(dates, input)

	if len(options) != 1 {
		t.Fatalf("expected 1 option, got %d", len(options))
	}
	if options[0].Currency != "USD" {
		t.Errorf("currency = %q, want USD (from source)", options[0].Currency)
	}
}

func TestBuildDateOptions_InvalidDate(t *testing.T) {
	dates := []models.DatePriceResult{
		{Date: "not-a-date", Price: 100, Currency: "EUR"},
		{Date: "2026-07-10", Price: 200, Currency: "EUR"},
	}
	input := OptimizeTripDatesInput{
		TripLength: 3,
		Guests:     1,
	}

	options := buildDateOptions(dates, input)

	if len(options) != 1 {
		t.Fatalf("expected 1 option (invalid date skipped), got %d", len(options))
	}
	if options[0].DepartDate != "2026-07-10" {
		t.Errorf("option date = %q, want 2026-07-10", options[0].DepartDate)
	}
}

func TestBuildDateOptions_Empty(t *testing.T) {
	options := buildDateOptions(nil, OptimizeTripDatesInput{TripLength: 3, Guests: 1})
	if len(options) != 0 {
		t.Errorf("expected 0 options for nil dates, got %d", len(options))
	}
}

func TestOptimizeTripDates_MissingDestination(t *testing.T) {
	_, err := OptimizeTripDates(t.Context(), OptimizeTripDatesInput{
		Origin:     "HEL",
		FromDate:   "2026-07-01",
		ToDate:     "2026-07-31",
		TripLength: 7,
	})
	if err == nil {
		t.Error("expected error for missing destination")
	}
}

func TestOptimizeTripDates_NegativeTripLength(t *testing.T) {
	_, err := OptimizeTripDates(t.Context(), OptimizeTripDatesInput{
		Origin:      "HEL",
		Destination: "BCN",
		FromDate:    "2026-07-01",
		ToDate:      "2026-07-31",
		TripLength:  -1,
	})
	if err == nil {
		t.Error("expected error for negative trip length")
	}
}
