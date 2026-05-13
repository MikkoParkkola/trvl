package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/preferences"
	"github.com/MikkoParkkola/trvl/internal/trips"
)

func captureStdoutMax(t *testing.T, fn func()) string {
	t.Helper()

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w

	fn()

	_ = w.Close()
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
