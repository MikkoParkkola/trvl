package main

import (
	"context"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/destinations"
	"github.com/MikkoParkkola/trvl/internal/hacks"
	"github.com/MikkoParkkola/trvl/internal/models"
)

func TestFormatDestinationCard_Minimal(t *testing.T) {
	models.UseColor = false

	info := &models.DestinationInfo{
		Location: "Unknown City",
	}

	out := captureStdout(t, func() {
		err := formatDestinationCard(info)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	if !strings.Contains(out, "Unknown City") {
		t.Errorf("expected location name in output")
	}
}

// ---------------------------------------------------------------------------
// printDatesTable
// ---------------------------------------------------------------------------

func TestPrintDatesTable_Success(t *testing.T) {
	models.UseColor = false

	result := &models.DateSearchResult{
		Success:   true,
		Count:     3,
		TripType:  "one_way",
		DateRange: "2026-06-01 to 2026-06-03",
		Dates: []models.DatePriceResult{
			{Date: "2026-06-01", Price: 199, Currency: "EUR"},
			{Date: "2026-06-02", Price: 179, Currency: "EUR"},
			{Date: "2026-06-03", Price: 220, Currency: "EUR"},
		},
	}

	out := captureStdout(t, func() {
		err := printDatesTable(context.Background(), "", result)
		if err != nil {
			t.Errorf("printDatesTable returned error: %v", err)
		}
	})

	for _, want := range []string{"2026-06-01", "EUR 199", "EUR 179", "EUR 220", "3 dates"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

func TestPrintDatesTable_RoundTrip(t *testing.T) {
	models.UseColor = false

	result := &models.DateSearchResult{
		Success:   true,
		Count:     2,
		TripType:  "round_trip",
		DateRange: "2026-06-01 to 2026-06-02",
		Dates: []models.DatePriceResult{
			{Date: "2026-06-01", Price: 399, Currency: "EUR", ReturnDate: "2026-06-08"},
			{Date: "2026-06-02", Price: 410, Currency: "EUR", ReturnDate: "2026-06-09"},
		},
	}

	out := captureStdout(t, func() {
		_ = printDatesTable(context.Background(), "", result)
	})

	// Round-trip should show Return column.
	if !strings.Contains(out, "Return") {
		t.Errorf("expected 'Return' column header for round-trip")
	}
	if !strings.Contains(out, "2026-06-08") {
		t.Errorf("expected return date in output")
	}
}

func TestPrintDatesTable_Empty(t *testing.T) {
	models.UseColor = false

	result := &models.DateSearchResult{
		Success: true,
		Count:   0,
	}

	out := captureStdout(t, func() {
		_ = printDatesTable(context.Background(), "", result)
	})

	if !strings.Contains(out, "No prices found") {
		t.Errorf("expected 'No prices found'")
	}
}

func TestPrintDatesTable_Failed(t *testing.T) {
	models.UseColor = false

	result := &models.DateSearchResult{
		Success: false,
		Error:   "bad request",
	}

	captureStdout(t, func() {
		_ = printDatesTable(context.Background(), "", result)
	})
}

// ---------------------------------------------------------------------------
// printHacksTable / printHack
// ---------------------------------------------------------------------------

func TestPrintHacksTable_WithHacks(t *testing.T) {
	models.UseColor = false

	detected := []hacks.Hack{
		{
			Type:        "hidden_city",
			Title:       "Hidden City Ticketing",
			Description: "Book a flight through AMS and deplane there.",
			Savings:     50,
			Currency:    "EUR",
			Steps:       []string{"Book HEL-AMS-CDG", "Exit at AMS"},
			Risks:       []string{"Airline may penalize"},
			Citations:   []string{"https://example.com"},
		},
		{
			Type:        "date_flex",
			Title:       "Date Flexibility",
			Description: "Fly 2 days earlier for cheaper fare.",
			Savings:     30,
			Currency:    "EUR",
			Steps:       []string{"Change departure to Tuesday"},
		},
	}

	out := captureStdout(t, func() {
		err := printHacksTable("HEL", "AMS", "2026-04-15", 200, "EUR", detected, true)
		if err != nil {
			t.Errorf("printHacksTable returned error: %v", err)
		}
	})

	for _, want := range []string{
		"Travel Hacks",
		"HEL",
		"AMS",
		"Baseline: EUR 200",
		"Hidden City Ticketing",
		"saves EUR 50",
		"Date Flexibility",
		"Book HEL-AMS-CDG",
		"Airline may penalize",
		"example.com",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

func TestPrintHacksTable_NoHacks(t *testing.T) {
	models.UseColor = false

	out := captureStdout(t, func() {
		_ = printHacksTable("HEL", "AMS", "2026-04-15", 0, "EUR", nil, true)
	})

	if !strings.Contains(out, "No hacks detected") {
		t.Errorf("expected 'No hacks detected'")
	}
}

func TestPrintHacksTable_NoBaseline(t *testing.T) {
	models.UseColor = false

	detected := []hacks.Hack{
		{
			Type:        "split",
			Title:       "Split Ticketing",
			Description: "Book two separate tickets.",
			Savings:     0,
		},
	}

	out := captureStdout(t, func() {
		_ = printHacksTable("HEL", "AMS", "2026-04-15", 0, "EUR", detected, true)
	})

	// Without baseline, should not show "Baseline:" line.
	if strings.Contains(out, "Baseline:") {
		t.Errorf("should not show Baseline when naivePrice is 0")
	}
}

func TestPrintHack_NoCurrency(t *testing.T) {
	models.UseColor = false

	h := hacks.Hack{
		Type:        "throwaway",
		Title:       "Throwaway Ticketing",
		Description: "Buy a round-trip and skip the return.",
		Savings:     100,
		Currency:    "", // should default to EUR
	}

	out := captureStdout(t, func() {
		printHack(1, h)
	})

	if !strings.Contains(out, "EUR 100") {
		t.Errorf("expected default EUR currency, got: %s", out)
	}
}

// ---------------------------------------------------------------------------
// formatEventsCard
// ---------------------------------------------------------------------------

func TestFormatEventsCard_WithEvents(t *testing.T) {
	models.UseColor = false

	events := []models.Event{
		{
			Name:       "Rock Concert",
			Date:       "2026-07-01",
			Time:       "20:00",
			Venue:      "Palau Sant Jordi",
			Type:       "Music",
			PriceRange: "EUR 50-150",
		},
		{
			Name:  "Art Exhibition",
			Date:  "2026-07-02",
			Venue: "MACBA",
			Type:  "Arts",
		},
	}

	out := captureStdout(t, func() {
		err := formatEventsCard(events, "Barcelona", "2026-07-01", "2026-07-08")
		if err != nil {
			t.Errorf("formatEventsCard returned error: %v", err)
		}
	})

	for _, want := range []string{"EVENTS IN Barcelona", "2 events", "Rock Concert", "Palau Sant Jordi", "Music", "Art Exhibition"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

func TestFormatEventsCard_NoEvents(t *testing.T) {
	models.UseColor = false

	out := captureStdout(t, func() {
		_ = formatEventsCard(nil, "Helsinki", "2026-07-01", "2026-07-08")
	})

	if !strings.Contains(out, "No events found") {
		t.Errorf("expected 'No events found'")
	}
}

// ---------------------------------------------------------------------------
// printGridTable
// ---------------------------------------------------------------------------

func TestPrintGridTable_Success(t *testing.T) {
	models.UseColor = false

	result := &models.PriceGrid{
		Success:        true,
		Count:          4,
		DepartureDates: []string{"2026-07-01", "2026-07-02"},
		ReturnDates:    []string{"2026-07-08", "2026-07-09"},
		Cells: []models.GridCell{
			{DepartureDate: "2026-07-01", ReturnDate: "2026-07-08", Price: 450, Currency: "EUR"},
			{DepartureDate: "2026-07-01", ReturnDate: "2026-07-09", Price: 480, Currency: "EUR"},
			{DepartureDate: "2026-07-02", ReturnDate: "2026-07-08", Price: 420, Currency: "EUR"},
			{DepartureDate: "2026-07-02", ReturnDate: "2026-07-09", Price: 460, Currency: "EUR"},
		},
	}

	out := captureStdout(t, func() {
		err := printGridTable(context.Background(), "", result, "HEL", "NRT")
		if err != nil {
			t.Errorf("printGridTable returned error: %v", err)
		}
	})

	for _, want := range []string{"Price grid", "HEL", "NRT", "4 combinations", "EUR 450", "EUR 420"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

func TestPrintGridTable_Empty(t *testing.T) {
	models.UseColor = false

	result := &models.PriceGrid{
		Success: true,
		Count:   0,
	}

	out := captureStdout(t, func() {
		_ = printGridTable(context.Background(), "", result, "HEL", "NRT")
	})

	if !strings.Contains(out, "No price data found") {
		t.Errorf("expected 'No price data found'")
	}
}

func TestPrintGridTable_Failed(t *testing.T) {
	models.UseColor = false

	result := &models.PriceGrid{
		Success: false,
		Error:   "failed",
	}

	captureStdout(t, func() {
		_ = printGridTable(context.Background(), "", result, "HEL", "NRT")
	})
}

func TestPrintGridTable_MissingCell(t *testing.T) {
	models.UseColor = false

	result := &models.PriceGrid{
		Success:        true,
		Count:          1,
		DepartureDates: []string{"2026-07-01"},
		ReturnDates:    []string{"2026-07-08", "2026-07-09"},
		Cells: []models.GridCell{
			{DepartureDate: "2026-07-01", ReturnDate: "2026-07-08", Price: 350, Currency: "EUR"},
			// 2026-07-09 intentionally missing
		},
	}

	out := captureStdout(t, func() {
		_ = printGridTable(context.Background(), "", result, "HEL", "BCN")
	})

	// Missing cell should render as "-"
	if !strings.Contains(out, "-") {
		t.Errorf("expected '-' for missing grid cell")
	}
}

// ---------------------------------------------------------------------------
// formatGuideCard
// ---------------------------------------------------------------------------

func TestFormatGuideCard_Full(t *testing.T) {
	models.UseColor = false

	guide := &models.WikivoyageGuide{
		Location: "Barcelona",
		Summary:  "Barcelona is a vibrant Mediterranean city.",
		URL:      "https://en.wikivoyage.org/wiki/Barcelona",
		Sections: map[string]string{
			"See":        "Visit La Sagrada Familia and Park Guell.",
			"Eat":        "Try tapas at La Boqueria.",
			"Get in":     "Fly to El Prat airport.",
			"Stay safe":  "Watch for pickpockets on Las Ramblas.",
			"Extra Info": "Some custom section.",
		},
	}

	out := captureStdout(t, func() {
		err := formatGuideCard(guide)
		if err != nil {
			t.Errorf("formatGuideCard returned error: %v", err)
		}
	})

	for _, want := range []string{
		"Barcelona",
		"wikivoyage.org",
		"OVERVIEW",
		"vibrant Mediterranean",
		"See",
		"La Sagrada Familia",
		"Eat",
		"tapas",
		"Get in",
		"El Prat",
		"Stay safe",
		"pickpockets",
		"Extra Info",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

func TestFormatGuideCard_Minimal(t *testing.T) {
	models.UseColor = false

	guide := &models.WikivoyageGuide{
		Location: "Nowhere",
		URL:      "https://en.wikivoyage.org/wiki/Nowhere",
		Sections: map[string]string{},
	}

	out := captureStdout(t, func() {
		_ = formatGuideCard(guide)
	})

	if !strings.Contains(out, "Nowhere") {
		t.Errorf("expected location name in output")
	}
}

// ---------------------------------------------------------------------------
// formatNearbyCard
// ---------------------------------------------------------------------------

func TestFormatNearbyCard_WithPOIs(t *testing.T) {
	models.UseColor = false

	result := &destinations.NearbyResult{
		POIs: []models.NearbyPOI{
			{Name: "Cafe Helsinki", Type: "cafe", Distance: 150, Cuisine: "Finnish", Hours: "08:00-20:00"},
			{Name: "Sushi Bar", Type: "restaurant", Distance: 300, Cuisine: "Japanese"},
		},
	}

	out := captureStdout(t, func() {
		err := formatNearbyCard(result)
		if err != nil {
			t.Errorf("formatNearbyCard returned error: %v", err)
		}
	})

	for _, want := range []string{"NEARBY PLACES", "2 found", "Cafe Helsinki", "cafe", "150m", "Finnish", "Sushi Bar"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

func TestFormatNearbyCard_WithRatedPlaces(t *testing.T) {
	models.UseColor = false

	result := &destinations.NearbyResult{
		POIs: []models.NearbyPOI{
			{Name: "Some Cafe", Type: "cafe", Distance: 100},
		},
		RatedPlaces: []models.RatedPlace{
			{Name: "Top Restaurant", Rating: 9.2, Category: "Italian", PriceLevel: 3, Distance: 200},
		},
	}

	out := captureStdout(t, func() {
		_ = formatNearbyCard(result)
	})

	for _, want := range []string{"TOP RATED", "Top Restaurant", "9.2/10", "Italian", "$$$"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

func TestFormatNearbyCard_WithAttractions(t *testing.T) {
	models.UseColor = false

	result := &destinations.NearbyResult{
		Attractions: []models.Attraction{
			{Name: "Helsinki Cathedral", Kind: "church", Distance: 500},
		},
	}

	out := captureStdout(t, func() {
		_ = formatNearbyCard(result)
	})

	for _, want := range []string{"ATTRACTIONS", "Helsinki Cathedral", "church", "500m"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

func TestFormatNearbyCard_Empty(t *testing.T) {
	models.UseColor = false

	result := &destinations.NearbyResult{}

	out := captureStdout(t, func() {
		_ = formatNearbyCard(result)
	})

	if !strings.Contains(out, "No nearby places found") {
		t.Errorf("expected 'No nearby places found'")
	}
}
