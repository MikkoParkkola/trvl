package mcp

import (
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/baggage"
	"github.com/MikkoParkkola/trvl/internal/hacks"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/weather"
)

func TestJoinStrings(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		parts []string
		sep   string
		want  string
	}{
		{name: "empty", parts: nil, sep: ", ", want: ""},
		{name: "single", parts: []string{"a"}, sep: ", ", want: "a"},
		{name: "multiple", parts: []string{"a", "b", "c"}, sep: ", ", want: "a, b, c"},
		{name: "pipe sep", parts: []string{"x", "y"}, sep: " | ", want: "x | y"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := joinStrings(tt.parts, tt.sep)
			if got != tt.want {
				t.Errorf("joinStrings(%v, %q) = %q, want %q", tt.parts, tt.sep, got, tt.want)
			}
		})
	}
}

func TestGroundRoutesHaveProvider(t *testing.T) {
	t.Parallel()
	routes := []models.GroundRoute{
		{Provider: "flixbus"},
		{Provider: "RegioJet"},
		{Provider: "trenitalia"},
	}

	tests := []struct {
		provider string
		want     bool
	}{
		{"flixbus", true},
		{"FlixBus", true}, // case insensitive
		{"regiojet", true},
		{"TRENITALIA", true},
		{"eurostar", false},
	}

	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			got := groundRoutesHaveProvider(routes, tt.provider)
			if got != tt.want {
				t.Errorf("groundRoutesHaveProvider(routes, %q) = %v, want %v", tt.provider, got, tt.want)
			}
		})
	}
}

func TestGroundRoutesHaveProvider_Empty(t *testing.T) {
	t.Parallel()
	got := groundRoutesHaveProvider(nil, "flixbus")
	if got {
		t.Error("should return false for nil routes")
	}
}

func TestBuildGroundRouteSummary(t *testing.T) {
	t.Parallel()
	routes := []models.GroundRoute{
		{
			Provider:  "FlixBus",
			Type:      "bus",
			Price:     25.00,
			Currency:  "EUR",
			Duration:  120,
			Transfers: 0,
			Departure: models.GroundStop{City: "Prague", Time: "2026-04-10T08:00"},
			Arrival:   models.GroundStop{City: "Vienna", Time: "2026-04-10T10:00"},
		},
		{
			Provider:  "RegioJet",
			Type:      "train",
			Price:     19.00,
			PriceMax:  29.00,
			Currency:  "EUR",
			Duration:  240,
			Transfers: 1,
			Departure: models.GroundStop{City: "Prague", Time: "2026-04-10T09:00"},
			Arrival:   models.GroundStop{City: "Vienna", Time: "2026-04-10T13:00"},
		},
	}

	got := buildGroundRouteSummary("Prague to Vienna", routes)
	if !strings.Contains(got, "Prague to Vienna") {
		t.Error("summary should contain header")
	}
	if !strings.Contains(got, "EUR 25.00") {
		t.Error("summary should contain price")
	}
	if !strings.Contains(got, "EUR 19.00-29.00") {
		t.Error("summary should contain price range")
	}
	if !strings.Contains(got, "direct") {
		t.Error("summary should show direct for 0 transfers")
	}
	if !strings.Contains(got, "1 transfers") {
		t.Error("summary should show transfer count")
	}
	if !strings.Contains(got, "08:00") {
		t.Error("summary should extract HH:MM from departure")
	}
}

func TestBuildGroundRouteSummary_ZeroPrice(t *testing.T) {
	t.Parallel()
	routes := []models.GroundRoute{
		{
			Provider:  "test",
			Price:     0,
			Duration:  60,
			Departure: models.GroundStop{City: "A", Time: "10:00"},
			Arrival:   models.GroundStop{City: "B", Time: "11:00"},
		},
	}
	got := buildGroundRouteSummary("test", routes)
	if !strings.Contains(got, "price unavailable") {
		t.Error("zero price should show 'price unavailable'")
	}
}

func TestBuildGroundRouteSummary_SeatsLeft(t *testing.T) {
	t.Parallel()
	seats := 3
	routes := []models.GroundRoute{
		{
			Provider:  "test",
			Price:     10,
			Currency:  "EUR",
			Duration:  60,
			SeatsLeft: &seats,
			Departure: models.GroundStop{City: "A", Time: "10:00"},
			Arrival:   models.GroundStop{City: "B", Time: "11:00"},
		},
	}
	got := buildGroundRouteSummary("test", routes)
	if !strings.Contains(got, "3 seats left") {
		t.Error("should show seats left when <= 10")
	}
}

func TestBuildGroundRouteSummary_Amenities(t *testing.T) {
	t.Parallel()
	routes := []models.GroundRoute{
		{
			Provider:  "test",
			Price:     10,
			Currency:  "EUR",
			Duration:  60,
			Amenities: []string{"wifi", "power outlet"},
			Departure: models.GroundStop{City: "A", Time: "10:00"},
			Arrival:   models.GroundStop{City: "B", Time: "11:00"},
		},
	}
	got := buildGroundRouteSummary("test", routes)
	if !strings.Contains(got, "wifi") {
		t.Error("should show amenities")
	}
}

func TestBuildGroundRouteSummary_TruncatesAt10(t *testing.T) {
	t.Parallel()
	routes := make([]models.GroundRoute, 15)
	for i := range routes {
		routes[i] = models.GroundRoute{
			Provider:  "test",
			Price:     float64(10 + i),
			Currency:  "EUR",
			Duration:  60,
			Departure: models.GroundStop{City: "A", Time: "10:00"},
			Arrival:   models.GroundStop{City: "B", Time: "11:00"},
		}
	}
	got := buildGroundRouteSummary("test", routes)
	if !strings.Contains(got, "5 more routes") {
		t.Error("should show overflow count for >10 routes")
	}
}

