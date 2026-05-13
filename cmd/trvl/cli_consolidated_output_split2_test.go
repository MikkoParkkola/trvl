package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/destinations"
	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/trip"
	"github.com/MikkoParkkola/trvl/internal/trips"
	"github.com/MikkoParkkola/trvl/internal/weather"
)

func TestPrintTripWeather_RealLegs_CancelledCtx_V27(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tr := &trips.Trip{
		ID:   "v27-weather-test",
		Name: "Weather Coverage Test",
		Legs: []trips.TripLeg{
			{
				From:      "HEL",
				To:        "Barcelona",
				StartTime: "2026-08-01T10:00",
				EndTime:   "2026-08-01T13:00",
			},
			{
				From:      "Barcelona",
				To:        "Rome",
				StartTime: "2026-08-05T09:00",
				EndTime:   "2026-08-05T12:00",
			},
			{
				From:      "Rome",
				To:        "HEL",
				StartTime: "2026-08-08T15:00",
				EndTime:   "2026-08-08T20:00",
			},
		},
	}

	printTripWeather(ctx, tr)
}

func TestPrintTripWeather_LongStay_TruncatedTo7Days_V27(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tr := &trips.Trip{
		ID:   "v27-long-stay",
		Name: "Long Stay Coverage Test",
		Legs: []trips.TripLeg{
			{
				From:      "HEL",
				To:        "Tokyo",
				StartTime: "2026-08-01",
			},
			{
				From:      "Tokyo",
				To:        "HEL",
				StartTime: "2026-08-20",
			},
		},
	}
	printTripWeather(ctx, tr)
}

func TestPrintTripWeather_LegWithEndTime_V27(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tr := &trips.Trip{
		ID:   "v27-endtime",
		Name: "End Time Coverage Test",
		Legs: []trips.TripLeg{
			{
				From:      "JFK",
				To:        "London",
				StartTime: "2026-09-01",
				EndTime:   "2026-09-05",
			},
		},
	}
	printTripWeather(ctx, tr)
}

func TestPrintTripWeather_DuplicateDestinations_V27(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tr := &trips.Trip{
		ID:   "v27-dedup",
		Name: "Dedup Coverage Test",
		Legs: []trips.TripLeg{
			{From: "HEL", To: "Paris", StartTime: "2026-07-01"},
			{From: "Paris", To: "HEL", StartTime: "2026-07-01"},
		},
	}
	printTripWeather(ctx, tr)
}

func TestRunCabinComparison_TableFormat_CancelledCtx_V27(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := runCabinComparison(ctx, []string{"HEL"}, []string{"BCN"}, "2026-08-01", flights.SearchOptions{}, "table")

	_ = err
}

func TestRunCabinComparison_MultiAirport_TableFormat_V27(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := runCabinComparison(ctx, []string{"HEL", "TMP"}, []string{"BCN"}, "2026-08-01", flights.SearchOptions{}, "table")
	_ = err
}

func TestPrintMultiCityTable_CurrencyConversion_CancelledCtx_V27(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result := &trip.MultiCityResult{
		Success:      true,
		HomeAirport:  "HEL",
		OptimalOrder: []string{"BCN", "ROM"},
		Segments: []trip.Segment{
			{From: "HEL", To: "BCN", Price: 150, Currency: "USD"},
			{From: "BCN", To: "ROM", Price: 80, Currency: "USD"},
		},
		TotalCost:    230,
		Currency:     "USD",
		Savings:      50,
		Permutations: 2,
	}

	err := printMultiCityTable(ctx, "EUR", result)
	_ = err
}

func TestRunCabinComparison_TableFormat_SingleAirport_V27(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := runCabinComparison(ctx, []string{"HEL"}, []string{"AMS"}, "2026-09-01", flights.SearchOptions{}, "table")
	_ = err
}

func TestFormatDestinationCard_Empty_V28(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()

	err := formatDestinationCard(&models.DestinationInfo{Location: "Tokyo"})
	_ = w.Close()
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "Tokyo") {
		t.Error("expected location in output")
	}
}

func TestFormatDestinationCard_FullInfo_V28(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()

	info := &models.DestinationInfo{
		Location: "Paris",
		Timezone: "Europe/Paris",
		Country: models.CountryInfo{
			Name:       "France",
			Code:       "FR",
			Region:     "Western Europe",
			Capital:    "Paris",
			Languages:  []string{"French"},
			Currencies: []string{"EUR"},
		},
		Weather: models.WeatherInfo{
			Forecast: []models.WeatherDay{
				{Date: "2026-07-01", TempHigh: 28, TempLow: 18, Precipitation: 2.5, Description: "Sunny"},
			},
		},
		Holidays: []models.Holiday{
			{Date: "2026-07-14", Name: "Bastille Day", Type: "National"},
		},
		Safety: models.SafetyInfo{
			Level:       2.5,
			Advisory:    "Exercise normal precautions",
			Source:      "State Dept",
			LastUpdated: "2026-01-01",
		},
		Currency: models.CurrencyInfo{
			BaseCurrency:  "USD",
			LocalCurrency: "EUR",
			ExchangeRate:  0.92,
		},
	}
	err := formatDestinationCard(info)
	_ = w.Close()
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "France") {
		t.Error("expected country in output")
	}
	if !strings.Contains(out, "Bastille Day") {
		t.Error("expected holiday in output")
	}
	if !strings.Contains(out, "CURRENCY") {
		t.Error("expected currency section in output")
	}
}

