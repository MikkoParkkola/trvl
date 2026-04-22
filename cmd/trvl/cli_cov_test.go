package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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

// ---------------------------------------------------------------------------
// formatAllIn
// ---------------------------------------------------------------------------

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
	err := printTripCostTable(result, "HEL", "BCN", 1)
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
	err := printTripCostTable(result, "HEL", "BCN", 1)
	w.Close()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)
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
	printOptimizeResults(result)
	w.Close()

	var buf bytes.Buffer
	buf.ReadFrom(r)
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
	printOptimizeResults(result)
	w.Close()

	var buf bytes.Buffer
	buf.ReadFrom(r)
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
	printOptimizeResults(result)
	w.Close()

	var buf bytes.Buffer
	buf.ReadFrom(r)
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
	w.Close()

	var buf bytes.Buffer
	buf.ReadFrom(r)
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
	w.Close()

	var buf bytes.Buffer
	buf.ReadFrom(r)
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
	w.Close()

	var buf bytes.Buffer
	buf.ReadFrom(r)
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

func TestFormatWatchDates_DateRangeShowsBest(t *testing.T) {
	w := watch.Watch{
		Type: "flight", Origin: "HEL", Destination: "BCN",
		DepartFrom: "2026-06-01", DepartTo: "2026-06-30",
		CheapestDate: "2026-06-15",
	}
	got := formatWatchDates(w)
	if !strings.Contains(got, "best: 2026-06-15") {
		t.Errorf("expected cheapest date in range, got: %q", got)
	}
}

func TestFormatWatchDates_FixedDates(t *testing.T) {
	w := watch.Watch{
		Type: "flight", Origin: "HEL", Destination: "BCN",
		DepartDate: "2026-06-15",
	}
	got := formatWatchDates(w)
	if got != "2026-06-15" {
		t.Errorf("expected depart date only, got: %q", got)
	}
}

