package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/deals"
	"github.com/MikkoParkkola/trvl/internal/models"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	fn()
	_ = w.Close()

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	return buf.String()
}

// ---------------------------------------------------------------------------
// printFlightsTable
// ---------------------------------------------------------------------------

func TestPrintFlightsTable_Success(t *testing.T) {
	models.UseColor = false
	defer func() { models.UseColor = false }()

	result := &models.FlightSearchResult{
		Success:  true,
		Count:    2,
		TripType: "one_way",
		Flights: []models.FlightResult{
			{
				Price:    199,
				Currency: "EUR",
				Duration: 150,
				Stops:    0,
				Legs: []models.FlightLeg{
					{
						DepartureAirport: models.AirportInfo{Code: "HEL", Name: "Helsinki"},
						ArrivalAirport:   models.AirportInfo{Code: "AMS", Name: "Amsterdam"},
						DepartureTime:    "08:00",
						ArrivalTime:      "10:30",
						Airline:          "Finnair",
						FlightNumber:     "AY1571",
					},
				},
			},
			{
				Price:    299,
				Currency: "EUR",
				Duration: 195,
				Stops:    1,
				Legs: []models.FlightLeg{
					{
						DepartureAirport: models.AirportInfo{Code: "HEL", Name: "Helsinki"},
						ArrivalAirport:   models.AirportInfo{Code: "FRA", Name: "Frankfurt"},
						DepartureTime:    "09:00",
						ArrivalTime:      "11:00",
						Airline:          "Lufthansa",
						FlightNumber:     "LH1155",
					},
					{
						DepartureAirport: models.AirportInfo{Code: "FRA", Name: "Frankfurt"},
						ArrivalAirport:   models.AirportInfo{Code: "AMS", Name: "Amsterdam"},
						DepartureTime:    "12:00",
						ArrivalTime:      "13:15",
						Airline:          "Lufthansa",
						FlightNumber:     "LH992",
					},
				},
			},
		},
	}

	out := captureStdout(t, func() {
		err := printFlightsTable(context.Background(), "HEL", "AMS", "", result, false)
		if err != nil {
			t.Errorf("printFlightsTable returned error: %v", err)
		}
	})

	// Should contain banner, airline names, and prices.
	for _, want := range []string{"Flights", "Finnair", "EUR 199", "Lufthansa", "EUR 299", "HEL", "AMS"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

func TestPrintFlightsTable_NoFlights(t *testing.T) {
	models.UseColor = false

	result := &models.FlightSearchResult{
		Success:  true,
		Count:    0,
		TripType: "one_way",
	}

	out := captureStdout(t, func() {
		err := printFlightsTable(context.Background(), "HEL", "NRT", "", result, false)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	if !strings.Contains(out, "No flights found") {
		t.Errorf("expected 'No flights found' in output, got: %s", out)
	}
}

func TestPrintFlightsTable_Failed(t *testing.T) {
	models.UseColor = false

	result := &models.FlightSearchResult{
		Success: false,
		Error:   "timeout",
	}

	out := captureStdout(t, func() {
		// Also writes to stderr, but we just check no panic and it runs.
		_ = printFlightsTable(context.Background(), "HEL", "NRT", "", result, false)
	})
	// The error goes to stderr, stdout may be empty. Just verify no panic.
	_ = out
}

func TestPrintFlightsTable_WithSelfConnect(t *testing.T) {
	models.UseColor = false

	result := &models.FlightSearchResult{
		Success:  true,
		Count:    1,
		TripType: "one_way",
		Flights: []models.FlightResult{
			{
				Price:       249,
				Currency:    "EUR",
				Duration:    300,
				Stops:       1,
				SelfConnect: true,
				Legs: []models.FlightLeg{
					{
						DepartureAirport: models.AirportInfo{Code: "HEL"},
						ArrivalAirport:   models.AirportInfo{Code: "FRA"},
						DepartureTime:    "06:00",
						ArrivalTime:      "08:00",
						Airline:          "Finnair",
						FlightNumber:     "AY811",
					},
					{
						DepartureAirport: models.AirportInfo{Code: "FRA"},
						ArrivalAirport:   models.AirportInfo{Code: "BCN"},
						DepartureTime:    "10:00",
						ArrivalTime:      "12:30",
						Airline:          "Ryanair",
						FlightNumber:     "FR123",
					},
				},
			},
		},
	}

	out := captureStdout(t, func() {
		_ = printFlightsTable(context.Background(), "HEL", "BCN", "", result, false)
	})

	if !strings.Contains(out, "self-connect") {
		t.Errorf("expected 'self-connect' mention in output, got: %s", out)
	}
}

func TestPrintFlightsTable_WithProvider(t *testing.T) {
	models.UseColor = false

	result := &models.FlightSearchResult{
		Success:  true,
		Count:    1,
		TripType: "one_way",
		Flights: []models.FlightResult{
			{
				Price:    150,
				Currency: "EUR",
				Duration: 120,
				Stops:    0,
				Provider: "kiwi",
				Legs: []models.FlightLeg{
					{
						DepartureAirport: models.AirportInfo{Code: "HEL"},
						ArrivalAirport:   models.AirportInfo{Code: "AMS"},
						Airline:          "KLM",
						FlightNumber:     "KL1234",
					},
				},
			},
		},
	}

	out := captureStdout(t, func() {
		_ = printFlightsTable(context.Background(), "HEL", "AMS", "", result, false)
	})

	if !strings.Contains(out, "Kiwi") {
		t.Errorf("expected 'Kiwi' provider label in output")
	}
}

// ---------------------------------------------------------------------------
// formatHotelsTable
// ---------------------------------------------------------------------------

func TestFormatHotelsTable_Success(t *testing.T) {
	models.UseColor = false

	result := &models.HotelSearchResult{
		Success: true,
		Count:   2,
		Hotels: []models.HotelResult{
			{
				Name:        "Hotel Alpha",
				Stars:       4,
				Rating:      8.5,
				ReviewCount: 200,
				Price:       120,
				Currency:    "EUR",
				Amenities:   []string{"wifi", "pool"},
			},
			{
				Name:        "Hotel Beta",
				Stars:       3,
				Rating:      7.2,
				ReviewCount: 50,
				Price:       80,
				Currency:    "EUR",
				Amenities:   []string{"wifi"},
			},
		},
	}

	out := captureStdout(t, func() {
		err := formatHotelsTable(context.Background(), "", "", result, false)
		if err != nil {
			t.Errorf("formatHotelsTable returned error: %v", err)
		}
	})

	for _, want := range []string{"Hotels", "Hotel Alpha", "Hotel Beta", "120", "80", "wifi"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

func TestFormatHotelsTable_Empty(t *testing.T) {
	models.UseColor = false

	result := &models.HotelSearchResult{
		Success: true,
		Count:   0,
		Hotels:  nil,
	}

	out := captureStdout(t, func() {
		err := formatHotelsTable(context.Background(), "", "", result, false)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	if !strings.Contains(out, "No hotels found") {
		t.Errorf("expected 'No hotels found', got: %s", out)
	}
}

func TestFormatHotelsTable_WithSources(t *testing.T) {
	models.UseColor = false

	result := &models.HotelSearchResult{
		Success: true,
		Count:   1,
		Hotels: []models.HotelResult{
			{
				Name:     "Multi-Source Hotel",
				Price:    100,
				Currency: "EUR",
				Sources: []models.PriceSource{
					{Provider: "google_hotels", Price: 100, Currency: "EUR"},
					{Provider: "booking", Price: 105, Currency: "EUR"},
				},
			},
		},
	}

	out := captureStdout(t, func() {
		_ = formatHotelsTable(context.Background(), "", "", result, false)
	})

	// Should show Sources column since booking != Google.
	if !strings.Contains(out, "Booking") {
		t.Errorf("expected 'Booking' source label in output")
	}
}

func TestFormatHotelsTable_TotalAvailable(t *testing.T) {
	models.UseColor = false

	result := &models.HotelSearchResult{
		Success:        true,
		Count:          1,
		TotalAvailable: 50,
		Hotels: []models.HotelResult{
			{
				Name:     "Only Hotel",
				Price:    90,
				Currency: "EUR",
			},
		},
	}

	out := captureStdout(t, func() {
		_ = formatHotelsTable(context.Background(), "", "", result, false)
	})

	if !strings.Contains(out, "Showing 1 of 50") {
		t.Errorf("expected 'Showing 1 of 50' in output, got: %s", out)
	}
}

// ---------------------------------------------------------------------------
// printGroundTable
// ---------------------------------------------------------------------------

func TestPrintGroundTable_Success(t *testing.T) {
	models.UseColor = false

	seats := 3
	result := &models.GroundSearchResult{
		Success: true,
		Count:   2,
		Routes: []models.GroundRoute{
			{
				Provider:  "FlixBus",
				Type:      "bus",
				Price:     15.50,
				Currency:  "EUR",
				Duration:  240,
				Departure: models.GroundStop{City: "Prague", Time: "2026-07-01T08:00:00"},
				Arrival:   models.GroundStop{City: "Vienna", Time: "2026-07-01T12:00:00"},
				Transfers: 0,
				SeatsLeft: &seats,
			},
			{
				Provider:  "RegioJet",
				Type:      "bus",
				Price:     12.00,
				PriceMax:  18.00,
				Currency:  "EUR",
				Duration:  270,
				Departure: models.GroundStop{City: "Prague", Time: "2026-07-01T09:30:00"},
				Arrival:   models.GroundStop{City: "Vienna", Time: "2026-07-01T14:00:00"},
				Transfers: 1,
			},
		},
	}

	out := captureStdout(t, func() {
		err := printGroundTable(context.Background(), "", result)
		if err != nil {
			t.Errorf("printGroundTable returned error: %v", err)
		}
	})

	for _, want := range []string{"Ground Transport", "FlixBus", "RegioJet", "bus", "08:00", "12:00"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

func TestPrintGroundTable_NotSuccess(t *testing.T) {
	models.UseColor = false

	result := &models.GroundSearchResult{
		Success: false,
		Error:   "no routes available",
	}

	// Error goes to stderr; just verify no panic.
	captureStdout(t, func() {
		_ = printGroundTable(context.Background(), "", result)
	})
}

func TestPrintGroundTable_NotSuccessNoError(t *testing.T) {
	models.UseColor = false

	result := &models.GroundSearchResult{
		Success: false,
	}

	captureStdout(t, func() {
		_ = printGroundTable(context.Background(), "", result)
	})
}

// ---------------------------------------------------------------------------
// printDealsTable
// ---------------------------------------------------------------------------

func TestPrintDealsTable_Success(t *testing.T) {
	models.UseColor = false

	result := &deals.DealsResult{
		Success: true,
		Count:   2,
		Deals: []deals.Deal{
			{
				Title:       "Helsinki to Tokyo for EUR 299",
				Price:       299,
				Currency:    "EUR",
				Origin:      "HEL",
				Destination: "NRT",
				Type:        "deal",
				Source:      "secretflying",
				Published:   time.Now().Add(-2 * time.Hour),
			},
			{
				Title:       "Error Fare: London to NYC for GBP 150",
				Price:       150,
				Currency:    "GBP",
				Origin:      "LHR",
				Destination: "JFK",
				Type:        "error_fare",
				Source:      "fly4free",
				Published:   time.Now().Add(-30 * time.Minute),
			},
		},
	}

	out := captureStdout(t, func() {
		err := printDealsTable(context.Background(), "", result)
		if err != nil {
			t.Errorf("printDealsTable returned error: %v", err)
		}
	})

	for _, want := range []string{"Travel Deals", "EUR 299", "HEL -> NRT", "error_fare"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

func TestPrintDealsTable_Empty(t *testing.T) {
	models.UseColor = false

	result := &deals.DealsResult{
		Success: false,
		Error:   "feed unavailable",
	}

	captureStdout(t, func() {
		_ = printDealsTable(context.Background(), "", result)
	})
}

func TestPrintDealsTable_NoError(t *testing.T) {
	models.UseColor = false

	result := &deals.DealsResult{
		Success: false,
	}

	captureStdout(t, func() {
		_ = printDealsTable(context.Background(), "", result)
	})
}

func TestPrintDealsTable_DealWithNoRoute(t *testing.T) {
	models.UseColor = false

	result := &deals.DealsResult{
		Success: true,
		Count:   1,
		Deals: []deals.Deal{
			{
				Title:     "Great deal somewhere",
				Price:     0,
				Type:      "deal",
				Source:    "thepointsguy",
				Published: time.Now().Add(-48 * time.Hour),
			},
		},
	}

	out := captureStdout(t, func() {
		_ = printDealsTable(context.Background(), "", result)
	})

	// No route info — should show "-" for route and price.
	if !strings.Contains(out, "-") {
		t.Errorf("expected '-' for missing route/price")
	}
}

func TestPrintDealsTable_DealPartialRoute(t *testing.T) {
	models.UseColor = false

	result := &deals.DealsResult{
		Success: true,
		Count:   2,
		Deals: []deals.Deal{
			{
				Title:       "To Bali",
				Destination: "DPS",
				Type:        "deal",
				Source:      "secretflying",
				Published:   time.Now(),
			},
			{
				Title:     "From Helsinki",
				Origin:    "HEL",
				Type:      "deal",
				Source:    "secretflying",
				Published: time.Now(),
			},
		},
	}

	out := captureStdout(t, func() {
		_ = printDealsTable(context.Background(), "", result)
	})

	if !strings.Contains(out, "-> DPS") {
		t.Errorf("expected '-> DPS' for destination-only deal")
	}
	if !strings.Contains(out, "HEL -> ?") {
		t.Errorf("expected 'HEL -> ?' for origin-only deal")
	}
}

// ---------------------------------------------------------------------------
// printExploreTable
// ---------------------------------------------------------------------------

func TestPrintExploreTable_Success(t *testing.T) {
	models.UseColor = false

	result := &models.ExploreResult{
		Success: true,
		Count:   2,
		Destinations: []models.ExploreDestination{
			{
				CityName:    "Tallinn",
				Country:     "Estonia",
				AirportCode: "TLL",
				Price:       49,
				AirlineName: "Finnair",
				Stops:       0,
			},
			{
				CityName:    "Stockholm",
				Country:     "Sweden",
				AirportCode: "ARN",
				Price:       89,
				AirlineName: "SAS",
				Stops:       0,
			},
		},
	}

	out := captureStdout(t, func() {
		// Pass empty targetCurrency to skip conversion. The function calls
		// flights.DetectSourceCurrency which would need network; however for
		// the test we just let it fall back to EUR.
		err := printExploreTable(context.Background(), "", result, "HEL")
		if err != nil {
			t.Errorf("printExploreTable returned error: %v", err)
		}
	})

	for _, want := range []string{"Tallinn", "Estonia", "TLL", "Finnair", "Direct", "Stockholm"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

func TestPrintExploreTable_NoDestinations(t *testing.T) {
	models.UseColor = false

	result := &models.ExploreResult{
		Success: true,
		Count:   0,
	}

	out := captureStdout(t, func() {
		_ = printExploreTable(context.Background(), "", result, "HEL")
	})

	if !strings.Contains(out, "No destinations found") {
		t.Errorf("expected 'No destinations found'")
	}
}

func TestPrintExploreTable_Failed(t *testing.T) {
	models.UseColor = false

	result := &models.ExploreResult{
		Success: false,
		Error:   "some error",
	}

	captureStdout(t, func() {
		_ = printExploreTable(context.Background(), "", result, "HEL")
	})
}

// ---------------------------------------------------------------------------
// formatDestinationCard
// ---------------------------------------------------------------------------

func TestFormatDestinationCard_Full(t *testing.T) {
	models.UseColor = false

	info := &models.DestinationInfo{
		Location: "Tokyo, Japan",
		Timezone: "Asia/Tokyo",
		Country: models.CountryInfo{
			Name:       "Japan",
			Code:       "JP",
			Capital:    "Tokyo",
			Languages:  []string{"Japanese"},
			Currencies: []string{"JPY"},
			Region:     "Asia",
		},
		Weather: models.WeatherInfo{
			Forecast: []models.WeatherDay{
				{Date: "2026-06-15", TempHigh: 28, TempLow: 20, Precipitation: 5.2, Description: "Partly cloudy"},
				{Date: "2026-06-16", TempHigh: 30, TempLow: 22, Precipitation: 0, Description: "Sunny"},
			},
		},
		Holidays: []models.Holiday{
			{Date: "2026-06-15", Name: "Mountain Day", Type: "public"},
		},
		Safety: models.SafetyInfo{
			Level:       1.5,
			Advisory:    "exercise normal caution",
			Source:      "Travel Advisory",
			LastUpdated: "2026-01-01",
		},
		Currency: models.CurrencyInfo{
			LocalCurrency: "JPY",
			ExchangeRate:  160.5,
			BaseCurrency:  "EUR",
		},
	}

	out := captureStdout(t, func() {
		err := formatDestinationCard(info)
		if err != nil {
			t.Errorf("formatDestinationCard returned error: %v", err)
		}
	})

	for _, want := range []string{
		"Tokyo, Japan",
		"Asia/Tokyo",
		"COUNTRY",
		"Japan (JP)",
		"Capital: Tokyo",
		"Languages: Japanese",
		"Currencies: JPY",
		"WEATHER",
		"28 C",
		"HOLIDAYS",
		"Mountain Day",
		"SAFETY",
		"1.5/5.0",
		"CURRENCY",
		"JPY",
		"160.50",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
}