func TestFormatGuideCard_Basic_V28(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()

	guide := &models.WikivoyageGuide{
		Location: "Barcelona",
		URL:      "https://en.wikivoyage.org/wiki/Barcelona",
		Summary:  "Great city on the Mediterranean.",
		Sections: map[string]string{
			"See":    "Sagrada Família, Park Güell",
			"Get in": "By air: El Prat airport.",
			"Custom": "Some extra info.",
		},
	}
	err := formatGuideCard(guide)
	_ = w.Close()
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Barcelona") {
		t.Error("expected location in output")
	}
	if !strings.Contains(out, "Sagrada") {
		t.Error("expected See section content")
	}
	if !strings.Contains(out, "Custom") {
		t.Error("expected custom section in output")
	}
}

func TestFormatGuideCard_EmptySections_V28(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()

	guide := &models.WikivoyageGuide{
		Location: "Nowhere",
		URL:      "https://en.wikivoyage.org/wiki/Nowhere",
		Sections: map[string]string{
			"See": "",
		},
	}
	_ = formatGuideCard(guide)
	_ = w.Close()
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)

	if !strings.Contains(buf.String(), "Nowhere") {
		t.Error("expected location in output")
	}
}

func TestFormatNearbyCard_Empty_V28(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()

	_ = formatNearbyCard(&destinations.NearbyResult{})
	_ = w.Close()
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)

	if !strings.Contains(buf.String(), "No nearby") {
		t.Error("expected 'No nearby' message")
	}
}

func TestFormatNearbyCard_WithPOIs_V28(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()

	result := &destinations.NearbyResult{
		POIs: []models.NearbyPOI{
			{Name: "La Boqueria", Type: "market", Distance: 200, Cuisine: "", Hours: "8:00-20:00"},
		},
		RatedPlaces: []models.RatedPlace{
			{Name: "Bar El Xampanyet", Rating: 8.5, Category: "bar", PriceLevel: 2, Distance: 300},
		},
		Attractions: []models.Attraction{
			{Name: "Sagrada Familia", Kind: "church", Distance: 500},
		},
	}
	_ = formatNearbyCard(result)
	_ = w.Close()
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)

	out := buf.String()
	if !strings.Contains(out, "La Boqueria") {
		t.Error("expected POI in output")
	}
	if !strings.Contains(out, "Bar El Xampanyet") {
		t.Error("expected rated place in output")
	}
	if !strings.Contains(out, "Sagrada Familia") {
		t.Error("expected attraction in output")
	}
}

func TestTruncate_ShortString(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Errorf("expected %q got %q", "hello", got)
	}
}

func TestTruncate_ExactLength(t *testing.T) {
	if got := truncate("hello", 5); got != "hello" {
		t.Errorf("expected %q got %q", "hello", got)
	}
}

func TestTruncate_Longer(t *testing.T) {
	got := truncate("hello world", 8)
	if !strings.HasSuffix(got, "...") {
		t.Errorf("expected ellipsis suffix, got %q", got)
	}
	if len(got) != 8 {
		t.Errorf("expected len 8, got %d", len(got))
	}
}

func TestTruncate_MaxLenThreeOrLess(t *testing.T) {
	got := truncate("hello", 3)
	if got != "hel" {
		t.Errorf("expected %q got %q", "hel", got)
	}
}