func TestFormatWatchDates_FixedDatesWithReturn(t *testing.T) {
	w := watch.Watch{
		Type: "flight", Origin: "HEL", Destination: "BCN",
		DepartDate: "2026-06-15", ReturnDate: "2026-06-22",
	}
	got := formatWatchDates(w)
	want := "2026-06-15 / 2026-06-22"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// formatLastCheck — renamed to avoid conflicts with watch_test.go
// ---------------------------------------------------------------------------

func TestFormatLastCheck_ZeroTime(t *testing.T) {
	got := formatLastCheck(time.Time{})
	if got != "never" {
		t.Errorf("expected 'never', got %q", got)
	}
}

func TestFormatLastCheck_Recent(t *testing.T) {
	got := formatLastCheck(time.Now().Add(-10 * time.Second))
	if got != "just now" {
		t.Errorf("expected 'just now', got %q", got)
	}
}

func TestFormatLastCheck_FewMinutes(t *testing.T) {
	got := formatLastCheck(time.Now().Add(-15 * time.Minute))
	if !strings.Contains(got, "m ago") {
		t.Errorf("expected minutes ago, got %q", got)
	}
}

func TestFormatLastCheck_FewHours(t *testing.T) {
	got := formatLastCheck(time.Now().Add(-5 * time.Hour))
	if !strings.Contains(got, "h ago") {
		t.Errorf("expected hours ago, got %q", got)
	}
}

func TestFormatLastCheck_MultipleDays(t *testing.T) {
	got := formatLastCheck(time.Now().Add(-72 * time.Hour))
	if !strings.Contains(got, "d ago") {
		t.Errorf("expected days ago, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// printSearchInterpretation
// ---------------------------------------------------------------------------

func TestPrintSearchInterpretation_Full(t *testing.T) {
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	defer func() { os.Stderr = oldStderr }()

	params := nlsearch.Params{
		Intent:      "flight",
		Origin:      "HEL",
		Destination: "BCN",
		Date:        "2026-06-15",
		ReturnDate:  "2026-06-22",
	}
	printSearchInterpretation("fly HEL BCN 2026-06-15", params)
	w.Close()

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()
	if !strings.Contains(output, "intent=flight") {
		t.Errorf("expected intent in output, got: %s", output)
	}
	if !strings.Contains(output, "from=HEL") {
		t.Errorf("expected origin, got: %s", output)
	}
	if !strings.Contains(output, "return=2026-06-22") {
		t.Errorf("expected return date, got: %s", output)
	}
}

func TestPrintSearchInterpretation_Minimal(t *testing.T) {
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	defer func() { os.Stderr = oldStderr }()

	params := nlsearch.Params{Intent: "deals"}
	printSearchInterpretation("deals", params)
	w.Close()

	var buf bytes.Buffer
	buf.ReadFrom(r)
	if !strings.Contains(buf.String(), "intent=deals") {
		t.Errorf("expected intent in output, got: %s", buf.String())
	}
}

// ---------------------------------------------------------------------------
// missingFieldsHint — renamed to avoid conflicts with search_test.go
// ---------------------------------------------------------------------------

func TestMissingFieldsHint_FlightAllMissing(t *testing.T) {
	oldStderr := os.Stderr
	_, w, _ := os.Pipe()
	os.Stderr = w
	defer func() { os.Stderr = oldStderr }()

	err := missingFieldsHint(nlsearch.Params{}, "flight", "trvl flights ...")
	w.Close()

	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

func TestMissingFieldsHint_HotelMissingDates(t *testing.T) {
	oldStderr := os.Stderr
	_, w, _ := os.Pipe()
	os.Stderr = w
	defer func() { os.Stderr = oldStderr }()

	err := missingFieldsHint(nlsearch.Params{}, "hotel", "trvl hotels ...")
	w.Close()

	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

func TestMissingFieldsHint_DealsNoMissing(t *testing.T) {
	oldStderr := os.Stderr
	_, w, _ := os.Pipe()
	os.Stderr = w
	defer func() { os.Stderr = oldStderr }()

	err := missingFieldsHint(nlsearch.Params{}, "deals", "trvl deals")
	w.Close()

	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// loadExistingKeys / saveKeysTo
// ---------------------------------------------------------------------------

func TestLoadExistingKeys_MissingFile(t *testing.T) {
	k := loadExistingKeys()
	_ = k // returns zero struct without panic
}

func TestSaveKeysTo_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keys.json")

	keys := APIKeys{
		SeatsAero: "sa-key",
		Kiwi:      "kiwi-key",
	}
	err := saveKeysTo(path, keys)
	if err != nil {
		t.Fatalf("saveKeysTo failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "sa-key") {
		t.Errorf("expected seats_aero key in file, got: %s", content)
	}
	if !strings.Contains(content, "kiwi-key") {
		t.Errorf("expected kiwi key in file, got: %s", content)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	if runtime.GOOS != "windows" {
		perm := info.Mode().Perm()
		if perm&0o077 != 0 {
			t.Errorf("keys file should be owner-only, got permissions %o", perm)
		}
	}
}

// ---------------------------------------------------------------------------
// runInstallCodexTOML
// ---------------------------------------------------------------------------

func TestRunInstallCodexTOML_DryRun(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "codex.toml")

	err := runInstallCodexTOML(cfgPath, "/usr/local/bin/trvl", false, true)
	w.Close()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)
	if !strings.Contains(buf.String(), "Would append") {
		t.Errorf("expected dry-run output, got: %s", buf.String())
	}
}

func TestRunInstallCodexTOML_CreateNew(t *testing.T) {
	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "codex.toml")

	err := runInstallCodexTOML(cfgPath, "/usr/local/bin/trvl", false, false)
	w.Close()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if !strings.Contains(string(data), "[mcp_servers.trvl]") {
		t.Errorf("expected TOML entry in file, got: %s", string(data))
	}
}

func TestRunInstallCodexTOML_AlreadyInstalled(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "codex.toml")
	_ = os.WriteFile(cfgPath, []byte("[mcp_servers.trvl]\ncommand = \"old\""), 0o644)

	err := runInstallCodexTOML(cfgPath, "/usr/local/bin/trvl", false, false)
	w.Close()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)
	if !strings.Contains(buf.String(), "already installed") {
		t.Errorf("expected already-installed message, got: %s", buf.String())
	}
}

func TestRunInstallCodexTOML_ForceOverwrite(t *testing.T) {
	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "codex.toml")
	_ = os.WriteFile(cfgPath, []byte("[mcp_servers.trvl]\ncommand = \"old\""), 0o644)

	err := runInstallCodexTOML(cfgPath, "/usr/local/bin/trvl", true, false)
	w.Close()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if !strings.Contains(string(data), "trvl") {
		t.Errorf("expected trvl config after force, got: %s", string(data))
	}
}

// ---------------------------------------------------------------------------
// maybeShowFlightHackTips — exercise early returns
// ---------------------------------------------------------------------------

func TestMaybeShowFlightHackTips_NilResult(t *testing.T) {
	maybeShowFlightHackTips(context.Background(), nil, nil, "", "", 1, nil)
}

func TestMaybeShowFlightHackTips_EmptyFlights(t *testing.T) {
	result := &models.FlightSearchResult{Success: true, Flights: nil}
	maybeShowFlightHackTips(context.Background(), []string{"HEL"}, []string{"BCN"}, "2026-06-15", "", 1, result)
}

func TestMaybeShowFlightHackTips_FailedResult(t *testing.T) {
	result := &models.FlightSearchResult{Success: false}
	maybeShowFlightHackTips(context.Background(), []string{"HEL"}, []string{"BCN"}, "2026-06-15", "", 1, result)
}

// ---------------------------------------------------------------------------
// resolveString — extra cases (renamed to avoid conflicts with setup_test.go)
// ---------------------------------------------------------------------------

func TestResolveString_NonInteractiveWithExisting(t *testing.T) {
	got := resolveString(true, "", "existing", "fallback")
	if got != "existing" {
		t.Errorf("expected existing even in non-interactive, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// isCodexTOML
// ---------------------------------------------------------------------------

func TestIsCodexTOML_Various(t *testing.T) {
	tests := []struct {
		client string
		want   bool
	}{
		{"codex", true},
		{"Codex", true},
		{"CODEX", true},
		{"claude", false},
		{"cursor", false},
		{"", false},
	}
	for _, tt := range tests {
		got := isCodexTOML(tt.client)
		if got != tt.want {
			t.Errorf("isCodexTOML(%q) = %v, want %v", tt.client, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// starRating — additional edge cases
// ---------------------------------------------------------------------------

func TestStarRating_FractionalRounding(t *testing.T) {
	got := starRating(3.5)
	fullCount := strings.Count(got, "\u2605")
	if fullCount != 3 {
		t.Errorf("expected 3 filled stars for 3.5, got %d in %q", fullCount, got)
	}
}

func TestStarRating_ExactInt(t *testing.T) {
	got := starRating(4.0)
	fullCount := strings.Count(got, "\u2605")
	if fullCount != 4 {
		t.Errorf("expected 4 filled stars for 4.0, got %d in %q", fullCount, got)
	}
}

// ---------------------------------------------------------------------------
// looksLikeGoogleHotelID — colon format
// ---------------------------------------------------------------------------

func TestLooksLikeGoogleHotelID_ColonFormat(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"abc:123", true},
		{"foo:bar baz", false},
		{"no-colon-here", false},
		{"two:colons:here", false},
	}
	for _, tt := range tests {
		got := looksLikeGoogleHotelID(tt.input)
		if got != tt.want {
			t.Errorf("looksLikeGoogleHotelID(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// colorizeVisaStatus — extra statuses
// ---------------------------------------------------------------------------

func TestColorizeVisaStatus_EvisaAndUnknown(t *testing.T) {
	got := colorizeVisaStatus("e-visa", "E-visa")
	if got == "" {
		t.Error("expected non-empty output for e-visa")
	}

	got2 := colorizeVisaStatus("unknown-status", "Unknown")
	if got2 != "Unknown" {
		t.Errorf("expected passthrough for unknown status, got %q", got2)
	}
}

// ---------------------------------------------------------------------------
// trvlFooter / capitalizeFirst / formatDateCompact — share.go helpers
// ---------------------------------------------------------------------------

func TestTrvlFooter_ContainsURL(t *testing.T) {
	f := trvlFooter()
	if !strings.Contains(f, "trvl") {
		t.Errorf("expected trvl in footer, got %q", f)
	}
}

func TestCapitalizeFirst_Table(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"hello", "Hello"},
		{"H", "H"},
		{"", ""},
		{"already", "Already"},
	}
	for _, tt := range tests {
		got := capitalizeFirst(tt.in)
		if got != tt.want {
			t.Errorf("capitalizeFirst(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestFormatDateCompact_Valid(t *testing.T) {
	got := formatDateCompact("2026-06-15")
	if !strings.Contains(got, "Jun") || !strings.Contains(got, "15") {
		t.Errorf("expected formatted date, got %q", got)
	}
}

func TestFormatDateCompact_Invalid(t *testing.T) {
	got := formatDateCompact("not-a-date")
	if got != "not-a-date" {
		t.Errorf("expected passthrough for invalid date, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// lastSearchPath
// ---------------------------------------------------------------------------

func TestLastSearchPath_NonEmpty(t *testing.T) {
	p := lastSearchPath()
	if p == "" {
		t.Error("expected non-empty path")
	}
	if !strings.Contains(p, ".trvl") {
		t.Errorf("expected .trvl in path, got %q", p)
	}
}

// ---------------------------------------------------------------------------
// saveLastSearch + loadLastSearch round trip
// ---------------------------------------------------------------------------

func TestSaveLoadLastSearch_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	setTestHome(t, dir)

	ls := &LastSearch{
		Command:     "flights",
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-06-15",
	}
	saveLastSearch(ls)

	loaded, err := loadLastSearch()
	if err != nil {
		t.Fatalf("loadLastSearch error: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected loaded last search, got nil")
	}
	if loaded.Origin != "HEL" || loaded.Destination != "BCN" {
		t.Errorf("loaded data mismatch: %+v", loaded)
	}
}

// ---------------------------------------------------------------------------
// printWhenTable — with notes and hotel
// ---------------------------------------------------------------------------

func TestPrintWhenTable_WithNotesAndHotel(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()

	candidates := []tripwindow.Candidate{
		{
			Start:             "2026-06-15",
			End:               "2026-06-20",
			Nights:            5,
			FlightCost:        200,
			HotelCost:         400,
			EstimatedCost:     600,
			Currency:          "EUR",
			OverlapsPreferred: true,
			HotelName:         "Hotel ABC",
		},
	}
	err := printWhenTable(candidates, "HEL", "BCN")
	w.Close()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()
	if !strings.Contains(output, "preferred") {
		t.Errorf("expected preferred note, got: %s", output)
	}
	if !strings.Contains(output, "Hotel ABC") {
		t.Errorf("expected hotel name, got: %s", output)
	}
}

// ---------------------------------------------------------------------------
// printWeekendTable
// ---------------------------------------------------------------------------

func TestPrintWeekendTable_SuccessTable(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()

	result := &trip.WeekendResult{
		Success: true,
		Origin:  "HEL",
		Month:   "2026-07",
		Nights:  2,
		Count:   1,
		Destinations: []trip.WeekendDestination{
			{
				Destination: "Tallinn",
				AirportCode: "TLL",
				FlightPrice: 50,
				HotelPrice:  80,
				Total:       130,
				Currency:    "EUR",
				Stops:       0,
				HotelName:   "Budget Inn",
			},
		},
	}
	err := printWeekendTable(context.Background(), "", result)
	w.Close()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)
	if !strings.Contains(buf.String(), "Tallinn") {
		t.Errorf("expected destination in output")
	}
}

// ---------------------------------------------------------------------------
// printSuggestTable — exercise insights branch
// ---------------------------------------------------------------------------

func TestPrintSuggestTable_WithInsights(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()

	result := &trip.SmartDateResult{
		Success:      true,
		Origin:       "HEL",
		Destination:  "BCN",
		Currency:     "EUR",
		AveragePrice: 150,
		CheapestDates: []trip.CheapDate{
			{Date: "2026-07-10", DayOfWeek: "Thu", Price: 100, Currency: "EUR"},
		},
		Insights: []trip.DateInsight{
			{Description: "Midweek is 30% cheaper"},
		},
	}
	err := printSuggestTable(context.Background(), "", result)
	w.Close()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)
	if !strings.Contains(buf.String(), "Midweek") {
		t.Errorf("expected insight in output")
	}
}

// ---------------------------------------------------------------------------
// printExploreTable
// ---------------------------------------------------------------------------

func TestPrintExploreTable_SuccessMinimal(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()

	result := &models.ExploreResult{
		Success: true,
		Count:   1,
		Destinations: []models.ExploreDestination{
			{
				CityName:    "Tallinn",
				AirportCode: "TLL",
				Price:       50,
				AirlineName: "Finnair",
				Stops:       0,
				Country:     "Estonia",
			},
		},
	}
	err := printExploreTable(context.Background(), "", result, "HEL")
	w.Close()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)
	if !strings.Contains(buf.String(), "Tallinn") {
		t.Errorf("expected Tallinn in output")
	}
}

func TestPrintExploreTable_NoCityName(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()

	result := &models.ExploreResult{
		Success: true,
		Count:   1,
		Destinations: []models.ExploreDestination{
			{AirportCode: "TLL", Price: 50, Stops: 2},
		},
	}
	err := printExploreTable(context.Background(), "", result, "HEL")
	w.Close()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)
	if !strings.Contains(buf.String(), "TLL") {
		t.Errorf("expected airport code fallback in output")
	}
}

// ---------------------------------------------------------------------------
// printMultiCityTable — with savings
// ---------------------------------------------------------------------------

func TestPrintMultiCityTable_WithSavings(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()

	result := &trip.MultiCityResult{
		Success:      true,
		HomeAirport:  "HEL",
		OptimalOrder: []string{"BCN", "ROM"},
		Permutations: 2,
		TotalCost:    500,
		Savings:      100,
		Currency:     "EUR",
		Segments: []trip.Segment{
			{From: "HEL", To: "BCN", Price: 150, Currency: "EUR"},
			{From: "BCN", To: "ROM", Price: 100, Currency: "EUR"},
			{From: "ROM", To: "HEL", Price: 250, Currency: "EUR"},
		},
	}
	err := printMultiCityTable(context.Background(), "", result)
	w.Close()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)
	if !strings.Contains(buf.String(), "Savings") {
		t.Errorf("expected savings row")
	}
}

// ---------------------------------------------------------------------------
// printDatesTable — round-trip column
// ---------------------------------------------------------------------------

func TestPrintDatesTable_WithReturnColumn(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()

	result := &models.DateSearchResult{
		Success:   true,
		DateRange: "2026-06-01 to 2026-06-30",
		TripType:  "round_trip",
		Count:     1,
		Dates: []models.DatePriceResult{
			{Date: "2026-06-10", Price: 100, Currency: "EUR", ReturnDate: "2026-06-17"},
		},
	}
	err := printDatesTable(context.Background(), "", result)
	w.Close()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)
	if !strings.Contains(buf.String(), "Return") {
		t.Errorf("expected Return column header for round_trip")
	}
}

// ---------------------------------------------------------------------------
// runWatchDaemon — timer loop via fake ticker
// ---------------------------------------------------------------------------

type fakeTickerCov struct {
	ch   chan time.Time
	done bool
}

func (f *fakeTickerCov) Chan() <-chan time.Time { return f.ch }
func (f *fakeTickerCov) Stop()                  { f.done = true }

func TestRunWatchDaemon_StopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var out bytes.Buffer

	tickCh := make(chan time.Time, 1)
	ft := &fakeTickerCov{ch: tickCh}

	cycleCount := 0
	runCycle := func(_ context.Context) (int, error) {
		cycleCount++
		cancel() // cancel immediately after first cycle
		return 1, nil
	}

	err := runWatchDaemon(ctx, &out, 1*time.Second, true, runCycle, func(_ time.Duration) watchDaemonTicker {
		return ft
	})

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if cycleCount < 1 {
		t.Errorf("expected at least 1 cycle, got %d", cycleCount)
	}
	if !strings.Contains(out.String(), "daemon") {
		t.Errorf("expected daemon messages in output, got: %s", out.String())
	}
}

func TestRunWatchDaemon_ZeroInterval(t *testing.T) {
	var out bytes.Buffer
	err := runWatchDaemon(context.Background(), &out, 0, false, func(_ context.Context) (int, error) {
		return 0, nil
	}, nil)
	if err == nil {
		t.Error("expected error for zero interval")
	}
}

func TestRunWatchDaemon_NilCycle(t *testing.T) {
	var out bytes.Buffer
	err := runWatchDaemon(context.Background(), &out, time.Second, false, nil, nil)
	if err == nil {
		t.Error("expected error for nil cycle")
	}
}

func TestRunWatchDaemon_CycleError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var out bytes.Buffer
	tickCh := make(chan time.Time, 1)
	ft := &fakeTickerCov{ch: tickCh}

	cycleCount := 0
	runCycle := func(_ context.Context) (int, error) {
		cycleCount++
		if cycleCount >= 1 {
			cancel()
		}
		return 0, fmt.Errorf("simulated error")
	}

	err := runWatchDaemon(ctx, &out, time.Second, true, runCycle, func(_ time.Duration) watchDaemonTicker {
		return ft
	})
	if err != nil {
		t.Errorf("daemon error should be logged not returned: %v", err)
	}
	if !strings.Contains(out.String(), "failed") {
		t.Errorf("expected failure message, got: %s", out.String())
	}
}

func TestRunWatchDaemon_NoActiveWatches(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var out bytes.Buffer
	tickCh := make(chan time.Time, 1)
	ft := &fakeTickerCov{ch: tickCh}

	runCycle := func(_ context.Context) (int, error) {
		cancel()
		return 0, nil
	}

	err := runWatchDaemon(ctx, &out, time.Second, true, runCycle, func(_ time.Duration) watchDaemonTicker {
		return ft
	})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "no active watches") {
		t.Errorf("expected no-watches message, got: %s", out.String())
	}
}

// ---------------------------------------------------------------------------
// Trips CLI — exercising cobra commands with temp HOME
// ---------------------------------------------------------------------------

func withTempHome(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	setTestHome(t, dir)
}

func TestTrips_CreateListShowDeleteFlow(t *testing.T) {
	withTempHome(t)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	// Create
	cmd := tripsCreateCmd()
	cmd.SetArgs([]string{"Summer 2026", "--tags", "vacation"})
	err := cmd.Execute()
	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()
	if !strings.Contains(output, "Created trip:") {
		t.Fatalf("expected 'Created trip:' in output, got: %s", output)
	}
	// Extract trip ID from "Created trip: trip_xxx"
	tripID := strings.TrimSpace(strings.TrimPrefix(output, "Created trip: "))

	// List
	old = os.Stdout
	r, w, _ = os.Pipe()
	os.Stdout = w
	err = runTripsList(false)
	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("list failed: %v", err)
	}

	buf.Reset()
	buf.ReadFrom(r)
	if !strings.Contains(buf.String(), "Summer 2026") {
		t.Errorf("expected trip name in list, got: %s", buf.String())
	}

	// List all
	old = os.Stdout
	r, w, _ = os.Pipe()
	os.Stdout = w
	err = runTripsList(true)
	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("list --all failed: %v", err)
	}

	buf.Reset()
	buf.ReadFrom(r)
	if !strings.Contains(buf.String(), "Summer 2026") {
		t.Errorf("expected trip name in list --all, got: %s", buf.String())
	}

	// Show
	old = os.Stdout
	_, w, _ = os.Pipe()
	os.Stdout = w
	showCmd := tripsShowCmd()
	showCmd.SetArgs([]string{tripID})
	err = showCmd.Execute()
	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("show failed: %v", err)
	}

	// Delete
	old = os.Stdout
	_, w, _ = os.Pipe()
	os.Stdout = w
	delCmd := tripsDeleteCmd()
	delCmd.SetArgs([]string{tripID})
	err = delCmd.Execute()
	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	// List after delete should be empty
	old = os.Stdout
	r, w, _ = os.Pipe()
	os.Stdout = w
	err = runTripsList(false)
	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("list after delete failed: %v", err)
	}
	buf.Reset()
	buf.ReadFrom(r)
	if !strings.Contains(buf.String(), "No active trips") {
		t.Errorf("expected empty list after delete, got: %s", buf.String())
	}
}

func TestTrips_AddLegAndBook(t *testing.T) {
	withTempHome(t)

	// Create a trip first
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	createCmd := tripsCreateCmd()
	createCmd.SetArgs([]string{"BCN Trip"})
	err := createCmd.Execute()
	w.Close()
	os.Stdout = old
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	var buf bytes.Buffer
	buf.ReadFrom(r)
	tripID := strings.TrimSpace(strings.TrimPrefix(buf.String(), "Created trip: "))

	// Add leg
	old = os.Stdout
	_, w, _ = os.Pipe()
	os.Stdout = w
	addLeg := tripsAddLegCmd()
	addLeg.SetArgs([]string{tripID, "flight", "--from", "HEL", "--to", "BCN", "--provider", "AY", "--price", "150", "--currency", "EUR", "--confirmed"})
	err = addLeg.Execute()
	w.Close()
	os.Stdout = old
	if err != nil {
		t.Fatalf("add-leg failed: %v", err)
	}

	// Book
	old = os.Stdout
	_, w, _ = os.Pipe()
	os.Stdout = w
	bookCmd := tripsBookCmd()
	bookCmd.SetArgs([]string{tripID, "--provider", "AY", "--ref", "ABC123"})
	err = bookCmd.Execute()
	w.Close()
	os.Stdout = old
	if err != nil {
		t.Fatalf("book failed: %v", err)
	}
}

func TestTrips_StatusEmpty(t *testing.T) {
	withTempHome(t)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	statusCmd := tripsStatusCmd()
	err := statusCmd.Execute()
	w.Close()
	os.Stdout = old
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	var buf bytes.Buffer
	buf.ReadFrom(r)
	if !strings.Contains(buf.String(), "No upcoming trips") {
		t.Errorf("expected no upcoming message, got: %s", buf.String())
	}
}

func TestTrips_AlertsEmpty(t *testing.T) {
	withTempHome(t)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	alertsCmd := tripsAlertsCmd()
	err := alertsCmd.Execute()
	w.Close()
	os.Stdout = old
	if err != nil {
		t.Fatalf("alerts failed: %v", err)
	}
	var buf bytes.Buffer
	buf.ReadFrom(r)
	// Just verify it ran without error
	_ = buf
}

// ---------------------------------------------------------------------------
// Prefs CLI — show/set with temp HOME
// ---------------------------------------------------------------------------

func TestPrefs_ShowDefault(t *testing.T) {
	withTempHome(t)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	err := runPrefsShow(nil, nil)
	w.Close()
	os.Stdout = old
	if err != nil {
		t.Fatalf("prefs show failed: %v", err)
	}
	var buf bytes.Buffer
	buf.ReadFrom(r)
	// Should output valid JSON
	if !strings.Contains(buf.String(), "{") {
		t.Errorf("expected JSON output, got: %s", buf.String())
	}
}

func TestPrefs_Set(t *testing.T) {
	withTempHome(t)

	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w
	err := runPrefsSet(nil, []string{"display_currency", "EUR"})
	w.Close()
	os.Stdout = old
	if err != nil {
		t.Fatalf("prefs set failed: %v", err)
	}
}

func TestPrefs_SetHomeAirports(t *testing.T) {
	withTempHome(t)

	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w
	err := runPrefsSet(nil, []string{"home_airports", "HEL,AMS"})
	w.Close()
	os.Stdout = old
	if err != nil {
		t.Fatalf("prefs set failed: %v", err)
	}
}

func TestPrefs_SetBool(t *testing.T) {
	withTempHome(t)

	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w
	err := runPrefsSet(nil, []string{"carry_on_only", "true"})
	w.Close()
	os.Stdout = old
	if err != nil {
		t.Fatalf("prefs set bool failed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Providers CLI — list with temp HOME (no providers configured)
// ---------------------------------------------------------------------------

func TestProviders_ListEmpty(t *testing.T) {
	withTempHome(t)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	cmd := providersListCmd()
	err := cmd.Execute()
	w.Close()
	os.Stdout = old
	if err != nil {
		t.Fatalf("providers list failed: %v", err)
	}
	var buf bytes.Buffer
	buf.ReadFrom(r)
	if !strings.Contains(buf.String(), "No providers") {
		t.Errorf("expected 'No providers' message, got: %s", buf.String())
	}
}

// ---------------------------------------------------------------------------
// colorizeVerdict — points_value.go
// ---------------------------------------------------------------------------

func TestColorizeVerdict_AllValues(t *testing.T) {
	tests := []struct {
		verdict string
	}{
		{"use points"},
		{"pay cash"},
		{"mixed"},
	}
	for _, tt := range tests {
		got := colorizeVerdict(tt.verdict)
		if got == "" {
			t.Errorf("colorizeVerdict(%q) returned empty", tt.verdict)
		}
	}
}

// ---------------------------------------------------------------------------
// printProgramList — points_value.go
// ---------------------------------------------------------------------------

func TestPrintProgramList_Table(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := printProgramList("table")
	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("printProgramList failed: %v", err)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)
	if buf.Len() == 0 {
		t.Error("expected non-empty program list")
	}
}

func TestPrintProgramList_JSON(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := printProgramList("json")
	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("printProgramList json failed: %v", err)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)
	if !strings.Contains(buf.String(), "[") {
		t.Error("expected JSON array output")
	}
}

// ---------------------------------------------------------------------------
// Watch CLI — list/remove/history with temp HOME
// ---------------------------------------------------------------------------

func TestWatch_ListEmpty(t *testing.T) {
	withTempHome(t)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	cmd := watchListCmd()
	err := cmd.Execute()
	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("watch list failed: %v", err)
	}
	var buf bytes.Buffer
	buf.ReadFrom(r)
	if !strings.Contains(buf.String(), "No active watches") {
		t.Errorf("expected 'No active watches' message, got: %s", buf.String())
	}
}

func TestWatch_RemoveNotFound(t *testing.T) {
	withTempHome(t)

	cmd := watchRemoveCmd()
	cmd.SetArgs([]string{"nonexistent"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for nonexistent watch")
	}
}

func TestWatch_HistoryNotFound(t *testing.T) {
	withTempHome(t)

	cmd := watchHistoryCmd()
	cmd.SetArgs([]string{"nonexistent"})
	err := cmd.Execute()
	// Should either error or show empty history
	_ = err
}

func TestWatch_AddListRemoveFlow(t *testing.T) {
	withTempHome(t)

	// Add a watch
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	addCmd := watchAddCmd()
	addCmd.SetArgs([]string{"HEL", "BCN", "--depart", "2026-06-15", "--return", "2026-06-22", "--below", "200"})
	err := addCmd.Execute()
	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("watch add failed: %v", err)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)

	// List should now show the watch
	old = os.Stdout
	r, w, _ = os.Pipe()
	os.Stdout = w
	listCmd := watchListCmd()
	err = listCmd.Execute()
	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("watch list failed: %v", err)
	}

	buf.Reset()
	buf.ReadFrom(r)
	output := buf.String()
	if strings.Contains(output, "No active watches") {
		t.Error("expected watches after adding one")
	}
	if !strings.Contains(output, "HEL") || !strings.Contains(output, "BCN") {
		t.Errorf("expected route in list, got: %s", output)
	}
}