func TestBuildWeatherSummary_Success(t *testing.T) {
	t.Parallel()
	result := &weather.WeatherResult{
		Success: true,
		City:    "Prague",
		Forecasts: []weather.Forecast{
			{City: "Prague", Date: "2026-04-10", TempMax: 18, TempMin: 8, Precipitation: 0, Description: "Sunny"},
			{City: "Prague", Date: "2026-04-11", TempMax: 15, TempMin: 6, Precipitation: 5.2, Description: "Rain"},
		},
	}
	got := buildWeatherSummary(result)
	if !strings.Contains(got, "Prague") {
		t.Error("should contain city name")
	}
	if !strings.Contains(got, "Sunny") {
		t.Error("should contain weather description")
	}
	if !strings.Contains(got, "rain") {
		t.Error("should contain precipitation")
	}
}

func TestBuildWeatherSummary_Failure(t *testing.T) {
	t.Parallel()
	result := &weather.WeatherResult{
		Success: false,
		City:    "Nowhere",
		Error:   "geocode failed",
	}
	got := buildWeatherSummary(result)
	if !strings.Contains(got, "unavailable") {
		t.Error("failed result should say unavailable")
	}
	if !strings.Contains(got, "Nowhere") {
		t.Error("should contain city name")
	}
}

func TestBuildWeatherSummary_Empty(t *testing.T) {
	t.Parallel()
	result := &weather.WeatherResult{
		Success:   true,
		City:      "Empty",
		Forecasts: nil,
	}
	got := buildWeatherSummary(result)
	if !strings.Contains(got, "No forecast") {
		t.Error("empty forecasts should show 'No forecast'")
	}
}

func TestBuildAccomHacksSummary_NoHacks(t *testing.T) {
	t.Parallel()
	got := buildAccomHacksSummary("Prague", "2026-05-01", "2026-05-07", nil)
	if !strings.Contains(got, "No accommodation split") {
		t.Error("should indicate no opportunities found")
	}
	if !strings.Contains(got, "Prague") {
		t.Error("should contain city")
	}
}

func TestBuildBaggageSummaryOne(t *testing.T) {
	t.Parallel()
	ab := baggage.AirlineBaggage{
		Code:              "FR",
		Name:              "Ryanair",
		CarryOnMaxKg:      10,
		CarryOnDimensions: "55x40x20 cm",
		PersonalItem:      false,
		CheckedIncluded:   0,
		CheckedFee:        25,
		OverheadOnly:      true,
		Notes:             "Overhead cabin bag costs extra",
	}
	got := buildBaggageSummaryOne(ab)
	if !strings.Contains(got, "Ryanair") {
		t.Error("should contain airline name")
	}
	if !strings.Contains(got, "10 kg") {
		t.Error("should contain carry-on weight")
	}
	if !strings.Contains(got, "WARNING") {
		t.Error("should show overhead-only warning")
	}
	if !strings.Contains(got, "EUR 25") {
		t.Error("should show checked bag fee")
	}
}

func TestBuildBaggageSummaryOne_FullService(t *testing.T) {
	t.Parallel()
	ab := baggage.AirlineBaggage{
		Code:            "KL",
		Name:            "KLM",
		CarryOnMaxKg:    12,
		PersonalItem:    true,
		CheckedIncluded: 1,
	}
	got := buildBaggageSummaryOne(ab)
	if !strings.Contains(got, "1 included") {
		t.Error("should show checked bags included")
	}
	if !strings.Contains(got, "Personal item: yes") {
		t.Error("should show personal item")
	}
}

func TestBuildBaggageSummaryAll(t *testing.T) {
	t.Parallel()
	airlines := []baggage.AirlineBaggage{
		{Code: "KL", Name: "KLM", CarryOnMaxKg: 12, CheckedIncluded: 1},
		{Code: "FR", Name: "Ryanair", CarryOnMaxKg: 10, CheckedFee: 25, OverheadOnly: true},
	}
	got := buildBaggageSummaryAll(airlines)
	if !strings.Contains(got, "2 airlines") {
		t.Error("should show airline count")
	}
	if !strings.Contains(got, "KLM") {
		t.Error("should list KLM")
	}
	if !strings.Contains(got, "Ryanair") {
		t.Error("should list Ryanair")
	}
}

func TestBuildAccomHacksSummary_WithHacks(t *testing.T) {
	t.Parallel()
	detected := []hacks.Hack{
		{
			Title:       "Split stay: budget + premium",
			Description: "Stay 3 nights at Budget Inn then 4 nights at Grand Hotel",
			Savings:     120,
			Currency:    "EUR",
		},
		{
			Title:       "Midweek switch",
			Description: "Switch hotels midweek for better rates",
			Savings:     0,
			Currency:    "EUR",
		},
	}
	got := buildAccomHacksSummary("Prague", "2026-05-01", "2026-05-07", detected)
	if !strings.Contains(got, "Prague") {
		t.Error("should contain city")
	}
	if !strings.Contains(got, "saves EUR 120") {
		t.Error("should show savings for first hack")
	}
	if !strings.Contains(got, "Midweek switch") {
		t.Error("should contain second hack title")
	}
	if !strings.Contains(got, "1.") || !strings.Contains(got, "2.") {
		t.Error("should number the hacks")
	}
}
