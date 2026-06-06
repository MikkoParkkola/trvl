package flights

import (
	"context"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

func TestMergeFlightResults_SortsCheapestAndFiltersStops(t *testing.T) {
	googleFlights := []models.FlightResult{
		{
			Price:    200,
			Currency: "EUR",
			Duration: 120,
			Stops:    0,
			Provider: "google_flights",
			Legs: []models.FlightLeg{
				{
					DepartureAirport: models.AirportInfo{Code: "HEL"},
					ArrivalAirport:   models.AirportInfo{Code: "DBV"},
					DepartureTime:    "2026-07-01T08:00",
					ArrivalTime:      "2026-07-01T10:00",
				},
			},
		},
	}
	kiwiFlights := []models.FlightResult{
		{
			Price:    150,
			Currency: "EUR",
			Duration: 300,
			Stops:    2,
			Provider: "kiwi",
			Legs: []models.FlightLeg{
				{DepartureAirport: models.AirportInfo{Code: "HEL"}, ArrivalAirport: models.AirportInfo{Code: "ARN"}, DepartureTime: "2026-07-01T06:00", ArrivalTime: "2026-07-01T07:00"},
				{DepartureAirport: models.AirportInfo{Code: "ARN"}, ArrivalAirport: models.AirportInfo{Code: "WAW"}, DepartureTime: "2026-07-01T08:00", ArrivalTime: "2026-07-01T09:00"},
				{DepartureAirport: models.AirportInfo{Code: "WAW"}, ArrivalAirport: models.AirportInfo{Code: "DBV"}, DepartureTime: "2026-07-01T10:00", ArrivalTime: "2026-07-01T11:00"},
			},
		},
		{
			Price:    175,
			Currency: "EUR",
			Duration: 180,
			Stops:    1,
			Provider: "kiwi",
			Legs: []models.FlightLeg{
				{DepartureAirport: models.AirportInfo{Code: "HEL"}, ArrivalAirport: models.AirportInfo{Code: "ARN"}, DepartureTime: "2026-07-01T07:00", ArrivalTime: "2026-07-01T08:00"},
				{DepartureAirport: models.AirportInfo{Code: "ARN"}, ArrivalAirport: models.AirportInfo{Code: "DBV"}, DepartureTime: "2026-07-01T09:00", ArrivalTime: "2026-07-01T10:00"},
			},
		},
	}

	merged := mergeFlightResults(googleFlights, kiwiFlights, nil, SearchOptions{
		MaxStops: models.OneStop,
		SortBy:   models.SortCheapest,
	})

	if len(merged) != 2 {
		t.Fatalf("merged count = %d, want 2", len(merged))
	}
	if merged[0].Price != 175 {
		t.Fatalf("first price = %.0f, want 175", merged[0].Price)
	}
	if merged[1].Price != 200 {
		t.Fatalf("second price = %.0f, want 200", merged[1].Price)
	}
}

// TestApplyComparableBaseline_LCCBagRanking proves an LCC bare fare ranks by its
// all-in (fare + carry-on fee) so it no longer unfairly beats an included fare.
func TestApplyComparableBaseline_LCCBagRanking(t *testing.T) {
	leg := func(code string) []models.FlightLeg {
		return []models.FlightLeg{{AirlineCode: code, DepartureTime: "2026-06-01T08:00"}}
	}
	// FR (Ryanair) is OverheadOnly -> +EUR15 carry-on; AY (Finnair) includes bag.
	flights := []models.FlightResult{
		{Price: 45, Currency: "EUR", Provider: "ryanair", Legs: leg("FR")},
		{Price: 55, Currency: "EUR", Provider: "finnair", Legs: leg("AY")},
	}
	applyComparableBaseline(flights)
	if flights[0].ComparablePrice == 0 {
		t.Fatal("Ryanair comparable price not set (expected carry-on fee added)")
	}
	if flights[0].ComparablePrice <= 45 {
		t.Errorf("Ryanair comparable %v should exceed base 45", flights[0].ComparablePrice)
	}
	// Finnair includes the bag -> no uplift -> ranking value stays 55.
	if flights[1].PriceForRanking() != 55 {
		t.Errorf("Finnair ranking value = %v, want 55", flights[1].PriceForRanking())
	}
	// If Ryanair all-in (45+15=60) now exceeds Finnair 55, the sort must reflect it.
	sortFlightResults(flights, models.SortCheapest)
	if flights[0].Provider != "finnair" {
		t.Errorf("after all-in ranking, cheapest should be finnair, got %s (FR comparable=%v)", flights[0].Provider, comparableOf(flights, "ryanair"))
	}
}

func comparableOf(flights []models.FlightResult, provider string) float64 {
	for _, f := range flights {
		if f.Provider == provider {
			return f.PriceForRanking()
		}
	}
	return 0
}

func TestNormalizeFlightCurrencies(t *testing.T) {
	// Stub converter: USD->EUR at 0.9, no network.
	conv := func(_ context.Context, amount float64, from, to string) (float64, string) {
		if from == "USD" && to == "EUR" {
			return amount * 0.9, "EUR"
		}
		if from == to {
			return amount, to
		}
		return amount, from // other pairs: leave unchanged
	}
	flights := []models.FlightResult{
		{Price: 100, Currency: "USD", Provider: "skiplagged"},
		{Price: 90, Currency: "EUR", Provider: "google"},
		{Price: 50, Currency: "GBP", Provider: "x"}, // unconvertible -> unchanged
	}
	normalizeFlightCurrencies(context.Background(), flights, "EUR", conv)
	if flights[0].Currency != "EUR" || flights[0].Price != 90 {
		t.Errorf("USD->EUR failed: %v %s", flights[0].Price, flights[0].Currency)
	}
	if flights[2].Currency != "GBP" || flights[2].Price != 50 {
		t.Errorf("unconvertible should stay GBP 50, got %v %s", flights[2].Price, flights[2].Currency)
	}
	normalizeFlightCurrencies(context.Background(), flights, "", conv)
	normalizeFlightCurrencies(context.Background(), flights, "EUR", nil)
}