// ---------------------------------------------------------------------------
// mcpConfigKey / trvlBinaryPath — mcp_install.go helpers
// ---------------------------------------------------------------------------

func TestMcpConfigKey_Values(t *testing.T) {
	tests := []struct {
		client string
		want   string
	}{
		{"claude", "mcpServers"},
		{"cursor", "mcpServers"},
		{"vscode", "servers"},
		{"zed", "context_servers"},
	}
	for _, tt := range tests {
		got := mcpConfigKey(tt.client)
		if got != tt.want {
			t.Errorf("mcpConfigKey(%q) = %q, want %q", tt.client, got, tt.want)
		}
	}
}

func TestTrvlBinaryPath_NonEmpty(t *testing.T) {
	p, _ := trvlBinaryPath()
	if p == "" {
		t.Error("expected non-empty binary path")
	}
}

// ---------------------------------------------------------------------------
// clientConfigPath — mcp_install.go
// ---------------------------------------------------------------------------

func TestClientConfigPath_AllKnownClients(t *testing.T) {
	clients := []string{"claude", "claude-code", "cursor", "windsurf", "codex"}
	for _, c := range clients {
		path, err := clientConfigPath(c)
		if err != nil {
			t.Errorf("clientConfigPath(%q) failed: %v", c, err)
		}
		if path == "" {
			t.Errorf("clientConfigPath(%q) returned empty path", c)
		}
	}
}

