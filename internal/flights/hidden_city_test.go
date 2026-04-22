package flights

import (
	"context"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

func TestHiddenCitySeedBeyondDestinations(t *testing.T) {
	ams := hiddenCitySeedBeyondDestinations("AMS")
	if len(ams) < 10 {
		t.Errorf("AMS seed too small: %d", len(ams))
	}
	// AMS seed must include HEL (Mikko's proven case).
	found := false
	for _, a := range ams {
		if a == "HEL" {
			found = true
		}
	}
	if !found {
		t.Error("AMS seed missing HEL")
	}

	hel := hiddenCitySeedBeyondDestinations("HEL")
	if len(hel) < 10 {
		t.Errorf("HEL seed too small: %d", len(hel))
	}

	other := hiddenCitySeedBeyondDestinations("XYZ")
	if len(other) == 0 {
		t.Error("default seed empty")
	}
}

func TestRoutesThroughHub(t *testing.T) {
	// Routes through AMS.
	flt := models.FlightResult{
		Legs: []models.FlightLeg{
			{DepartureAirport: models.AirportInfo{Code: "PRG"}, ArrivalAirport: models.AirportInfo{Code: "AMS"}},
			{DepartureAirport: models.AirportInfo{Code: "AMS"}, ArrivalAirport: models.AirportInfo{Code: "HEL"}},
		},
	}
	if !routesThroughHub(flt, "AMS") {
		t.Error("should route through AMS")
	}
	// Does NOT route through AMS when AMS is final.
	flt2 := models.FlightResult{
		Legs: []models.FlightLeg{
			{DepartureAirport: models.AirportInfo{Code: "PRG"}, ArrivalAirport: models.AirportInfo{Code: "AMS"}},
		},
	}
	if routesThroughHub(flt2, "AMS") {
		t.Error("should not treat final dest as transit")
	}
}

func TestDetectHiddenCity_FlagsCheaperBeyond(t *testing.T) {
	// Mock: direct AMS→HEL = 200, but AMS→HEL→RIX = 130 (savings 70).
	search := func(ctx context.Context, origin, dest, date string) (*models.FlightSearchResult, error) {
		switch dest {
		case "HEL":
			return &models.FlightSearchResult{
				Success: true,
				Flights: []models.FlightResult{{
					Price: 200, Currency: "EUR",
					Legs: []models.FlightLeg{
						{DepartureAirport: models.AirportInfo{Code: "AMS"}, ArrivalAirport: models.AirportInfo{Code: "HEL"}},
					},
				}},
			}, nil
		case "RIX":
			return &models.FlightSearchResult{
				Success: true,
				Flights: []models.FlightResult{{
					Price: 130, Currency: "EUR",
					Legs: []models.FlightLeg{
						{DepartureAirport: models.AirportInfo{Code: "AMS"}, ArrivalAirport: models.AirportInfo{Code: "HEL"}},
						{DepartureAirport: models.AirportInfo{Code: "HEL"}, ArrivalAirport: models.AirportInfo{Code: "RIX"}},
					},
				}},
			}, nil
		}
		return &models.FlightSearchResult{Success: true, Flights: nil}, nil
	}

	cands, err := DetectHiddenCity(context.Background(), "AMS", "HEL", "2026-06-01", search, 30)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cands) == 0 {
		t.Fatal("expected at least one candidate")
	}
	c := cands[0]
	if c.BeyondDestination != "RIX" {
		t.Errorf("expected RIX, got %s", c.BeyondDestination)
	}
	if c.Savings != 70 {
		t.Errorf("expected savings 70, got %.0f", c.Savings)
	}
}

func TestDetectHiddenCity_NoSavingsIgnored(t *testing.T) {
	// Everything is 200 → no savings → no candidates.
	search := func(ctx context.Context, origin, dest, date string) (*models.FlightSearchResult, error) {
		return &models.FlightSearchResult{
			Success: true,
			Flights: []models.FlightResult{{
				Price: 200, Currency: "EUR",
				Legs: []models.FlightLeg{
					{DepartureAirport: models.AirportInfo{Code: "AMS"}, ArrivalAirport: models.AirportInfo{Code: "HEL"}},
					{DepartureAirport: models.AirportInfo{Code: "HEL"}, ArrivalAirport: models.AirportInfo{Code: dest}},
				},
			}},
		}, nil
	}
	cands, err := DetectHiddenCity(context.Background(), "AMS", "HEL", "2026-06-01", search, 30)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cands) != 0 {
		t.Errorf("expected 0 candidates (no savings), got %d", len(cands))
	}
}
