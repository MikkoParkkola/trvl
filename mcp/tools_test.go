package mcp

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

func TestArgString_NilArgs(t *testing.T) {
	t.Parallel()
	got := argString(nil, "key")
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestArgString_MissingKey(t *testing.T) {
	t.Parallel()
	got := argString(map[string]any{"other": "val"}, "key")
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestArgString_NonStringValue(t *testing.T) {
	t.Parallel()
	got := argString(map[string]any{"key": 42}, "key")
	if got != "" {
		t.Errorf("expected empty for non-string, got %q", got)
	}
}

func TestArgString_BoolValue(t *testing.T) {
	t.Parallel()
	got := argString(map[string]any{"key": true}, "key")
	if got != "" {
		t.Errorf("expected empty for bool, got %q", got)
	}
}

func TestArgString_ValidString(t *testing.T) {
	t.Parallel()
	got := argString(map[string]any{"key": "hello"}, "key")
	if got != "hello" {
		t.Errorf("got %q, want hello", got)
	}
}

func TestArgString_EmptyString(t *testing.T) {
	t.Parallel()
	got := argString(map[string]any{"key": ""}, "key")
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestArgString_NilValue(t *testing.T) {
	t.Parallel()
	got := argString(map[string]any{"key": nil}, "key")
	if got != "" {
		t.Errorf("expected empty for nil value, got %q", got)
	}
}

// --- argInt ---

func TestArgInt_NilArgs(t *testing.T) {
	t.Parallel()
	got := argInt(nil, "key", 42)
	if got != 42 {
		t.Errorf("expected 42, got %d", got)
	}
}

func TestArgInt_MissingKey(t *testing.T) {
	t.Parallel()
	got := argInt(map[string]any{}, "key", 10)
	if got != 10 {
		t.Errorf("expected 10, got %d", got)
	}
}

func TestArgInt_Float64Value(t *testing.T) {
	t.Parallel()
	got := argInt(map[string]any{"key": float64(7)}, "key", 0)
	if got != 7 {
		t.Errorf("expected 7, got %d", got)
	}
}

func TestArgInt_IntValue(t *testing.T) {
	t.Parallel()
	got := argInt(map[string]any{"key": 5}, "key", 0)
	if got != 5 {
		t.Errorf("expected 5, got %d", got)
	}
}

func TestArgInt_JSONNumber(t *testing.T) {
	t.Parallel()
	got := argInt(map[string]any{"key": json.Number("99")}, "key", 0)
	if got != 99 {
		t.Errorf("expected 99, got %d", got)
	}
}

func TestArgInt_JSONNumberInvalid(t *testing.T) {
	t.Parallel()
	got := argInt(map[string]any{"key": json.Number("not-a-number")}, "key", 42)
	if got != 42 {
		t.Errorf("expected default 42, got %d", got)
	}
}

func TestArgInt_StringValue(t *testing.T) {
	t.Parallel()
	got := argInt(map[string]any{"key": "not a number"}, "key", 42)
	if got != 42 {
		t.Errorf("expected default 42, got %d", got)
	}
}

func TestArgInt_NilValue(t *testing.T) {
	t.Parallel()
	got := argInt(map[string]any{"key": nil}, "key", 42)
	if got != 42 {
		t.Errorf("expected default 42, got %d", got)
	}
}

// --- argBool ---

func TestArgBool_NilArgs(t *testing.T) {
	t.Parallel()
	got := argBool(nil, "key", true)
	if !got {
		t.Error("expected true default")
	}
}

func TestArgBool_MissingKey(t *testing.T) {
	t.Parallel()
	got := argBool(map[string]any{}, "key", false)
	if got {
		t.Error("expected false default")
	}
}

func TestArgBool_TrueValue(t *testing.T) {
	t.Parallel()
	got := argBool(map[string]any{"key": true}, "key", false)
	if !got {
		t.Error("expected true")
	}
}

func TestArgBool_FalseValue(t *testing.T) {
	t.Parallel()
	got := argBool(map[string]any{"key": false}, "key", true)
	if got {
		t.Error("expected false")
	}
}

func TestArgBool_NonBoolValue(t *testing.T) {
	t.Parallel()
	got := argBool(map[string]any{"key": "true"}, "key", false)
	if got {
		t.Error("expected default false for non-bool value")
	}
}

func TestArgBool_IntValue(t *testing.T) {
	t.Parallel()
	got := argBool(map[string]any{"key": 1}, "key", false)
	if got {
		t.Error("expected default false for int value")
	}
}

// --- handleDestinationInfo ---

func TestHandleDestinationInfo_MissingLocation(t *testing.T) {
	t.Parallel()
	_, _, err := handleDestinationInfo(context.Background(), map[string]any{}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for missing location")
	}
}

func TestHandleDestinationInfo_NilArgs(t *testing.T) {
	t.Parallel()
	_, _, err := handleDestinationInfo(context.Background(), nil, nil, nil, nil)
	if err == nil {
		t.Error("expected error for nil args")
	}
}

func TestHandleDestinationInfo_EmptyLocation(t *testing.T) {
	t.Parallel()
	_, _, err := handleDestinationInfo(context.Background(), map[string]any{"location": ""}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for empty location")
	}
}

// --- destinationSummary ---

func TestDestinationSummary_MinimalInfo(t *testing.T) {
	t.Parallel()
	info := &models.DestinationInfo{Location: "Helsinki"}
	summary := destinationSummary(info)
	if !strings.Contains(summary, "Helsinki") {
		t.Error("summary should contain location name")
	}
}

func TestDestinationSummary_FullInfo(t *testing.T) {
	t.Parallel()
	info := &models.DestinationInfo{
		Location: "Tokyo",
		Country:  models.CountryInfo{Name: "Japan", Region: "Asia"},
		Weather: models.WeatherInfo{
			Current: models.WeatherDay{
				Date:        "2026-06-15",
				Description: "Sunny",
				TempHigh:    30,
				TempLow:     22,
			},
		},
		Safety:   models.SafetyInfo{Level: 1.5, Advisory: "Exercise normal caution", Source: "Travel Advisory"},
		Currency: models.CurrencyInfo{LocalCurrency: "JPY", ExchangeRate: 165.50},
		Holidays: []models.Holiday{{Name: "Test1"}, {Name: "Test2"}},
	}
	summary := destinationSummary(info)

	if !strings.Contains(summary, "Tokyo") {
		t.Error("summary should contain location")
	}
	if !strings.Contains(summary, "Japan") {
		t.Error("summary should contain country")
	}
	if !strings.Contains(summary, "Sunny") {
		t.Error("summary should contain weather")
	}
	if !strings.Contains(summary, "1.5") {
		t.Error("summary should contain safety level")
	}
	if !strings.Contains(summary, "JPY") {
		t.Error("summary should contain currency")
	}
	if !strings.Contains(summary, "holidays") {
		t.Error("summary should mention holidays")
	}
}

func TestDestinationSummary_NoCurrency(t *testing.T) {
	t.Parallel()
	info := &models.DestinationInfo{Location: "Unknown"}
	summary := destinationSummary(info)
	if strings.Contains(summary, "Currency:") {
		t.Error("summary should not contain currency when no rate")
	}
}

func TestDestinationSummary_NoWeather(t *testing.T) {
	t.Parallel()
	info := &models.DestinationInfo{
		Location: "Unknown",
		Country:  models.CountryInfo{Name: "Test", Region: "Test"},
	}
	summary := destinationSummary(info)
	if strings.Contains(summary, "Today:") {
		t.Error("summary should not contain weather when no current date")
	}
}

func TestDestinationSummary_NoSafety(t *testing.T) {
	t.Parallel()
	info := &models.DestinationInfo{Location: "Unknown"}
	summary := destinationSummary(info)
	if strings.Contains(summary, "Safety:") {
		t.Error("summary should not contain safety when no source")
	}
}

// --- flightSummary ---

func TestFlightSummary_NoResults(t *testing.T) {
	t.Parallel()
	result := &models.FlightSearchResult{Success: true, Count: 0}
	summary := flightSummary(result, "HEL", "NRT")
	if !strings.Contains(summary, "No flights found") {
		t.Errorf("summary = %q, want 'No flights found'", summary)
	}
}

func TestFlightSummary_WithError(t *testing.T) {
	t.Parallel()
	result := &models.FlightSearchResult{Success: false, Error: "blocked"}
	summary := flightSummary(result, "HEL", "NRT")
	if !strings.Contains(summary, "blocked") {
		t.Errorf("summary = %q, want to contain 'blocked'", summary)
	}
}

func TestFlightSummary_WithFlights(t *testing.T) {
	t.Parallel()
	result := &models.FlightSearchResult{
		Success: true,
		Count:   2,
		Flights: []models.FlightResult{
			{Price: 500, Currency: "EUR", Stops: 0, Legs: []models.FlightLeg{{Airline: "Finnair"}}},
			{Price: 300, Currency: "EUR", Stops: 1, Legs: []models.FlightLeg{{Airline: "Lufthansa"}}},
		},
	}
	summary := flightSummary(result, "HEL", "NRT")
	if !strings.Contains(summary, "Found 2 flights") {
		t.Errorf("summary = %q, want 'Found 2 flights'", summary)
	}
	if !strings.Contains(summary, "300") {
		t.Error("summary should contain cheapest price")
	}
}

func TestFlightSummary_NonstopOption(t *testing.T) {
	t.Parallel()
	result := &models.FlightSearchResult{
		Success: true,
		Count:   2,
		Flights: []models.FlightResult{
			{Price: 500, Currency: "EUR", Stops: 0, Legs: []models.FlightLeg{{Airline: "Finnair"}}},
			{Price: 300, Currency: "EUR", Stops: 1, Legs: []models.FlightLeg{{Airline: "Lufthansa"}}},
		},
	}
	summary := flightSummary(result, "HEL", "NRT")
	if !strings.Contains(summary, "Nonstop") {
		t.Error("summary should mention nonstop options")
	}
}

// The assistant text copy duplicates structuredContent, so it must be compact:
// a 53-route search_ground result spent ~121 KB of a ~197 KB response on
// indentation alone.
func TestBuildAnnotatedContentBlocks_TextCopyIsCompactJSON(t *testing.T) {
	t.Parallel()
	stop := func(city, at string) models.GroundStop {
		return models.GroundStop{City: city, Station: city + " Hbf", Time: at}
	}
	data := &models.GroundSearchResult{
		Success: true,
		Count:   2,
		Routes: []models.GroundRoute{
			{
				Provider: "flixbus", Type: "bus", Price: 19.99, Currency: "EUR", Duration: 240,
				Departure: stop("Vienna", "2026-12-01T08:00:00"), Arrival: stop("Prague", "2026-12-01T12:00:00"),
				Legs:       []models.GroundLeg{{Type: "bus", Provider: "flixbus", Departure: stop("Vienna", "2026-12-01T08:00:00"), Arrival: stop("Prague", "2026-12-01T12:00:00"), Duration: 240}},
				BookingURL: "https://example.test/book?route=1&seat=2",
			},
			{
				Provider: "regiojet", Type: "train", Price: 24.5, Currency: "EUR", Duration: 255, Transfers: 1,
				Departure: stop("Vienna", "2026-12-01T09:00:00"), Arrival: stop("Prague", "2026-12-01T13:15:00"),
				Amenities: []string{"wifi", "power"},
			},
		},
	}
	blocks, err := buildAnnotatedContentBlocks("2 routes", data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(blocks) != 2 {
		t.Fatalf("len = %d, want 2", len(blocks))
	}
	assertCompactJSONOf(t, blocks[1].Text, data)
}

// assertCompactJSONOf checks that text parses to the same JSON as payload and
// carries no indentation. Comparing parsed values keeps the check independent
// of how the encoder escapes characters such as & in URLs. The whitespace
// check assumes payload strings hold no newlines, tabs or double spaces.
func assertCompactJSONOf(t *testing.T, text string, payload any) {
	t.Helper()
	if strings.ContainsAny(text, "\n\t") || strings.Contains(text, "  ") {
		t.Errorf("text copy is indented, want compact JSON: %d bytes, %d newlines", len(text), strings.Count(text, "\n"))
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var got, want any
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("text copy is not JSON: %v", err)
	}
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("text copy does not match the structured payload:\ngot  %s\nwant %s", text, raw)
	}
}