// ---------------------------------------------------------------------------
// providers status — with temp HOME
// ---------------------------------------------------------------------------

func TestProviders_StatusEmpty(t *testing.T) {
	withTempHome(t)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	cmd := providersStatusCmd()
	err := cmd.Execute()
	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("providers status failed: %v", err)
	}
	var buf bytes.Buffer
	buf.ReadFrom(r)
	_ = buf
}

// ---------------------------------------------------------------------------
// Trips with legs — status command exercises
// ---------------------------------------------------------------------------

func TestTrips_StatusWithTrip(t *testing.T) {
	withTempHome(t)

	// Create trip with a leg that has a future date
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	createCmd := tripsCreateCmd()
	createCmd.SetArgs([]string{"Upcoming"})
	_ = createCmd.Execute()
	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	tripID := strings.TrimSpace(strings.TrimPrefix(buf.String(), "Created trip: "))

	// Add a leg with future date
	futureDate := time.Now().AddDate(0, 0, 10).Format("2006-01-02T15:04")
	old = os.Stdout
	_, w, _ = os.Pipe()
	os.Stdout = w
	addLeg := tripsAddLegCmd()
	addLeg.SetArgs([]string{tripID, "flight", "--from", "HEL", "--to", "BCN", "--start", futureDate})
	_ = addLeg.Execute()
	w.Close()
	os.Stdout = old

	// Now status should show it
	old = os.Stdout
	r, w, _ = os.Pipe()
	os.Stdout = w
	statusCmd := tripsStatusCmd()
	_ = statusCmd.Execute()
	w.Close()
	os.Stdout = old

	buf.Reset()
	buf.ReadFrom(r)
	// Should show the upcoming trip
	if !strings.Contains(buf.String(), "Upcoming") {
		t.Errorf("expected trip name in status, got: %s", buf.String())
	}
}

