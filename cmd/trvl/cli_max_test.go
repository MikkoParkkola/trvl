package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/preferences"
	"github.com/MikkoParkkola/trvl/internal/trips"
	"github.com/MikkoParkkola/trvl/internal/watch"
)

// ---------------------------------------------------------------------------
// formatCountdown — partial coverage
// ---------------------------------------------------------------------------

func TestFormatCountdown_Departed(t *testing.T) {
	if got := formatCountdown(-1 * time.Hour); got != "departed" {
		t.Errorf("formatCountdown(-1h) = %q, want %q", got, "departed")
	}
}

func TestFormatCountdown_MultipleDays(t *testing.T) {
	got := formatCountdown(72 * time.Hour)
	if got != "in 3 days" {
		t.Errorf("formatCountdown(72h) = %q, want %q", got, "in 3 days")
	}
}

func TestFormatCountdown_OneDay(t *testing.T) {
	d := 30 * time.Hour
	got := formatCountdown(d)
	if !strings.HasPrefix(got, "in 1 day") {
		t.Errorf("formatCountdown(30h) = %q, want prefix 'in 1 day'", got)
	}
}

func TestFormatCountdown_FewHours(t *testing.T) {
	got := formatCountdown(5 * time.Hour)
	if got != "in 5h" {
		t.Errorf("formatCountdown(5h) = %q, want %q", got, "in 5h")
	}
}

func TestFormatCountdown_Minutes(t *testing.T) {
	got := formatCountdown(45 * time.Minute)
	if got != "in 45m" {
		t.Errorf("formatCountdown(45m) = %q, want %q", got, "in 45m")
	}
}

// ---------------------------------------------------------------------------
// colorizeStatus — only partially covered
// ---------------------------------------------------------------------------

