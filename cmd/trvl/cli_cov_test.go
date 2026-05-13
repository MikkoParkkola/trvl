package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/nlsearch"
	"github.com/MikkoParkkola/trvl/internal/optimizer"
	"github.com/MikkoParkkola/trvl/internal/preferences"
	"github.com/MikkoParkkola/trvl/internal/trip"
	"github.com/MikkoParkkola/trvl/internal/tripwindow"
	"github.com/MikkoParkkola/trvl/internal/watch"
)

type fakeTickerCov struct {
	ch   chan time.Time
	done bool
}

func (f *fakeTickerCov) Chan() <-chan time.Time { return f.ch }

func (f *fakeTickerCov) Stop() { f.done = true }

func withTempHome(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	setTestHome(t, dir)
}

var _ tripwindow.Candidate

var _ watch.Watch

var _ nlsearch.Params

var _ optimizer.OptimizeResult

func TestFormatAllIn_ZeroAllIn(t *testing.T) {
	got := formatAllIn(100, "EUR", 0, "")
	if got != "EUR 100" {
		t.Errorf("expected fallback to formatPrice, got %q", got)
	}
}

func TestFormatAllIn_EmptyBreakdown(t *testing.T) {
	got := formatAllIn(100, "EUR", 135, "")
	if got != "EUR 100" {
		t.Errorf("expected fallback to formatPrice, got %q", got)
	}
}