// ---------------------------------------------------------------------------
// saveKeys — setup.go (exercises the keysPath() + saveKeysTo chain)
// ---------------------------------------------------------------------------

func TestSaveKeys_RoundTrip(t *testing.T) {
	withTempHome(t)

	keys := APIKeys{Kiwi: "test-kiwi-key"}
	err := saveKeys(keys)
	if err != nil {
		t.Fatalf("saveKeys failed: %v", err)
	}

	// Verify file was created
	loaded := loadExistingKeys()
	if loaded.Kiwi != "test-kiwi-key" {
		t.Errorf("expected kiwi key, got: %+v", loaded)
	}
}

// ---------------------------------------------------------------------------
// newRealWatchDaemonTicker / Chan — coverage for trivial methods
// ---------------------------------------------------------------------------

func TestNewRealWatchDaemonTicker(t *testing.T) {
	ticker := newRealWatchDaemonTicker(time.Hour)
	defer ticker.Stop()

	ch := ticker.Chan()
	if ch == nil {
		t.Error("expected non-nil channel")
	}
}

// ---------------------------------------------------------------------------
// shareTrip / shareLastSearch — with temp HOME
// ---------------------------------------------------------------------------

func TestShareTrip_Success(t *testing.T) {
	withTempHome(t)

	// Create a trip first
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	createCmd := tripsCreateCmd()
	createCmd.SetArgs([]string{"Share Test"})
	_ = createCmd.Execute()
	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	tripID := strings.TrimSpace(strings.TrimPrefix(buf.String(), "Created trip: "))

	// Add a leg
	old = os.Stdout
	_, w, _ = os.Pipe()
	os.Stdout = w
	addLeg := tripsAddLegCmd()
	addLeg.SetArgs([]string{tripID, "flight", "--from", "HEL", "--to", "BCN"})
	_ = addLeg.Execute()
	w.Close()
	os.Stdout = old

	// Share
	old = os.Stdout
	r, w, _ = os.Pipe()
	os.Stdout = w
	err := shareTrip(tripID, "markdown")
	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("shareTrip failed: %v", err)
	}

	buf.Reset()
	buf.ReadFrom(r)
	if !strings.Contains(buf.String(), "HEL") || !strings.Contains(buf.String(), "BCN") {
		t.Errorf("expected route in share output, got: %s", buf.String())
	}
}