func TestColorizeStatus_AllCases(t *testing.T) {
	models.UseColor = false
	defer func() { models.UseColor = false }()

	tests := []struct {
		input string
		want  string
	}{
		{"planning", "planning"},
		{"booked", "booked"},
		{"in_progress", "in_progress"},
		{"completed", "completed"},
		{"cancelled", "cancelled"},
		{"unknown", "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := colorizeStatus(tt.input)
			if got != tt.want {
				t.Errorf("colorizeStatus(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestColorizeStatus_WithColor(t *testing.T) {
	models.UseColor = true
	defer func() { models.UseColor = false }()

	got := colorizeStatus("planning")
	if !strings.Contains(got, "planning") {
		t.Error("colorizeStatus(planning) with color should contain 'planning'")
	}

	got = colorizeStatus("booked")
	if !strings.Contains(got, "booked") {
		t.Error("colorizeStatus(booked) with color should contain 'booked'")
	}

	got = colorizeStatus("cancelled")
	if !strings.Contains(got, "cancelled") {
		t.Error("colorizeStatus(cancelled) with color should contain 'cancelled'")
	}
}

// ---------------------------------------------------------------------------
// nextLegSummary — partial coverage
// ---------------------------------------------------------------------------

func TestNextLegSummary_NoLegs(t *testing.T) {
	tr := trips.Trip{}
	got := nextLegSummary(tr)
	if got != "" {
		t.Errorf("nextLegSummary(no legs) = %q, want empty", got)
	}
}

func TestNextLegSummary_AllPast(t *testing.T) {
	tr := trips.Trip{
		Legs: []trips.TripLeg{
			{Type: "flight", From: "HEL", To: "BCN", StartTime: "2020-01-01T10:00"},
		},
	}
	got := nextLegSummary(tr)
	if got != "" {
		t.Errorf("nextLegSummary(all past) = %q, want empty", got)
	}
}

func TestNextLegSummary_FutureLegMax(t *testing.T) {
	future := time.Now().Add(48 * time.Hour).Format("2006-01-02T15:04")
	tr := trips.Trip{
		Legs: []trips.TripLeg{
			{Type: "flight", From: "HEL", To: "BCN", StartTime: future},
		},
	}
	got := nextLegSummary(tr)
	if got == "" {
		t.Fatal("nextLegSummary(future) should return non-empty")
	}
	if !strings.Contains(got, "flight HEL->BCN") {
		t.Errorf("nextLegSummary = %q, expected 'flight HEL->BCN'", got)
	}
}

func TestNextLegSummary_DateOnlyFormat(t *testing.T) {
	future := time.Now().Add(72 * time.Hour).Format("2006-01-02")
	tr := trips.Trip{
		Legs: []trips.TripLeg{
			{Type: "hotel", From: "BCN", To: "BCN", StartTime: future},
		},
	}
	got := nextLegSummary(tr)
	if got == "" {
		t.Fatal("nextLegSummary(date-only) should return non-empty")
	}
}

func TestNextLegSummary_EmptyStartTime(t *testing.T) {
	tr := trips.Trip{
		Legs: []trips.TripLeg{
			{Type: "flight", From: "HEL", To: "BCN"},
		},
	}
	got := nextLegSummary(tr)
	if got != "" {
		t.Errorf("nextLegSummary(empty start) = %q, want empty", got)
	}
}

func TestNextLegSummary_BadDateFormat(t *testing.T) {
	tr := trips.Trip{
		Legs: []trips.TripLeg{
			{Type: "flight", From: "HEL", To: "BCN", StartTime: "not-a-date"},
		},
	}
	got := nextLegSummary(tr)
	if got != "" {
		t.Errorf("nextLegSummary(bad date) = %q, want empty", got)
	}
}

// ---------------------------------------------------------------------------
// printLegLine — covers confirmed + provider + price branches
// ---------------------------------------------------------------------------

func TestPrintLegLine_ConfirmedMax(t *testing.T) {
	models.UseColor = false
	out := captureStdoutMax(t, func() {
		printLegLine(trips.TripLeg{
			Type:      "flight",
			From:      "HEL",
			To:        "BCN",
			Provider:  "Finnair",
			StartTime: "2026-07-01T08:00",
			Price:     199,
			Currency:  "EUR",
			Confirmed: true,
			Reference: "ABC123",
		})
	})
	if !strings.Contains(out, "confirmed") {
		t.Errorf("expected 'confirmed' in output, got %q", out)
	}
	if !strings.Contains(out, "Finnair") {
		t.Errorf("expected 'Finnair' in output, got %q", out)
	}
	if !strings.Contains(out, "EUR 199") {
		t.Errorf("expected 'EUR 199' in output, got %q", out)
	}
	if !strings.Contains(out, "ref:ABC123") {
		t.Errorf("expected 'ref:ABC123' in output, got %q", out)
	}
}

func TestPrintLegLine_PlannedMax(t *testing.T) {
	models.UseColor = false
	out := captureStdoutMax(t, func() {
		printLegLine(trips.TripLeg{
			Type: "hotel",
			From: "BCN",
			To:   "BCN",
		})
	})
	if !strings.Contains(out, "planned") {
		t.Errorf("expected 'planned' in output, got %q", out)
	}
}

// ---------------------------------------------------------------------------
// printTripDetail — covers tags, notes, bookings
// ---------------------------------------------------------------------------

func TestPrintTripDetail_FullMax(t *testing.T) {
	models.UseColor = false
	out := captureStdoutMax(t, func() {
		trip := &trips.Trip{
			ID:        "trip_abc",
			Name:      "Barcelona Trip",
			Status:    "planning",
			CreatedAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
			Tags:      []string{"summer", "beach"},
			Notes:     "Remember sunscreen",
			Legs: []trips.TripLeg{
				{Type: "flight", From: "HEL", To: "BCN", StartTime: "2026-07-01T08:00"},
			},
			Bookings: []trips.Booking{
				{Type: "flight", Provider: "Finnair", Reference: "XYZ", URL: "https://example.com"},
			},
		}
		printTripDetail(trip)
	})
	if !strings.Contains(out, "Barcelona Trip") {
		t.Error("expected trip name in output")
	}
	if !strings.Contains(out, "summer, beach") {
		t.Error("expected tags in output")
	}
	if !strings.Contains(out, "Remember sunscreen") {
		t.Error("expected notes in output")
	}
	if !strings.Contains(out, "Finnair") {
		t.Error("expected booking provider in output")
	}
	if !strings.Contains(out, "https://example.com") {
		t.Error("expected booking URL in output")
	}
}

// ---------------------------------------------------------------------------
// formatLastSearchMarkdown — partial coverage for all branches
// ---------------------------------------------------------------------------

func TestFormatLastSearchMarkdown_FullMax(t *testing.T) {
	ls := &LastSearch{
		Command:        "trip",
		Origin:         "HEL",
		Destination:    "BCN",
		DepartDate:     "2026-07-01",
		ReturnDate:     "2026-07-08",
		Nights:         7,
		FlightPrice:    199,
		FlightCurrency: "EUR",
		FlightAirline:  "Finnair",
		FlightStops:    0,
		HotelPrice:     560,
		HotelCurrency:  "EUR",
		HotelName:      "Hotel Arts",
		TotalPrice:     759,
		TotalCurrency:  "EUR",
	}
	md := formatLastSearchMarkdown(ls)

	if !strings.Contains(md, "**HEL -> BCN**") {
		t.Error("expected header with route")
	}
	if !strings.Contains(md, "7 nights") {
		t.Error("expected nights count")
	}
	if !strings.Contains(md, "Finnair") {
		t.Error("expected airline name")
	}
	if !strings.Contains(md, "nonstop") {
		t.Error("expected 'nonstop' for 0 stops")
	}
	if !strings.Contains(md, "Hotel Arts") {
		t.Error("expected hotel name")
	}
	if !strings.Contains(md, "**EUR 759**") {
		t.Error("expected total price")
	}
}

func TestFormatLastSearchMarkdown_OneStop(t *testing.T) {
	ls := &LastSearch{
		Origin:         "HEL",
		Destination:    "NRT",
		FlightPrice:    599,
		FlightCurrency: "EUR",
		FlightAirline:  "Lufthansa",
		FlightStops:    1,
	}
	md := formatLastSearchMarkdown(ls)
	if !strings.Contains(md, "1 stop") {
		t.Error("expected '1 stop' in output")
	}
}

func TestFormatLastSearchMarkdown_MultipleStops(t *testing.T) {
	ls := &LastSearch{
		Origin:         "HEL",
		Destination:    "SYD",
		FlightPrice:    899,
		FlightCurrency: "EUR",
		FlightAirline:  "Qatar",
		FlightStops:    2,
	}
	md := formatLastSearchMarkdown(ls)
	if !strings.Contains(md, "2 stops") {
		t.Error("expected '2 stops' in output")
	}
}

func TestFormatLastSearchMarkdown_NoOriginDest(t *testing.T) {
	ls := &LastSearch{
		Command: "deals",
	}
	md := formatLastSearchMarkdown(ls)
	if !strings.Contains(md, "**deals search**") {
		t.Errorf("expected command search header, got %q", md)
	}
}

func TestFormatLastSearchMarkdown_NoFlightNoHotel(t *testing.T) {
	ls := &LastSearch{
		Origin:      "HEL",
		Destination: "BCN",
	}
	md := formatLastSearchMarkdown(ls)
	// Should still have footer even without prices.
	if !strings.Contains(md, "trvl") {
		t.Error("expected footer in output")
	}
}

func TestFormatLastSearchMarkdown_HotelOnly(t *testing.T) {
	ls := &LastSearch{
		Origin:        "HEL",
		Destination:   "BCN",
		HotelPrice:    120,
		HotelCurrency: "EUR",
	}
	md := formatLastSearchMarkdown(ls)
	if !strings.Contains(md, "Hotel") {
		t.Error("expected Hotel row in output")
	}
	if strings.Contains(md, "Flight") {
		t.Error("should not contain Flight row when no flight price")
	}
}

// ---------------------------------------------------------------------------
// extractTripRoute
// ---------------------------------------------------------------------------

func TestExtractTripRoute_EmptyMax(t *testing.T) {
	tr := &trips.Trip{}
	origin, dest, depart, ret, nights := extractTripRoute(tr)
	if origin != "" || dest != "" || depart != "" || ret != "" || nights != 0 {
		t.Error("expected all empty for trip with no legs")
	}
}

func TestExtractTripRoute_SingleLeg(t *testing.T) {
	tr := &trips.Trip{
		Legs: []trips.TripLeg{
			{From: "HEL", To: "BCN", StartTime: "2026-07-01T08:00", EndTime: "2026-07-01T12:00"},
		},
	}
	origin, dest, depart, ret, _ := extractTripRoute(tr)
	if origin != "HEL" {
		t.Errorf("origin = %q, want HEL", origin)
	}
	if dest != "BCN" {
		t.Errorf("dest = %q, want BCN", dest)
	}
	if depart != "2026-07-01" {
		t.Errorf("depart = %q, want 2026-07-01", depart)
	}
	if ret != "2026-07-01" {
		t.Errorf("ret = %q, want 2026-07-01", ret)
	}
}

func TestExtractTripRoute_MultiLegNights(t *testing.T) {
	tr := &trips.Trip{
		Legs: []trips.TripLeg{
			{From: "HEL", To: "BCN", StartTime: "2026-07-01"},
			{From: "BCN", To: "HEL", StartTime: "2026-07-08"},
		},
	}
	_, _, _, _, nights := extractTripRoute(tr)
	if nights != 7 {
		t.Errorf("nights = %d, want 7", nights)
	}
}

func TestExtractTripRoute_LastLegNoEndTime(t *testing.T) {
	tr := &trips.Trip{
		Legs: []trips.TripLeg{
			{From: "HEL", To: "BCN", StartTime: "2026-07-01"},
			{From: "BCN", To: "HEL", StartTime: "2026-07-08"},
		},
	}
	_, _, _, ret, _ := extractTripRoute(tr)
	if ret != "2026-07-08" {
		t.Errorf("ret = %q, want 2026-07-08 (fallback to StartTime)", ret)
	}
}

// ---------------------------------------------------------------------------
// formatDateCompact
// ---------------------------------------------------------------------------

func TestFormatDateCompact_ValidMax(t *testing.T) {
	got := formatDateCompact("2026-07-15")
	if got != "Jul 15" {
		t.Errorf("formatDateCompact = %q, want %q", got, "Jul 15")
	}
}

func TestFormatDateCompact_InvalidMax(t *testing.T) {
	got := formatDateCompact("not-a-date")
	if got != "not-a-date" {
		t.Errorf("formatDateCompact(invalid) = %q, want passthrough", got)
	}
}

// ---------------------------------------------------------------------------
// flightWarnings
// ---------------------------------------------------------------------------

func TestFlightWarnings_WithWarnings(t *testing.T) {
	f := models.FlightResult{Warnings: []string{"Long layover", "Red-eye"}}
	got := flightWarnings(f)
	if got != "Long layover; Red-eye" {
		t.Errorf("flightWarnings = %q, want joined warnings", got)
	}
}

func TestFlightWarnings_SelfConnectMax(t *testing.T) {
	f := models.FlightResult{SelfConnect: true}
	got := flightWarnings(f)
	if !strings.Contains(got, "Self-connect") {
		t.Errorf("flightWarnings(self-connect) = %q, want Self-connect", got)
	}
}

func TestFlightWarnings_None(t *testing.T) {
	f := models.FlightResult{}
	got := flightWarnings(f)
	if got != "" {
		t.Errorf("flightWarnings(none) = %q, want empty", got)
	}
}

// ---------------------------------------------------------------------------
// flightProviderLabel
// ---------------------------------------------------------------------------

func TestFlightProviderLabel_AllCases(t *testing.T) {
	tests := []struct {
		provider string
		want     string
	}{
		{"", ""},
		{"google_flights", "Google"},
		{"kiwi", "Kiwi"},
		{"GOOGLE_FLIGHTS", "Google"},
		{"  kiwi  ", "Kiwi"},
		{"momondo", "momondo"},
	}
	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			got := flightProviderLabel(models.FlightResult{Provider: tt.provider})
			if got != tt.want {
				t.Errorf("flightProviderLabel(%q) = %q, want %q", tt.provider, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// hackTypeLabel
// ---------------------------------------------------------------------------

func TestHackTypeLabel_AllTypes(t *testing.T) {
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
		{"hidden_city", "hidden city"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := hackTypeLabel(tt.input)
			if got != tt.want {
				t.Errorf("hackTypeLabel(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// colorizeVisaStatus
// ---------------------------------------------------------------------------

func TestColorizeVisaStatus_AllStatusesMax(t *testing.T) {
	models.UseColor = false
	tests := []struct {
		status string
		label  string
		want   string
	}{
		{"visa-free", "Visa free", "Visa free"},
		{"freedom-of-movement", "Freedom", "Freedom"},
		{"visa-on-arrival", "On arrival", "On arrival"},
		{"e-visa", "E-visa", "E-visa"},
		{"visa-required", "Required", "Required"},
		{"unknown", "Unknown", "Unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			got := colorizeVisaStatus(tt.status, tt.label)
			if got != tt.want {
				t.Errorf("colorizeVisaStatus(%q) = %q, want %q", tt.status, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// resolveString
// ---------------------------------------------------------------------------

func TestResolveString_FlagWinsMax(t *testing.T) {
	got := resolveString(false, "flag", "existing", "fallback")
	if got != "flag" {
		t.Errorf("resolveString = %q, want 'flag'", got)
	}
}

func TestResolveString_ExistingWins(t *testing.T) {
	got := resolveString(false, "", "existing", "fallback")
	if got != "existing" {
		t.Errorf("resolveString = %q, want 'existing'", got)
	}
}

func TestResolveString_Fallback(t *testing.T) {
	got := resolveString(false, "", "", "fallback")
	if got != "fallback" {
		t.Errorf("resolveString = %q, want 'fallback'", got)
	}
}

func TestResolveString_NonInteractiveFallback(t *testing.T) {
	got := resolveString(true, "", "", "fallback")
	if got != "fallback" {
		t.Errorf("resolveString(nonInteractive) = %q, want 'fallback'", got)
	}
}

// ---------------------------------------------------------------------------
// coalesce
// ---------------------------------------------------------------------------

func TestCoalesce_FirstNonEmptyMax(t *testing.T) {
	if got := coalesce("a", "b"); got != "a" {
		t.Errorf("coalesce('a','b') = %q, want 'a'", got)
	}
}

func TestCoalesce_SecondWhenFirstEmpty(t *testing.T) {
	if got := coalesce("", "b"); got != "b" {
		t.Errorf("coalesce('','b') = %q, want 'b'", got)
	}
}

func TestCoalesce_BothEmpty(t *testing.T) {
	if got := coalesce("", ""); got != "" {
		t.Errorf("coalesce('','') = %q, want empty", got)
	}
}

// ---------------------------------------------------------------------------
// setupTimestamp
// ---------------------------------------------------------------------------

func TestSetupTimestamp_RFC3339Max(t *testing.T) {
	ts := setupTimestamp()
	_, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		t.Errorf("setupTimestamp() = %q, not valid RFC3339: %v", ts, err)
	}
}

// ---------------------------------------------------------------------------
// hotelSourceLabels
// ---------------------------------------------------------------------------

func TestHotelSourceLabels_EmptyMax(t *testing.T) {
	h := models.HotelResult{}
	got := hotelSourceLabels(h)
	if got != "" {
		t.Errorf("hotelSourceLabels(empty) = %q, want empty", got)
	}
}

func TestHotelSourceLabels_Multiple(t *testing.T) {
	h := models.HotelResult{
		Sources: []models.PriceSource{
			{Provider: "google_hotels"},
			{Provider: "booking"},
			{Provider: "google_hotels"}, // duplicate
		},
	}
	got := hotelSourceLabels(h)
	if !strings.Contains(got, "Google") {
		t.Error("expected Google in labels")
	}
	if !strings.Contains(got, "Booking") {
		t.Error("expected Booking in labels")
	}
	// Should deduplicate.
	if strings.Count(got, "Google") > 1 {
		t.Error("expected deduplicated Google")
	}
}

// ---------------------------------------------------------------------------
// formatRoomsTable
// ---------------------------------------------------------------------------

func TestFormatRoomsTable_NoRoomsMax(t *testing.T) {
	models.UseColor = false
	out := captureStdoutMax(t, func() {
		_ = formatRoomsTable(&hotels.RoomAvailability{
			HotelID:  "hotel_123",
			CheckIn:  "2026-07-01",
			CheckOut: "2026-07-08",
		})
	})
	if !strings.Contains(out, "No room types found") {
		t.Errorf("expected 'No room types found', got %q", out)
	}
}

func TestFormatRoomsTable_WithRoomsMax(t *testing.T) {
	models.UseColor = false
	out := captureStdoutMax(t, func() {
		_ = formatRoomsTable(&hotels.RoomAvailability{
			Name:     "Beach Resort",
			CheckIn:  "2026-07-01",
			CheckOut: "2026-07-08",
			Rooms: []hotels.RoomType{
				{Name: "Standard", Price: 100, Currency: "EUR", MaxGuests: 2, Provider: "booking", Amenities: []string{"wifi", "AC"}},
				{Name: "Suite", Price: 250, Currency: "EUR", MaxGuests: 4, Provider: "booking"},
			},
		})
	})
	if !strings.Contains(out, "Beach Resort") {
		t.Error("expected hotel name in output")
	}
	if !strings.Contains(out, "Standard") {
		t.Error("expected room name in output")
	}
	if !strings.Contains(out, "Cheapest") {
		t.Error("expected cheapest summary")
	}
}

// ---------------------------------------------------------------------------
// formatTripMarkdown
// ---------------------------------------------------------------------------

func TestFormatTripMarkdown_WithLegs(t *testing.T) {
	tr := &trips.Trip{
		Name: "Summer Trip",
		Legs: []trips.TripLeg{
			{
				Type:      "flight",
				From:      "HEL",
				To:        "BCN",
				StartTime: "2026-07-01",
				EndTime:   "2026-07-01",
				Price:     199,
				Currency:  "EUR",
				Provider:  "Finnair",
			},
			{
				Type:      "hotel",
				From:      "BCN",
				To:        "BCN",
				StartTime: "2026-07-01",
				EndTime:   "2026-07-08",
				Price:     560,
				Currency:  "EUR",
			},
		},
	}
	md := formatTripMarkdown(tr)
	if !strings.Contains(md, "**HEL -> BCN**") {
		t.Error("expected route header")
	}
	if !strings.Contains(md, "7 nights") {
		t.Error("expected nights count")
	}
	if !strings.Contains(md, "Finnair") {
		t.Error("expected provider in table")
	}
	if !strings.Contains(md, "Total") {
		t.Error("expected total row")
	}
}

func TestFormatTripMarkdown_NoLegsMax(t *testing.T) {
	tr := &trips.Trip{Name: "Empty Trip"}
	md := formatTripMarkdown(tr)
	if !strings.Contains(md, "**Empty Trip**") {
		t.Error("expected trip name as header")
	}
}

func TestFormatTripMarkdown_NoPrices(t *testing.T) {
	tr := &trips.Trip{
		Legs: []trips.TripLeg{
			{From: "HEL", To: "BCN", StartTime: "2026-07-01"},
		},
	}
	md := formatTripMarkdown(tr)
	// Should not contain price table.
	if strings.Contains(md, "| Price |") {
		t.Error("should not contain price table when no prices")
	}
}

// ---------------------------------------------------------------------------
// saveLastSearch / loadLastSearch
// ---------------------------------------------------------------------------

func TestSaveLoadLastSearch_RoundTripMax(t *testing.T) {
	// Override HOME to use temp dir.
	dir := t.TempDir()
	setTestHome(t, dir)

	ls := &LastSearch{
		Command:     "flights",
		Origin:      "HEL",
		Destination: "BCN",
	}
	saveLastSearch(ls)

	loaded, err := loadLastSearch()
	if err != nil {
		t.Fatalf("loadLastSearch: %v", err)
	}
	if loaded.Origin != "HEL" {
		t.Errorf("Origin = %q, want HEL", loaded.Origin)
	}
	if loaded.Destination != "BCN" {
		t.Errorf("Destination = %q, want BCN", loaded.Destination)
	}
}

// ---------------------------------------------------------------------------
// runWatchDaemon — additional uncovered branches
// ---------------------------------------------------------------------------

func TestRunWatchDaemon_NilRunCycle(t *testing.T) {
	err := runWatchDaemon(context.Background(), &bytes.Buffer{}, time.Hour, true, nil, nil)
	if err == nil {
		t.Fatal("expected error for nil runCycle")
	}
	if !strings.Contains(err.Error(), "check function") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunWatchDaemon_NilTicker(t *testing.T) {
	// With nil newTicker, should use default.
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately so daemon exits

	var buf bytes.Buffer
	err := runWatchDaemon(ctx, &buf, time.Hour, false, func(context.Context) (int, error) {
		return 0, nil
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunWatchDaemon_NoRunNow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ticker := &stubWatchDaemonTicker{ch: make(chan time.Time, 1)}
	var buf bytes.Buffer
	runs := 0
	done := make(chan error, 1)

	go func() {
		done <- runWatchDaemon(ctx, &buf, time.Hour, false, func(context.Context) (int, error) {
			runs++
			cancel()
			return 0, nil
		}, func(time.Duration) watchDaemonTicker {
			return ticker
		})
	}()

	ticker.ch <- time.Now()

	if err := <-done; err != nil {
		t.Fatalf("runWatchDaemon: %v", err)
	}
	if runs != 1 {
		t.Errorf("runs = %d, want 1 (no runNow)", runs)
	}
}

func TestRunWatchDaemon_ZeroWatches(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ticker := &stubWatchDaemonTicker{ch: make(chan time.Time, 1)}
	var buf bytes.Buffer
	done := make(chan error, 1)

	go func() {
		done <- runWatchDaemon(ctx, &buf, time.Hour, true, func(context.Context) (int, error) {
			cancel()
			return 0, nil
		}, func(time.Duration) watchDaemonTicker {
			return ticker
		})
	}()

	if err := <-done; err != nil {
		t.Fatalf("runWatchDaemon: %v", err)
	}
	if !strings.Contains(buf.String(), "no active watches") {
		t.Errorf("expected 'no active watches' in output, got %q", buf.String())
	}
}

// ---------------------------------------------------------------------------
// watchDaemonCmd — covers the 50% uncovered cmd setup
// ---------------------------------------------------------------------------

func TestWatchDaemonCmd_FlagsMax(t *testing.T) {
	cmd := watchDaemonCmd()
	if cmd.Use != "daemon" {
		t.Errorf("Use = %q, want 'daemon'", cmd.Use)
	}
	f := cmd.Flags()
	if _, err := f.GetDuration("every"); err != nil {
		t.Errorf("missing --every flag: %v", err)
	}
	if _, err := f.GetBool("run-now"); err != nil {
		t.Errorf("missing --run-now flag: %v", err)
	}
}

// ---------------------------------------------------------------------------
// maybeShowFlightHackTips — deeper path coverage
// ---------------------------------------------------------------------------

func TestMaybeShowFlightHackTips_SingleFlight(t *testing.T) {
	// Not a live test — just exercises the pure logic paths.
	result := &models.FlightSearchResult{
		Success: true,
		Flights: []models.FlightResult{
			{Price: 500, Currency: "EUR", Legs: []models.FlightLeg{
				{AirlineCode: "AY"},
			}},
		},
	}
	// Should not panic.
	out := captureStdoutMax(t, func() {
		maybeShowFlightHackTips(context.Background(), []string{"HEL"}, []string{"NRT"}, "2026-07-01", "2026-07-08", 1, result)
	})
	_ = out // Just verify no panic.
}

// ---------------------------------------------------------------------------
// formatHotelsTable — covers the 81.8% to push higher
// ---------------------------------------------------------------------------

func TestFormatHotelsTable_NoHotels(t *testing.T) {
	models.UseColor = false
	out := captureStdoutMax(t, func() {
		_ = formatHotelsTable(context.Background(), "", &models.HotelSearchResult{})
	})
	if !strings.Contains(out, "No hotels found") {
		t.Errorf("expected 'No hotels found', got %q", out)
	}
}

func TestFormatHotelsTable_WithHotels(t *testing.T) {
	models.UseColor = false
	result := &models.HotelSearchResult{
		Count:          2,
		TotalAvailable: 5,
		Hotels: []models.HotelResult{
			{
				Name:        "Cheap Hotel",
				Stars:       3,
				Rating:      7.5,
				ReviewCount: 100,
				Price:       50,
				Currency:    "EUR",
				Amenities:   []string{"wifi"},
				Sources:     []models.PriceSource{{Provider: "booking"}},
			},
			{
				Name:        "Fancy Hotel",
				Stars:       5,
				Rating:      9.2,
				ReviewCount: 500,
				Price:       200,
				Currency:    "EUR",
				Amenities:   []string{"pool", "spa", "gym"},
				Sources:     []models.PriceSource{{Provider: "trivago"}},
				Savings:     30,
				CheapestSource: "booking",
			},
		},
	}
	out := captureStdoutMax(t, func() {
		_ = formatHotelsTable(context.Background(), "", result)
	})
	if !strings.Contains(out, "Showing 2 of 5 hotels") {
		t.Error("expected 'Showing 2 of 5 hotels'")
	}
	if !strings.Contains(out, "Cheap Hotel") {
		t.Error("expected hotel name")
	}
	if !strings.Contains(out, "Cheapest") {
		t.Error("expected cheapest summary")
	}
}

// ---------------------------------------------------------------------------
// applyPreference — cover more preference keys
// ---------------------------------------------------------------------------

func TestApplyPreference_PreferredDistrictsMax(t *testing.T) {
	p := &preferences.Preferences{}
	err := applyPreference(p, "preferred_districts", "Barcelona=Eixample,Born")
	if err != nil {
		t.Fatalf("applyPreference: %v", err)
	}
}

func TestApplyPreference_PreferredDistrictsDelete(t *testing.T) {
	p := &preferences.Preferences{}
	// First add.
	_ = applyPreference((*prefsWrapper)(p), "preferred_districts", "Barcelona=Eixample")
	// Then delete.
	err := applyPreference((*prefsWrapper)(p), "preferred_districts", "Barcelona=")
	if err != nil {
		t.Fatalf("applyPreference delete: %v", err)
	}
}

func TestApplyPreference_InvalidFormat(t *testing.T) {
	p := &preferences.Preferences{}
	err := applyPreference((*prefsWrapper)(p), "preferred_districts", "noequalssign")
	if err == nil {
		t.Error("expected error for invalid format")
	}
}

func TestApplyPreference_EmptyCity(t *testing.T) {
	p := &preferences.Preferences{}
	err := applyPreference((*prefsWrapper)(p), "preferred_districts", "=Eixample")
	if err == nil {
		t.Error("expected error for empty city")
	}
}

func TestApplyPreference_UnknownKey_Max(t *testing.T) {
	p := &preferences.Preferences{}
	err := applyPreference((*prefsWrapper)(p), "nonexistent_key", "value")
	if err == nil {
		t.Error("expected error for unknown key")
	}
}

// ---------------------------------------------------------------------------
// watchHistoryCmd — cover the history display path
// ---------------------------------------------------------------------------

func TestWatchHistoryCmd_Found(t *testing.T) {
	dir := t.TempDir()
	store := watch.NewStore(dir)
	w := watch.Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-01",
	}
	id, err := store.Add(w)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordPrice(id, 200, "EUR"); err != nil {
		t.Fatal(err)
	}

	// The watch history command uses DefaultStore so we can't easily test the
	// CLI path, but we can verify the store operations work.
	history := store.History(id)
	if len(history) != 1 {
		t.Errorf("expected 1 history entry, got %d", len(history))
	}
}

// ---------------------------------------------------------------------------
// maybeShowAccomHackTip — date parsing edge cases
// ---------------------------------------------------------------------------

func TestMaybeShowAccomHackTip_BadDates(t *testing.T) {
	// Should not panic with bad dates.
	maybeShowAccomHackTip(context.Background(), "Helsinki", "invalid", "2026-07-08", "EUR", 2)
	maybeShowAccomHackTip(context.Background(), "Helsinki", "2026-07-01", "invalid", "EUR", 2)
}

func TestMaybeShowAccomHackTip_ShortStayMax(t *testing.T) {
	// 2-night stay — should not trigger tip.
	out := captureStdoutMax(t, func() {
		maybeShowAccomHackTip(context.Background(), "Helsinki", "2026-07-01", "2026-07-03", "EUR", 2)
	})
	if strings.Contains(out, "Tip") {
		t.Error("should not show tip for short stay")
	}
}

func TestMaybeShowAccomHackTip_EmptyDatesMax(t *testing.T) {
	// Should return immediately with empty dates.
	maybeShowAccomHackTip(context.Background(), "Helsinki", "", "", "EUR", 2)
}

// ---------------------------------------------------------------------------
// captureStdout helper (already exists in other test files, but we need it here)
// ---------------------------------------------------------------------------

func captureStdoutMax(t *testing.T, fn func()) string {
	t.Helper()

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w

	fn()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	return buf.String()
}

// Adapter so we can call applyPreference with the correct type.
// The preferences package may use a different struct layout, so we need to
// check the actual type used. Let's verify what applyPreference expects.

type prefsWrapper = preferences.Preferences

// ---------------------------------------------------------------------------
// openBrowser — cover the function to exercise its branches
// ---------------------------------------------------------------------------

func TestOpenBrowser_EmptyURLMax(t *testing.T) {
	err := openBrowser("")
	if err == nil {
		t.Error("expected error for empty URL")
	}
}

// ---------------------------------------------------------------------------
// saveLastSearch edge case — nil pointer
// ---------------------------------------------------------------------------

// TestSaveLastSearch_NilDoesNotPanic removed: saveLastSearch(nil) intentionally
// panics because nil *LastSearch has no meaningful state to persist.

// ---------------------------------------------------------------------------
// formatWatchDates (extra branches to push coverage)
// ---------------------------------------------------------------------------

func TestFormatWatchDates_FixedDepartOnly(t *testing.T) {
	w := watch.Watch{DepartDate: "2026-07-01"}
	got := formatWatchDates(w)
	if got != "2026-07-01" {
		t.Errorf("formatWatchDates = %q, want %q", got, "2026-07-01")
	}
}

// ---------------------------------------------------------------------------
// secureTempPath
// ---------------------------------------------------------------------------

func TestSecureTempPath_Unique(t *testing.T) {
	dir := t.TempDir()
	p1, err := secureTempPath(dir, "test-")
	if err != nil {
		t.Fatal(err)
	}
	p2, err := secureTempPath(dir, "test-")
	if err != nil {
		t.Fatal(err)
	}
	if p1 == p2 {
		t.Error("expected unique paths")
	}
}

// ---------------------------------------------------------------------------
// upgradeCmd branches
// ---------------------------------------------------------------------------

func TestUpgradeCmd_DryRun(t *testing.T) {
	cmd := upgradeCmd()
	if cmd.Use != "upgrade" {
		t.Errorf("Use = %q, want 'upgrade'", cmd.Use)
	}
	// Check flags exist.
	if _, err := cmd.Flags().GetBool("dry-run"); err != nil {
		t.Errorf("missing --dry-run flag: %v", err)
	}
	if _, err := cmd.Flags().GetBool("quiet"); err != nil {
		t.Errorf("missing --quiet flag: %v", err)
	}
}

// ---------------------------------------------------------------------------
// mcpConfigKey
// ---------------------------------------------------------------------------

func TestMcpConfigKey_VSCode(t *testing.T) {
	if got := mcpConfigKey("vscode"); got != "servers" {
		t.Errorf("mcpConfigKey(vscode) = %q, want %q", got, "servers")
	}
}

func TestMcpConfigKey_Zed(t *testing.T) {
	if got := mcpConfigKey("zed"); got != "context_servers" {
		t.Errorf("mcpConfigKey(zed) = %q, want %q", got, "context_servers")
	}
}

func TestMcpConfigKey_Default(t *testing.T) {
	if got := mcpConfigKey("claude-desktop"); got != "mcpServers" {
		t.Errorf("mcpConfigKey(claude-desktop) = %q, want %q", got, "mcpServers")
	}
}

// ---------------------------------------------------------------------------
// clientConfigPath — cover more clients
// ---------------------------------------------------------------------------

func TestClientConfigPath_AllClients(t *testing.T) {
	clients := []string{
		"cursor", "claude-code", "windsurf", "vscode", "gemini",
		"amazon-q", "lm-studio",
	}
	for _, c := range clients {
		t.Run(c, func(t *testing.T) {
			path, err := clientConfigPath(c)
			if err != nil {
				t.Errorf("clientConfigPath(%q) error: %v", c, err)
			}
			if path == "" {
				t.Errorf("clientConfigPath(%q) = empty", c)
			}
		})
	}
}

func TestClientConfigPath_UnknownMax(t *testing.T) {
	_, err := clientConfigPath("unknown-editor")
	if err == nil {
		t.Error("expected error for unknown client")
	}
}

func TestClientConfigPath_Codex(t *testing.T) {
	path, err := clientConfigPath("codex")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(path, "config.toml") {
		t.Errorf("codex path should contain config.toml, got %q", path)
	}
}

func TestClientConfigPath_Zed(t *testing.T) {
	path, err := clientConfigPath("zed")
	if err != nil {
		t.Fatal(err)
	}
	if path == "" {
		t.Error("expected non-empty path for zed")
	}
}

// ---------------------------------------------------------------------------
// convertRoundedDisplayAmounts
// ---------------------------------------------------------------------------

func TestConvertRoundedDisplayAmounts_SameCurrency(t *testing.T) {
	val := 100.0
	got := convertRoundedDisplayAmounts(context.Background(), "EUR", "EUR", 0, &val)
	if got != "EUR" {
		t.Errorf("same currency should return source, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// saveKeysTo — permission and round-trip
// ---------------------------------------------------------------------------

func TestSaveKeysTo_CreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	path := fmt.Sprintf("%s/deep/nested/keys.json", dir)

	keys := APIKeys{Kiwi: "test-key"}
	if err := saveKeysTo(path, keys); err != nil {
		t.Fatalf("saveKeysTo: %v", err)
	}

	// Verify file permissions.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if runtime.GOOS != "windows" {
		if info.Mode().Perm() != 0o600 {
			t.Errorf("file mode = %o, want 0600", info.Mode().Perm())
		}
	}
}
