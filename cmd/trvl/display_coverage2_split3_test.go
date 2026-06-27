package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/trip"
	"github.com/MikkoParkkola/trvl/internal/trips"
)

func TestPrintTripDetail_Full(t *testing.T) {
	models.UseColor = false

	tr := &trips.Trip{
		ID:        "trip-001",
		Name:      "Helsinki to Prague",
		Status:    "booked",
		CreatedAt: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		Tags:      []string{"business", "spring"},
		Notes:     "Conference trip.",
		Legs: []trips.TripLeg{
			{
				Type:      "flight",
				From:      "HEL",
				To:        "PRG",
				Provider:  "Finnair",
				StartTime: "2026-04-15T08:00",
				Price:     250,
				Currency:  "EUR",
				Confirmed: true,
				Reference: "AY123",
			},
			{
				Type:      "hotel",
				From:      "Prague",
				To:        "Prague",
				Provider:  "Czech Inn",
				StartTime: "2026-04-15",
				Confirmed: false,
			},
		},
		Bookings: []trips.Booking{
			{Type: "flight", Provider: "Finnair", Reference: "AY123", URL: "https://finnair.com/booking/AY123"},
		},
	}

	out := captureStdout(t, func() {
		printTripDetail(tr)
	})

	for _, want := range []string{
		"Helsinki to Prague",
		"Status: booked",
		"trip-001",
		"2026-04-01",
		"business",
		"spring",
		"Conference trip",
		"Legs:",
		"flight",
		"HEL->PRG",
		"Finnair",
		"EUR 250",
		"confirmed",
		"ref:AY123",
		"hotel",
		"planned",
		"Bookings:",
		"finnair.com",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

func TestPrintTripDetail_Minimal(t *testing.T) {
	models.UseColor = false

	tr := &trips.Trip{
		ID:        "trip-002",
		Name:      "Quick Trip",
		Status:    "planning",
		CreatedAt: time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC),
	}

	out := captureStdout(t, func() {
		printTripDetail(tr)
	})

	if !strings.Contains(out, "Quick Trip") {
		t.Errorf("expected trip name in output")
	}
	// No Legs or Bookings sections.
	if strings.Contains(out, "Legs:") {
		t.Errorf("should not show Legs section when empty")
	}
}

// ---------------------------------------------------------------------------
// printLegLine
// ---------------------------------------------------------------------------

func TestPrintLegLine_Confirmed(t *testing.T) {
	models.UseColor = false

	leg := trips.TripLeg{
		Type:      "flight",
		From:      "HEL",
		To:        "AMS",
		Provider:  "KLM",
		StartTime: "2026-07-01T08:00",
		Price:     199,
		Currency:  "EUR",
		Confirmed: true,
		Reference: "KL1571",
	}

	out := captureStdout(t, func() {
		printLegLine(leg)
	})

	for _, want := range []string{"flight", "HEL->AMS", "KLM", "EUR 199", "confirmed", "ref:KL1571"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

func TestPrintLegLine_Planned(t *testing.T) {
	models.UseColor = false

	leg := trips.TripLeg{
		Type: "train",
		From: "Prague",
		To:   "Vienna",
	}

	out := captureStdout(t, func() {
		printLegLine(leg)
	})

	if !strings.Contains(out, "planned") {
		t.Errorf("expected 'planned' status")
	}
	if !strings.Contains(out, "Prague->Vienna") {
		t.Errorf("expected route")
	}
}

// ---------------------------------------------------------------------------
// nextLegSummary
// ---------------------------------------------------------------------------

func TestNextLegSummary_FutureLeg(t *testing.T) {
	models.UseColor = false

	future := time.Now().Add(72 * time.Hour).Format("2006-01-02T15:04")
	tr := trips.Trip{
		Legs: []trips.TripLeg{
			{Type: "flight", From: "HEL", To: "BCN", StartTime: future},
		},
	}

	got := nextLegSummary(tr)
	if got == "" {
		t.Error("expected non-empty summary for future leg")
	}
	if !strings.Contains(got, "flight") {
		t.Errorf("expected 'flight' in summary, got: %s", got)
	}
	if !strings.Contains(got, "HEL->BCN") {
		t.Errorf("expected 'HEL->BCN' in summary, got: %s", got)
	}
}

func TestNextLegSummary_PastLeg(t *testing.T) {
	past := time.Now().Add(-72 * time.Hour).Format("2006-01-02T15:04")
	tr := trips.Trip{
		Legs: []trips.TripLeg{
			{Type: "flight", From: "HEL", To: "BCN", StartTime: past},
		},
	}

	got := nextLegSummary(tr)
	if got != "" {
		t.Errorf("expected empty summary for past leg, got: %s", got)
	}
}

func TestNextLegSummary_NoStartTime(t *testing.T) {
	tr := trips.Trip{
		Legs: []trips.TripLeg{
			{Type: "flight", From: "HEL", To: "BCN"},
		},
	}

	got := nextLegSummary(tr)
	if got != "" {
		t.Errorf("expected empty summary when no start time, got: %s", got)
	}
}

func TestNextLegSummary_DateOnly(t *testing.T) {
	// Test with date-only format (no time).
	future := time.Now().Add(72 * time.Hour).Format("2006-01-02")
	tr := trips.Trip{
		Legs: []trips.TripLeg{
			{Type: "train", From: "Prague", To: "Vienna", StartTime: future},
		},
	}

	got := nextLegSummary(tr)
	if got == "" {
		t.Error("expected non-empty summary for date-only future leg")
	}
}

// ---------------------------------------------------------------------------
// formatCountdown
// ---------------------------------------------------------------------------

func TestFormatCountdown_ExtraCases(t *testing.T) {
	tests := []struct {
		name string
		d    time.Duration
		want string
	}{
		{"negative", -1 * time.Hour, "departed"},
		{"minutes", 30 * time.Minute, "in 30m"},
		{"hours", 5 * time.Hour, "in 5h"},
		{"one day", 36 * time.Hour, "in 1 day 12h"},
		{"multiple days", 72 * time.Hour, "in 3 days"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatCountdown(tt.d)
			if got != tt.want {
				t.Errorf("formatCountdown(%v) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// colorizeStatus
// ---------------------------------------------------------------------------

func TestColorizeStatus_AllValues(t *testing.T) {
	models.UseColor = false

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
			if !strings.Contains(got, tt.want) {
				t.Errorf("colorizeStatus(%q) = %q, want to contain %q", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// printJSON
// ---------------------------------------------------------------------------

func TestPrintJSON(t *testing.T) {
	data := map[string]interface{}{
		"key": "value",
		"num": 42,
	}

	out := captureStdout(t, func() {
		err := printJSON(data)
		if err != nil {
			t.Errorf("printJSON returned error: %v", err)
		}
	})

	if !strings.Contains(out, `"key": "value"`) {
		t.Errorf("expected JSON output, got: %s", out)
	}
	if !strings.Contains(out, `"num": 42`) {
		t.Errorf("expected num in JSON, got: %s", out)
	}
}

// ---------------------------------------------------------------------------
// looksLikeGoogleHotelID
// ---------------------------------------------------------------------------

func TestLooksLikeGoogleHotelID_ExtraCases(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"/g/11b6d4_v_4", true},
		{"ChIJy7MSZP0LkkYRZw2dDekQP78", true},
		{"entity:12345", true},
		{"Hotel Lutetia", false},
		{"Prague", false},
		{"  /g/11test  ", true},
		{"ChIJ spaces here", true}, // ChIJ prefix always matches
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := looksLikeGoogleHotelID(tt.input)
			if got != tt.want {
				t.Errorf("looksLikeGoogleHotelID(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// printMultiCityTable with currency conversion (empty targetCurrency path)
// ---------------------------------------------------------------------------

func TestPrintMultiCityTable_WithCurrencyConversion(t *testing.T) {
	models.UseColor = false

	result := &trip.MultiCityResult{
		Success:      true,
		HomeAirport:  "HEL",
		OptimalOrder: []string{"BCN"},
		Segments: []trip.Segment{
			{From: "HEL", To: "BCN", Price: 120, Currency: "EUR"},
			{From: "BCN", To: "HEL", Price: 110, Currency: "EUR"},
		},
		TotalCost:    230,
		Currency:     "EUR",
		Permutations: 1,
	}

	// With empty targetCurrency, should not convert.
	out := captureStdout(t, func() {
		_ = printMultiCityTable(context.Background(), "", result)
	})

	if !strings.Contains(out, "EUR 230") {
		t.Errorf("expected EUR 230 in output, got: %s", out)
	}
}

// ---------------------------------------------------------------------------
// formatDestinationCard — coverage for no-weather / no-holidays branches
// ---------------------------------------------------------------------------

func TestFormatDestinationCard_NoWeatherNoHolidays(t *testing.T) {
	models.UseColor = false

	info := &models.DestinationInfo{
		Location: "Empty City",
		Country: models.CountryInfo{
			Name: "Emptyland",
			Code: "EL",
		},
	}

	out := captureStdout(t, func() {
		err := formatDestinationCard(info)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	if !strings.Contains(out, "Empty City") {
		t.Errorf("expected location name")
	}
}