func TestShareLastSearch_Success(t *testing.T) {
	withTempHome(t)

	// Save a last search
	ls := &LastSearch{
		Command:     "flights",
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-06-15",
		FlightPrice: 150,
		FlightCurrency: "EUR",
	}
	saveLastSearch(ls)

	// Share
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	err := shareLastSearch("markdown")
	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("shareLastSearch failed: %v", err)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)
	if !strings.Contains(buf.String(), "HEL") {
		t.Errorf("expected origin in share output, got: %s", buf.String())
	}
}

func TestShareLastSearch_NoData(t *testing.T) {
	withTempHome(t)

	err := shareLastSearch("markdown")
	if err == nil {
		t.Error("expected error when no last search saved")
	}
}

// ---------------------------------------------------------------------------
// runProvidersList — with temp HOME
// ---------------------------------------------------------------------------

func TestRunProvidersList_EmptyWithTempHome(t *testing.T) {
	withTempHome(t)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	cmd := providersListCmd()
	err := cmd.Execute()
	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("providers list failed: %v", err)
	}
	var buf bytes.Buffer
	buf.ReadFrom(r)
	if !strings.Contains(buf.String(), "No providers") {
		t.Errorf("expected 'No providers' message, got: %s", buf.String())
	}
}

// ---------------------------------------------------------------------------
// runProvidersStatus — with temp HOME
// ---------------------------------------------------------------------------

