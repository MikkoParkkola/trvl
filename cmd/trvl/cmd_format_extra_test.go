package main

import (
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/spf13/cobra"
)

// ---------------------------------------------------------------------------
// Helper function tests
// ---------------------------------------------------------------------------

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		mins int
		want string
	}{
		{0, "-"},
		{45, "45m"},
		{60, "1h 0m"},
		{90, "1h 30m"},
		{150, "2h 30m"},
	}
	for _, tt := range tests {
		got := formatDuration(tt.mins)
		if got != tt.want {
			t.Errorf("formatDuration(%d) = %q, want %q", tt.mins, got, tt.want)
		}
	}
}

func TestFormatStops(t *testing.T) {
	tests := []struct {
		stops int
		want  string
	}{
		{0, "Direct"},
		{1, "1 stop"},
		{2, "2 stops"},
		{3, "3 stops"},
	}
	for _, tt := range tests {
		got := formatStops(tt.stops)
		if got != tt.want {
			t.Errorf("formatStops(%d) = %q, want %q", tt.stops, got, tt.want)
		}
	}
}

func TestFormatPrice(t *testing.T) {
	tests := []struct {
		amount   float64
		currency string
		want     string
	}{
		{0, "EUR", "-"},
		{199, "EUR", "EUR 199"},
		{1234, "USD", "USD 1234"},
	}
	for _, tt := range tests {
		got := formatPrice(tt.amount, tt.currency)
		if got != tt.want {
			t.Errorf("formatPrice(%v, %q) = %q, want %q", tt.amount, tt.currency, got, tt.want)
		}
	}
}

func TestFormatGroundTime(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"2026-07-01T14:30:00", "14:30"},
		{"2026-07-01T08:00:00+02:00", "08:00"},
		{"short", "short"},
	}
	for _, tt := range tests {
		got := formatGroundTime(tt.input)
		if got != tt.want {
			t.Errorf("formatGroundTime(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		s      string
		maxLen int
		want   string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "he..."},
		{"ab", 2, "ab"},
		{"abc", 3, "abc"},
		{"abcd", 3, "abc"},
	}
	for _, tt := range tests {
		got := truncate(tt.s, tt.maxLen)
		if got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.s, tt.maxLen, got, tt.want)
		}
	}
}

func TestSplitLines(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"one", 1},
		{"one\ntwo\nthree", 3},
		{"", 0},
	}
	for _, tt := range tests {
		got := splitLines(tt.input)
		if len(got) != tt.want {
			t.Errorf("splitLines(%q) returned %d lines, want %d", tt.input, len(got), tt.want)
		}
	}
}