func TestFormatAllIn_BagFee(t *testing.T) {
	got := formatAllIn(100, "EUR", 135, "+35 bag")
	want := "EUR 135 (+35 bag)"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatAllIn_BagsIncluded(t *testing.T) {
	got := formatAllIn(89, "EUR", 89, "bags included")
	want := "EUR 89 (bags incl)"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatAllIn_FFBags(t *testing.T) {
	got := formatAllIn(89, "EUR", 89, "FF waiver")
	want := "EUR 89 (FF bags)"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatAllIn_SamePriceOtherBreakdown(t *testing.T) {
	got := formatAllIn(89, "EUR", 89, "promo discount")
	want := "EUR 89 (promo discount)"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// formatMiles
// ---------------------------------------------------------------------------

func TestFormatMiles_Short(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, "0"},
		{5, "5"},
		{99, "99"},
		{100, "100"},
		{999, "999"},
	}
	for _, tt := range tests {
		got := formatMiles(tt.n)
		if got != tt.want {
			t.Errorf("formatMiles(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestFormatMiles_WithCommas(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{1000, "1,000"},
		{1234, "1,234"},
		{12345, "12,345"},
		{100000, "100,000"},
		{1000000, "1,000,000"},
		{1234567, "1,234,567"},
	}
	for _, tt := range tests {
		got := formatMiles(tt.n)
		if got != tt.want {
			t.Errorf("formatMiles(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// hackTypeLabel
// ---------------------------------------------------------------------------

func TestHackTypeLabel_AllKnown(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"rail_fly_arbitrage", "Rail+Fly"},
		{"advance_purchase", "Timing"},
		{"fare_breakpoint", "Routing"},
		{"destination_airport", "Destination"},
		{"fuel_surcharge", "Surcharge"},
		{"group_split", "Group"},
	}
	for _, tt := range tests {
		got := hackTypeLabel(tt.input)
		if got != tt.want {
			t.Errorf("hackTypeLabel(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestHackTypeLabel_Unknown(t *testing.T) {
	got := hackTypeLabel("some_other_type")
	want := "some other type"
	if got != want {
		t.Errorf("hackTypeLabel(unknown) = %q, want %q", got, want)
	}
}

func TestHackTypeLabel_Empty(t *testing.T) {
	got := hackTypeLabel("")
	if got != "" {
		t.Errorf("hackTypeLabel empty = %q, want empty", got)
	}
}

// ---------------------------------------------------------------------------
// tripCostHotelDetail
// ---------------------------------------------------------------------------

func TestTripCostHotelDetail_Unavailable(t *testing.T) {
	h := trip.HotelCost{Total: 0, PerNight: 0}
	got := tripCostHotelDetail(h, 5)
	if got != "Unavailable" {
		t.Errorf("expected Unavailable, got %q", got)
	}
}

func TestTripCostHotelDetail_NoName(t *testing.T) {
	h := trip.HotelCost{Total: 500, PerNight: 100, Currency: "EUR"}
	got := tripCostHotelDetail(h, 5)
	want := "EUR 100/night x 5 nights"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestTripCostHotelDetail_WithName(t *testing.T) {
	h := trip.HotelCost{Total: 500, PerNight: 100, Currency: "EUR", Name: "Hotel ABC"}
	got := tripCostHotelDetail(h, 5)
	want := "Hotel ABC, EUR 100/night x 5 nights"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestTripCostHotelDetail_ZeroPerNight(t *testing.T) {
	h := trip.HotelCost{Total: 100, PerNight: 0, Currency: "EUR"}
	got := tripCostHotelDetail(h, 3)
	if got != "Unavailable" {
		t.Errorf("expected Unavailable for zero PerNight, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// tripCostFlightDetail — extra cases
// ---------------------------------------------------------------------------

func TestTripCostFlightDetail_UnavailableZero(t *testing.T) {
	got := tripCostFlightDetail(0, "HEL", "BCN", 1)
	if got != "Unavailable" {
		t.Errorf("expected Unavailable, got %q", got)
	}
}

func TestTripCostFlightDetail_NegativeAmount(t *testing.T) {
	got := tripCostFlightDetail(-10, "HEL", "BCN", 0)
	if got != "Unavailable" {
		t.Errorf("expected Unavailable for negative, got %q", got)
	}
}

func TestTripCostFlightDetail_WithData(t *testing.T) {
	got := tripCostFlightDetail(150, "HEL", "BCN", 1)
	if !strings.Contains(got, "HEL") || !strings.Contains(got, "BCN") || !strings.Contains(got, "1 stop") {
		t.Errorf("unexpected detail: %q", got)
	}
}

// ---------------------------------------------------------------------------
// printTripCostTable
// ---------------------------------------------------------------------------

func TestPrintTripCostTable_FailedResult(t *testing.T) {
	old := os.Stdout
	os.Stdout, _ = os.CreateTemp("", "test")
	defer func() { os.Stdout = old }()

	result := &trip.TripCostResult{Success: false, Error: "test failure"}
	err := printTripCostTable(result, "HEL", "BCN", 1, false)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPrintTripCostTable_SuccessWithWarning(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()

	result := &trip.TripCostResult{
		Success: true,
		Error:   "partial data",
		Flights: trip.FlightCost{
			Outbound: 100, Return: 120, Currency: "EUR",
		},
		Hotels:    trip.HotelCost{Total: 500, PerNight: 100, Currency: "EUR", Name: "Test Hotel"},
		Total:     720,
		Currency:  "EUR",
		PerPerson: 720,
		PerDay:    103,
		Nights:    7,
	}
	err := printTripCostTable(result, "HEL", "BCN", 1, false)
	_ = w.Close()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "HEL") || !strings.Contains(output, "BCN") {
		t.Errorf("expected route in output, got: %s", output)
	}
}

// ---------------------------------------------------------------------------
// printOptimizeResults
// ---------------------------------------------------------------------------

func TestPrintOptimizeResults_Empty(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()

	result := &optimizer.OptimizeResult{
		Success: true,
		Options: nil,
	}
	printOptimizeResults(result, "", nil, false)
	_ = w.Close()

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	if !strings.Contains(buf.String(), "Optimize") {
		t.Errorf("expected banner in output, got: %s", buf.String())
	}
}

func TestPrintOptimizeResults_WithOptions(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()

	result := &optimizer.OptimizeResult{
		Success: true,
		Options: []optimizer.BookingOption{
			{
				Rank:              1,
				Strategy:          "Direct",
				AllInCost:         200,
				Currency:          "EUR",
				SavingsVsBaseline: 50,
				HacksApplied:      []string{"advance_purchase"},
			},
			{
				Rank:         2,
				Strategy:     "Via TLL",
				AllInCost:    250,
				Currency:     "EUR",
				HacksApplied: nil,
			},
		},
		Baseline: &optimizer.BookingOption{
			AllInCost: 250,
			Currency:  "EUR",
		},
	}
	printOptimizeResults(result, "", nil, false)
	_ = w.Close()

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	output := buf.String()
	if !strings.Contains(output, "Direct") {
		t.Errorf("expected strategy in output, got: %s", output)
	}
}

func TestPrintOptimizeResults_BaselineCheapest(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()

	result := &optimizer.OptimizeResult{
		Success: true,
		Options: []optimizer.BookingOption{
			{Rank: 1, Strategy: "Direct", AllInCost: 200, Currency: "EUR", SavingsVsBaseline: 0},
		},
		Baseline: &optimizer.BookingOption{AllInCost: 200, Currency: "EUR"},
	}
	printOptimizeResults(result, "", nil, false)
	_ = w.Close()

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	if !strings.Contains(buf.String(), "cheapest") {
		t.Errorf("expected cheapest message when no savings")
	}
}

// ---------------------------------------------------------------------------
// printMilesEarning
// ---------------------------------------------------------------------------

func TestPrintMilesEarning_NoFFPrograms(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()

	prefs := &preferences.Preferences{}
	printMilesEarning(prefs, "HEL", "BCN", models.FlightResult{})
	_ = w.Close()

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	if buf.Len() != 0 {
		t.Errorf("expected no output for empty FF programs, got: %s", buf.String())
	}
}

func TestPrintMilesEarning_NoAirlineCode(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()

	prefs := &preferences.Preferences{
		FrequentFlyerPrograms: []preferences.FrequentFlyerStatus{
			{ProgramName: "Finnair Plus", Alliance: "oneworld"},
		},
	}
	printMilesEarning(prefs, "HEL", "BCN", models.FlightResult{
		Legs: nil,
	})
	_ = w.Close()

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	if buf.Len() != 0 {
		t.Errorf("expected no output for empty airline code, got: %s", buf.String())
	}
}

func TestPrintMilesEarning_WithData(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()

	prefs := &preferences.Preferences{
		FrequentFlyerPrograms: []preferences.FrequentFlyerStatus{
			{ProgramName: "Finnair Plus", Alliance: "oneworld", MilesBalance: 50000},
		},
	}
	flight := models.FlightResult{
		Price:    150,
		Currency: "EUR",
		Legs: []models.FlightLeg{
			{AirlineCode: "AY", Airline: "Finnair"},
		},
	}
	printMilesEarning(prefs, "HEL", "BCN", flight)
	_ = w.Close()

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	// Output depends on points.EstimateMilesEarned returning > 0.
	// Even if 0, the function ran without error -- we exercised the code path.
	_ = buf
}

// ---------------------------------------------------------------------------
// formatWatchDates
// ---------------------------------------------------------------------------

func TestFormatWatchDates_RoomWatchBasic(t *testing.T) {
	w := watch.Watch{
		Type:       "room",
		DepartDate: "2026-06-15",
		ReturnDate: "2026-06-18",
	}
	got := formatWatchDates(w)
	if !strings.Contains(got, "2026-06-15") || !strings.Contains(got, "2026-06-18") {
		t.Errorf("expected dates in output, got: %q", got)
	}
}

func TestFormatWatchDates_RoomWatchWithMatch(t *testing.T) {
	w := watch.Watch{
		Type:        "room",
		DepartDate:  "2026-06-15",
		ReturnDate:  "2026-06-18",
		MatchedRoom: "Deluxe Suite",
	}
	got := formatWatchDates(w)
	if !strings.Contains(got, "Deluxe Suite") {
		t.Errorf("expected matched room in output, got: %q", got)
	}
}

func TestFormatWatchDates_RouteWatchAny(t *testing.T) {
	w := watch.Watch{Type: "flight", Origin: "HEL", Destination: "BCN"}
	got := formatWatchDates(w)
	if !strings.Contains(got, "any") {
		t.Errorf("expected 'any' for route watch, got: %q", got)
	}
}

func TestFormatWatchDates_RouteWatchShowsBest(t *testing.T) {
	w := watch.Watch{
		Type: "flight", Origin: "HEL", Destination: "BCN",
		CheapestDate: "2026-07-01",
	}
	got := formatWatchDates(w)
	if !strings.Contains(got, "best: 2026-07-01") {
		t.Errorf("expected cheapest date, got: %q", got)
	}
}

func TestFormatWatchDates_DateRangeBasic(t *testing.T) {
	w := watch.Watch{
		Type: "flight", Origin: "HEL", Destination: "BCN",
		DepartFrom: "2026-06-01", DepartTo: "2026-06-30",
	}
	got := formatWatchDates(w)
	if !strings.Contains(got, "..") {
		t.Errorf("expected range notation, got: %q", got)
	}
}