func TestRunProvidersStatus_Empty(t *testing.T) {
	withTempHome(t)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	cmd := providersStatusCmd()
	err := cmd.Execute()
	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("providers status failed: %v", err)
	}
	var buf bytes.Buffer
	buf.ReadFrom(r)
	_ = buf
}

// ---------------------------------------------------------------------------
// watchCheckCmd — runs check cycle with no watches
// ---------------------------------------------------------------------------

func TestWatch_CheckEmpty(t *testing.T) {
	withTempHome(t)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	cmd := watchCheckCmd()
	err := cmd.Execute()
	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("watch check failed: %v", err)
	}
	var buf bytes.Buffer
	buf.ReadFrom(r)
	if !strings.Contains(buf.String(), "No active watches") {
		t.Errorf("expected no-watches message, got: %s", buf.String())
	}
}

// ---------------------------------------------------------------------------
// watchRoomsCmd — exercises the room watch add path
// ---------------------------------------------------------------------------

func TestWatch_AddRoomWatch(t *testing.T) {
	withTempHome(t)

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = old })

	// Drain the pipe in a goroutine to prevent deadlock if output exceeds
	// the OS pipe buffer.
	outCh := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		buf.ReadFrom(r)
		outCh <- buf.String()
	}()

	cmd := watchRoomsCmd()
	cmd.SetArgs([]string{"Hotel Lutetia", "--checkin", "2026-06-15", "--checkout", "2026-06-18", "--keywords", "suite"})
	execErr := cmd.Execute()
	w.Close()
	os.Stdout = old

	if execErr != nil {
		t.Fatalf("watch rooms failed: %v", execErr)
	}

	out := <-outCh
	if !strings.Contains(out, "watch") {
		t.Errorf("expected output to contain %q, got: %s", "watch", out)
	}
}