func TestFlightRoute(t *testing.T) {
	tests := []struct {
		name string
		f    models.FlightResult
		want string
	}{
		{"empty", models.FlightResult{}, ""},
		{"direct", models.FlightResult{
			Legs: []models.FlightLeg{
				{DepartureAirport: models.AirportInfo{Code: "HEL"}, ArrivalAirport: models.AirportInfo{Code: "NRT"}},
			},
		}, "HEL -> NRT"},
		{"one stop", models.FlightResult{
			Legs: []models.FlightLeg{
				{DepartureAirport: models.AirportInfo{Code: "HEL"}, ArrivalAirport: models.AirportInfo{Code: "FRA"}},
				{DepartureAirport: models.AirportInfo{Code: "FRA"}, ArrivalAirport: models.AirportInfo{Code: "NRT"}},
			},
		}, "HEL -> FRA -> NRT"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := flightRoute(tt.f)
			if got != tt.want {
				t.Errorf("flightRoute() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFlightRoute_AnnotatesLayover(t *testing.T) {
	f := models.FlightResult{
		Legs: []models.FlightLeg{
			{DepartureAirport: models.AirportInfo{Code: "BRU"}, ArrivalAirport: models.AirportInfo{Code: "FRA"}},
			{DepartureAirport: models.AirportInfo{Code: "FRA"}, ArrivalAirport: models.AirportInfo{Code: "TLL"}, LayoverMinutes: 120},
		},
	}
	want := "BRU -> FRA (2h 0m) -> TLL"
	if got := flightRoute(f); got != want {
		t.Errorf("flightRoute() = %q, want %q", got, want)
	}
}

func TestFlightRoute_RoundTripSplitsByDirection(t *testing.T) {
	// A composed round-trip carries inbound-tagged legs; the route must render
	// each direction separately so the return is visible and the turnaround is
	// never disguised as a connection.
	f := models.FlightResult{
		Legs: []models.FlightLeg{
			{DepartureAirport: models.AirportInfo{Code: "HEL"}, ArrivalAirport: models.AirportInfo{Code: "BCN"}, Direction: "outbound"},
			{DepartureAirport: models.AirportInfo{Code: "BCN"}, ArrivalAirport: models.AirportInfo{Code: "HEL"}, Direction: "inbound"},
		},
	}
	want := "HEL -> BCN  ||  BCN -> HEL"
	if got := flightRoute(f); got != want {
		t.Errorf("flightRoute() = %q, want %q", got, want)
	}
}

func TestFlightRoute_RoundTripWithConnections(t *testing.T) {
	// Each direction keeps its own layover annotation independently.
	f := models.FlightResult{
		Legs: []models.FlightLeg{
			{DepartureAirport: models.AirportInfo{Code: "HEL"}, ArrivalAirport: models.AirportInfo{Code: "FRA"}, Direction: "outbound"},
			{DepartureAirport: models.AirportInfo{Code: "FRA"}, ArrivalAirport: models.AirportInfo{Code: "BCN"}, Direction: "outbound", LayoverMinutes: 90},
			{DepartureAirport: models.AirportInfo{Code: "BCN"}, ArrivalAirport: models.AirportInfo{Code: "HEL"}, Direction: "inbound"},
		},
	}
	want := "HEL -> FRA (1h 30m) -> BCN  ||  BCN -> HEL"
	if got := flightRoute(f); got != want {
		t.Errorf("flightRoute() = %q, want %q", got, want)
	}
}

func TestFlightAirlinesDisplay(t *testing.T) {
	tests := []struct {
		name string
		f    models.FlightResult
		want string
	}{
		{"single", models.FlightResult{Legs: []models.FlightLeg{{Airline: "Finnair"}}}, "Finnair"},
		{"dedup same", models.FlightResult{Legs: []models.FlightLeg{{Airline: "Finnair"}, {Airline: "Finnair"}}}, "Finnair"},
		{"mixed", models.FlightResult{Legs: []models.FlightLeg{{Airline: "Brussels"}, {Airline: "Lufthansa"}}}, "Brussels / Lufthansa"},
		{"empty", models.FlightResult{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := flightAirlinesDisplay(tt.f); got != tt.want {
				t.Errorf("flightAirlinesDisplay() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFlightNumbersDisplay(t *testing.T) {
	tests := []struct {
		name string
		f    models.FlightResult
		want string
	}{
		{"single", models.FlightResult{Legs: []models.FlightLeg{{FlightNumber: "AY1306"}}}, "AY1306"},
		{"connection", models.FlightResult{Legs: []models.FlightLeg{{FlightNumber: "SN2611"}, {FlightNumber: "LH882"}}}, "SN2611 / LH882"},
		{"all empty", models.FlightResult{Legs: []models.FlightLeg{{}, {}}}, "-"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := flightNumbersDisplay(tt.f); got != tt.want {
				t.Errorf("flightNumbersDisplay() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFlightAircraftDisplay(t *testing.T) {
	tests := []struct {
		name string
		f    models.FlightResult
		want string
	}{
		{"single", models.FlightResult{Legs: []models.FlightLeg{{Aircraft: "Airbus A350"}}}, "A350"},
		{"connection", models.FlightResult{Legs: []models.FlightLeg{{Aircraft: "Airbus A319"}, {Aircraft: "Airbus A320"}}}, "A319 / A320"},
		{"boeing", models.FlightResult{Legs: []models.FlightLeg{{Aircraft: "Boeing 737-800"}}}, "737-800"},
		{"all empty", models.FlightResult{Legs: []models.FlightLeg{{}, {}}}, "-"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := flightAircraftDisplay(tt.f); got != tt.want {
				t.Errorf("flightAircraftDisplay() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatLegDeparture(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"google form", "2026-05-28T19:25", "Thu 28 May 19:25"},
		{"rfc3339", "2026-05-28T19:25:00+02:00", "Thu 28 May 19:25"},
		{"unparseable falls back", "garbage", "garbage"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatLegDeparture(tt.raw); got != tt.want {
				t.Errorf("formatLegDeparture(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestFormatLegArrival(t *testing.T) {
	tests := []struct {
		name string
		dep  string
		arr  string
		want string
	}{
		{"same day", "2026-05-28T19:25", "2026-05-28T22:45", "22:45"},
		{"overnight +1", "2026-05-29T21:00", "2026-05-30T00:25", "00:25 +1"},
		{"unparseable arr falls back", "2026-05-28T19:25", "nope", "nope"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatLegArrival(tt.dep, tt.arr); got != tt.want {
				t.Errorf("formatLegArrival(%q,%q) = %q, want %q", tt.dep, tt.arr, got, tt.want)
			}
		})
	}
}

func TestPrintBookingLinks(t *testing.T) {
	flights := []models.FlightResult{
		{Provider: "google_flights", BookingURL: "https://book.test/a", Legs: []models.FlightLeg{{Airline: "Finnair"}}},
		{Provider: "kiwi", BookingURL: "", Legs: []models.FlightLeg{{Airline: "KLM"}}}, // no URL -> skipped
		{Provider: "skiplagged", BookingURL: "https://book.test/c", Legs: []models.FlightLeg{{Airline: "easyJet"}}},
	}
	var b strings.Builder
	printBookingLinks(&b, flights)
	out := b.String()
	if !strings.Contains(out, "Booking links:") {
		t.Fatalf("missing header; got:\n%s", out)
	}
	// Index must match the table position (1-based): first flight is [1], third is [3].
	if !strings.Contains(out, "[1] Finnair · Google — https://book.test/a") {
		t.Errorf("missing/incorrect link 1; got:\n%s", out)
	}
	if !strings.Contains(out, "[3] easyJet · skiplagged — https://book.test/c") {
		t.Errorf("missing/incorrect link 3; got:\n%s", out)
	}
	if strings.Contains(out, "[2]") {
		t.Errorf("flight without URL should be skipped; got:\n%s", out)
	}
}

func TestPrintBookingLinks_NoLinksPrintsNothing(t *testing.T) {
	var b strings.Builder
	printBookingLinks(&b, []models.FlightResult{{Provider: "kiwi", BookingURL: ""}})
	if b.String() != "" {
		t.Errorf("expected empty output when no URLs, got: %q", b.String())
	}
}

func TestHotelSearchLinks(t *testing.T) {
	h := models.HotelResult{Name: "Pestana Casino Park"}
	booking, google := hotelSearchLinks(h, "Funchal")
	if !strings.Contains(booking, "booking.com/searchresults.html?ss=") {
		t.Errorf("booking link wrong: %s", booking)
	}
	if !strings.Contains(booking, "Pestana") || !strings.Contains(booking, "Funchal") {
		t.Errorf("booking link missing name/location: %s", booking)
	}
	if !strings.Contains(google, "google.com/travel/search?q=") {
		t.Errorf("google link wrong: %s", google)
	}
	// Location already in the name -> not duplicated.
	h2 := models.HotelResult{Name: "Hotel Funchal Centro"}
	b2, _ := hotelSearchLinks(h2, "Funchal")
	if strings.Count(strings.ToLower(b2), "funchal") != 1 {
		t.Errorf("location should not be duplicated when already in name: %s", b2)
	}
}

func TestPrintHotelLinks(t *testing.T) {
	hotels := []models.HotelResult{
		{Name: "TUI BLUE Madeira Gardens", ImageURL: "https://img.test/a.jpg"},
		{Name: "Quinta da Penha"}, // no image -> Photo line omitted
	}
	var b strings.Builder
	printHotelLinks(&b, hotels, "Funchal")
	out := b.String()
	if !strings.Contains(out, "Links (photos & booking):") {
		t.Fatalf("missing header; got:\n%s", out)
	}
	if !strings.Contains(out, "[1] TUI BLUE Madeira Gardens") || !strings.Contains(out, "[2] Quinta da Penha") {
		t.Errorf("indices wrong; got:\n%s", out)
	}
	if !strings.Contains(out, "Photo:         https://img.test/a.jpg") {
		t.Errorf("missing image link for hotel 1; got:\n%s", out)
	}
	// Hotel 2 has no image -> exactly one Photo line total.
	if strings.Count(out, "Photo:") != 1 {
		t.Errorf("expected exactly 1 Photo line; got:\n%s", out)
	}
}

func TestPrintHotelLinks_EmptyPrintsNothing(t *testing.T) {
	var b strings.Builder
	printHotelLinks(&b, nil, "Funchal")
	if b.String() != "" {
		t.Errorf("expected empty output for no hotels, got: %q", b.String())
	}
}

func TestStarRating(t *testing.T) {
	tests := []struct {
		rating float64
		full   int
		total  int // total star runes
	}{
		{5.0, 5, 5},
		{4.5, 4, 5},
		{3.0, 3, 5},
		{0.0, 0, 5},
	}
	for _, tt := range tests {
		got := starRating(tt.rating)
		// Count filled stars (U+2605 = 3 bytes).
		filled := strings.Count(got, "\u2605")
		if filled != tt.full {
			t.Errorf("starRating(%.1f) has %d filled stars, want %d", tt.rating, filled, tt.full)
		}
		totalRunes := strings.Count(got, "\u2605") + strings.Count(got, "\u2606")
		if totalRunes != tt.total {
			t.Errorf("starRating(%.1f) has %d total stars, want %d", tt.rating, totalRunes, tt.total)
		}
	}
}

func TestFormatDealAge(t *testing.T) {
	now := time.Now()
	tests := []struct {
		t    time.Time
		want string
	}{
		{now.Add(-30 * time.Minute), "30m ago"},
		{now.Add(-5 * time.Hour), "5h ago"},
		{now.Add(-48 * time.Hour), "2d ago"},
	}
	for _, tt := range tests {
		got := formatDealAge(tt.t)
		if got != tt.want {
			t.Errorf("formatDealAge(%v) = %q, want %q", tt.t, got, tt.want)
		}
	}
}

func TestAirportCompletion(t *testing.T) {
	// With 2+ args, should return no suggestions.
	suggestions, directive := airportCompletion(nil, []string{"HEL", "NRT"}, "")
	if len(suggestions) != 0 {
		t.Errorf("expected 0 suggestions with 2 args, got %d", len(suggestions))
	}
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("unexpected directive: %v", directive)
	}
}

func TestAirportCompletion_Partial(t *testing.T) {
	suggestions, directive := airportCompletion(nil, nil, "HEL")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("unexpected directive: %v", directive)
	}
	// HEL should appear in suggestions (Helsinki-Vantaa).
	found := false
	for _, s := range suggestions {
		if strings.HasPrefix(s, "HEL\t") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected HEL in completion suggestions")
	}
}

func TestVersionCmd_Exists(t *testing.T) {
	if versionCmd.Use != "version" {
		t.Errorf("versionCmd.Use = %q, want %q", versionCmd.Use, "version")
	}
}
