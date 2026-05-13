package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/trip"
	"github.com/MikkoParkkola/trvl/internal/tripwindow"
)

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
	_ = w.Close()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
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
	_ = w.Close()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
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
	_ = w.Close()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
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
	_ = w.Close()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
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
	_ = w.Close()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
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
	_ = w.Close()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
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
	_ = w.Close()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	if !strings.Contains(buf.String(), "Return") {
		t.Errorf("expected Return column header for round_trip")
	}
}

// ---------------------------------------------------------------------------
// runWatchDaemon — timer loop via fake ticker
// ---------------------------------------------------------------------------

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

func TestTrips_CreateListShowDeleteFlow(t *testing.T) {
	withTempHome(t)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	// Create
	cmd := tripsCreateCmd()
	cmd.SetArgs([]string{"Summer 2026", "--tags", "vacation"})
	err := cmd.Execute()
	_ = w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
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
	_ = w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("list failed: %v", err)
	}

	buf.Reset()
	_, _ = buf.ReadFrom(r)
	if !strings.Contains(buf.String(), "Summer 2026") {
		t.Errorf("expected trip name in list, got: %s", buf.String())
	}

	// List all
	old = os.Stdout
	r, w, _ = os.Pipe()
	os.Stdout = w
	err = runTripsList(true)
	_ = w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("list --all failed: %v", err)
	}

	buf.Reset()
	_, _ = buf.ReadFrom(r)
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
	_ = w.Close()
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
	_ = w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	// List after delete should be empty
	old = os.Stdout
	r, w, _ = os.Pipe()
	os.Stdout = w
	err = runTripsList(false)
	_ = w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("list after delete failed: %v", err)
	}
	buf.Reset()
	_, _ = buf.ReadFrom(r)
	if !strings.Contains(buf.String(), "No active trips") {
		t.Errorf("expected empty list after delete, got: %s", buf.String())
	}
}