// ---------------------------------------------------------------------------
// Command validation paths — exercise cobra RunE error returns
// ---------------------------------------------------------------------------

func TestFlightsCmd_InvalidCabin(t *testing.T) {
	cmd := flightsCmd()
	cmd.SetArgs([]string{"HEL", "BCN", "2026-06-15", "--cabin", "invalid"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for invalid cabin")
	}
}

func TestFlightsCmd_InvalidStops(t *testing.T) {
	cmd := flightsCmd()
	cmd.SetArgs([]string{"HEL", "BCN", "2026-06-15", "--stops", "invalid"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for invalid stops")
	}
}

func TestFlightsCmd_InvalidSort(t *testing.T) {
	cmd := flightsCmd()
	cmd.SetArgs([]string{"HEL", "BCN", "2026-06-15", "--sort", "invalid"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for invalid sort")
	}
}

func TestVisaCmd_MissingPassport(t *testing.T) {
	cmd := visaCmd()
	cmd.SetArgs([]string{"--destination", "JP"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when passport is missing")
	}
}

func TestVisaCmd_ListAll(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	cmd := visaCmd()
	cmd.SetArgs([]string{"--list"})
	err := cmd.Execute()
	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("visa --list failed: %v", err)
	}
	var buf bytes.Buffer
	buf.ReadFrom(r)
	if buf.Len() < 100 {
		t.Errorf("expected country list, got too short output: %d bytes", buf.Len())
	}
}

func TestVisaCmd_Lookup(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	cmd := visaCmd()
	cmd.SetArgs([]string{"--passport", "FI", "--destination", "JP"})
	err := cmd.Execute()
	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("visa lookup failed: %v", err)
	}
	var buf bytes.Buffer
	buf.ReadFrom(r)
	if !strings.Contains(buf.String(), "Visa") {
		t.Errorf("expected visa info in output, got: %s", buf.String())
	}
}

func TestExploreCmd_InvalidOrigin(t *testing.T) {
	cmd := exploreCmd()
	cmd.SetArgs([]string{"XX"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for invalid 2-letter IATA")
	}
}

func TestBaggageCmd_AllFlag(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	cmd := baggageCmd()
	cmd.SetArgs([]string{"--all"})
	err := cmd.Execute()
	w.Close()
	os.Stdout = old
	if err != nil {
		t.Fatalf("baggage --all failed: %v", err)
	}
	var buf bytes.Buffer
	buf.ReadFrom(r)
	if buf.Len() == 0 {
		t.Error("expected non-empty baggage list")
	}
}

func TestBaggageCmd_SingleAirline(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	cmd := baggageCmd()
	cmd.SetArgs([]string{"KL"})
	err := cmd.Execute()
	w.Close()
	os.Stdout = old
	if err != nil {
		t.Fatalf("baggage KL failed: %v", err)
	}
	var buf bytes.Buffer
	buf.ReadFrom(r)
	if buf.Len() == 0 {
		t.Error("expected non-empty baggage detail")
	}
}

func TestWeatherCmd_RequiresArg(t *testing.T) {
	cmd := weatherCmd()
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for missing arg")
	}
}

func TestLoungesCmd_RequiresArg(t *testing.T) {
	cmd := loungesCmd()
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for missing arg")
	}
}

func TestHacksCmd_RequiresOriginDest(t *testing.T) {
	cmd := hacksCmd()
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for missing args")
	}
}

func TestGridCmd_NoArgsFails(t *testing.T) {
	cmd := gridCmd()
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for missing args")
	}
}

func TestCalendarCmd_RequiresArgs(t *testing.T) {
	cmd := calendarCmd()
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for missing args")
	}
}

func TestAccomHackCmd_RequiresArg(t *testing.T) {
	cmd := accomHackCmd()
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for missing arg")
	}
}

func TestUpgradeCmd_Runs(t *testing.T) {
	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	cmd := upgradeCmd()
	cmd.SetArgs([]string{"--dry-run"})
	_ = cmd.Execute()
	w.Close()
	os.Stdout = old
}

func TestUpgradeCmd_Quiet(t *testing.T) {
	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	cmd := upgradeCmd()
	cmd.SetArgs([]string{"--quiet"})
	_ = cmd.Execute()
	w.Close()
	os.Stdout = old
}

// Import anchors to ensure all imports are used.
var _ tripwindow.Candidate
var _ watch.Watch
var _ nlsearch.Params
var _ optimizer.OptimizeResult