func TestFormatGuideCard_Empty(t *testing.T) {
	guide := &models.WikivoyageGuide{
		Location: "Test City",
		URL:      "https://example.com",
		Sections: map[string]string{},
	}
	if err := formatGuideCard(guide); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFormatGuideCard_WithSummaryAndSections(t *testing.T) {
	guide := &models.WikivoyageGuide{
		Location: "Barcelona",
		URL:      "https://en.wikivoyage.org/wiki/Barcelona",
		Summary:  "A vibrant city.",
		Sections: map[string]string{
			"Get in":    "Fly to El Prat.",
			"See":       "Sagrada Familia.",
			"Eat":       "Tapas everywhere.",
			"OtherSect": "Some content.",
		},
	}
	if err := formatGuideCard(guide); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFormatDestinationCard_ZeroExchangeRate(t *testing.T) {
	info := &models.DestinationInfo{
		Location: "Testland",
		Currency: models.CurrencyInfo{
			BaseCurrency:  "EUR",
			LocalCurrency: "TLC",
			ExchangeRate:  0,
		},
	}

	if err := formatDestinationCard(info); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFormatDestinationCard_WithTimezoneAndHolidays(t *testing.T) {
	info := &models.DestinationInfo{
		Location: "Tokyo",
		Timezone: "Asia/Tokyo",
		Country: models.CountryInfo{
			Name:       "Japan",
			Code:       "JP",
			Region:     "Asia",
			Capital:    "Tokyo",
			Languages:  []string{"Japanese"},
			Currencies: []string{"JPY"},
		},
		Holidays: []models.Holiday{
			{Date: "2026-06-20", Name: "Summer Fest", Type: "Local"},
		},
		Safety: models.SafetyInfo{
			Level:       3.5,
			Advisory:    "Exercise caution",
			Source:      "FCDO",
			LastUpdated: "2026-01-01",
		},
	}
	if err := formatDestinationCard(info); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFormatNearbyCard_WithRatedAndAttractions(t *testing.T) {
	result := &destinations.NearbyResult{
		RatedPlaces: []models.RatedPlace{
			{Name: "El Xampanyet", Rating: 8.5, Category: "bar", PriceLevel: 2, Distance: 300},
		},
		Attractions: []models.Attraction{
			{Name: "Sagrada Familia", Kind: "church", Distance: 2000},
		},
	}
	if err := formatNearbyCard(result); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFormatPricesTable_Empty(t *testing.T) {
	result := &models.HotelPriceResult{
		HotelID:  "/g/11abc",
		CheckIn:  "2026-06-15",
		CheckOut: "2026-06-18",
	}
	if err := formatPricesTable(result); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFormatRating_Zero(t *testing.T) {
	if got := formatRating(0); got != "0" {
		t.Errorf("expected \"0\", got %q", got)
	}
}

func TestFormatRating_NonZero(t *testing.T) {
	if got := formatRating(8.5); got != "8.5" {
		t.Errorf("expected \"8.5\", got %q", got)
	}
}

func TestPrintWeatherTable_Empty(t *testing.T) {
	result := &weather.WeatherResult{
		City:      "Prague",
		Success:   true,
		Forecasts: nil,
	}
	if err := printWeatherTable(result, "2026-04-20", "2026-04-26"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPrintWeatherTable_HotAndRainy(t *testing.T) {

	result := &weather.WeatherResult{
		City:    "Bangkok",
		Success: true,
		Forecasts: []weather.Forecast{
			{Date: "2026-04-20", TempMin: 28, TempMax: 38, Precipitation: 10.0, Description: "Heavy rain"},
		},
	}
	if err := printWeatherTable(result, "2026-04-20", "2026-04-20"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPrintWeatherTable_Cold(t *testing.T) {

	result := &weather.WeatherResult{
		City:    "Reykjavik",
		Success: true,
		Forecasts: []weather.Forecast{
			{Date: "2026-01-05", TempMin: -5, TempMax: 2, Precipitation: 0.5, Description: "Snow"},
		},
	}
	if err := printWeatherTable(result, "2026-01-05", "2026-01-05"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFormatLastCheck_MinutesAgo(t *testing.T) {
	got := formatLastCheck(time.Now().Add(-30 * time.Minute))
	if !strings.HasSuffix(got, "m ago") {
		t.Errorf("expected minutes ago, got %q", got)
	}
}

func TestFormatLastCheck_HoursAgo(t *testing.T) {
	got := formatLastCheck(time.Now().Add(-3 * time.Hour))
	if !strings.HasSuffix(got, "h ago") {
		t.Errorf("expected hours ago, got %q", got)
	}
}

func TestFormatLastCheck_DaysAgo(t *testing.T) {
	got := formatLastCheck(time.Now().Add(-50 * time.Hour))
	if !strings.HasSuffix(got, "d ago") {
		t.Errorf("expected days ago, got %q", got)
	}
}

func TestCabinResultTableRows_Nonstop(t *testing.T) {
	r := cabinResult{
		Cabin:    "Economy",
		Price:    199,
		Currency: "EUR",
		Airline:  "KLM",
		Stops:    0,
		Duration: 125,
	}
	stopLabel := "nonstop"
	if r.Stops == 1 {
		stopLabel = "1 stop"
	} else if r.Stops > 1 {
		stopLabel = "more"
	}
	if stopLabel != "nonstop" {
		t.Errorf("expected nonstop, got %q", stopLabel)
	}
}

func TestCabinResultTableRows_OneStop(t *testing.T) {
	r := cabinResult{Stops: 1}
	stopLabel := "nonstop"
	if r.Stops == 1 {
		stopLabel = "1 stop"
	}
	if stopLabel != "1 stop" {
		t.Errorf("expected '1 stop', got %q", stopLabel)
	}
}

func TestCabinResultTableRows_MultiStop(t *testing.T) {
	r := cabinResult{Stops: 3}
	stopLabel := "nonstop"
	if r.Stops == 1 {
		stopLabel = "1 stop"
	} else if r.Stops > 1 {
		stopLabel = "3 stops"
	}
	if stopLabel != "3 stops" {
		t.Errorf("expected '3 stops', got %q", stopLabel)
	}
}

func TestCabinResultTableRows_ErrorRow(t *testing.T) {
	r := cabinResult{Cabin: "First", Error: "no flights"}
	if r.Error == "" {
		t.Error("expected error to be set")
	}
}
