package flights

import (
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
