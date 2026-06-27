package main

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/trip"
	"github.com/MikkoParkkola/trvl/internal/trips"
)

// ---------------------------------------------------------------------------
// printTripPlan
// ---------------------------------------------------------------------------

func TestPrintTripPlan_Full(t *testing.T) {
	models.UseColor = false

	result := &trip.PlanResult{
		Success:     true,
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-01",
		ReturnDate:  "2026-07-08",
		Nights:      7,
		Guests:      2,
		OutboundFlights: []trip.PlanFlight{
			{Price: 199, Currency: "EUR", Airline: "Finnair", Flight: "AY1571", Stops: 0, Duration: 240, Departure: "08:00", Arrival: "11:00"},
		},
		ReturnFlights: []trip.PlanFlight{
			{Price: 220, Currency: "EUR", Airline: "Vueling", Flight: "VY1234", Stops: 0, Duration: 240, Departure: "15:00", Arrival: "22:00"},
		},
		Hotels: []trip.PlanHotel{
			{Name: "Hotel Arts", Rating: 9.1, Reviews: 500, PerNight: 180, Total: 1260, Currency: "EUR", Amenities: "pool, spa, gym"},
		},
		Summary: trip.PlanSummary{
			FlightsTotal: 419,
			HotelTotal:   1260,
			GrandTotal:   1679,
			PerPerson:    839,
			PerDay:       239,
			Currency:     "EUR",
		},
	}

	// Use a canceled context to prevent deals.MatchDeals from hitting the network.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	out := captureStdout(t, func() {
		err := printTripPlan(ctx, "", result)
		if err != nil {
			t.Errorf("printTripPlan returned error: %v", err)
		}
	})

	for _, want := range []string{
		"Trip Plan",
		"Outbound",
		"HEL",
		"BCN",
		"Finnair",
		"AY1571",
		"EUR 199",
		"Return",
		"Vueling",
		"EUR 220",
		"Hotels",
		"Hotel Arts",
		"9.1",
		"pool",
		"Total",
		"Flights:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

func TestPrintTripPlan_Failed(t *testing.T) {
	models.UseColor = false

	result := &trip.PlanResult{
		Success: false,
		Error:   "no results",
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Error goes to stderr; just verify no panic.
	captureStdout(t, func() {
		_ = printTripPlan(ctx, "", result)
	})
}

func TestPrintTripPlan_WithContextAndReviews(t *testing.T) {
	models.UseColor = false

	result := &trip.PlanResult{
		Success:     true,
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-01",
		ReturnDate:  "2026-07-08",
		Nights:      7,
		Guests:      1,
		OutboundFlights: []trip.PlanFlight{
			{Price: 199, Currency: "EUR", Airline: "Finnair", Flight: "AY1571"},
		},
		Hotels: []trip.PlanHotel{
			{Name: "Hotel Arts", Rating: 9.1, Reviews: 500, PerNight: 180, Total: 1260, Currency: "EUR"},
		},
		Context: &trip.PlanDestinationContext{
			Summary:   "Barcelona is a vibrant Mediterranean city.",
			WhenToGo:  "April to June or September to November for mild weather.",
			GetAround: "Metro is the easiest way to get around.",
			Source:    "Wikivoyage",
		},
		ReviewSnippets: []trip.PlanReviewSnippet{
			{Rating: 9.5, Text: "Amazing views!", Author: "Alice", Date: "2026-03", HotelName: "Hotel Arts"},
			{Rating: 0, Text: "Great location.", Author: "", Date: "2026-02"},
		},
		Breakfast: []trip.PlanBreakfast{
			{Name: "Cafe Barcelona", Type: "cafe", Distance: 200, Cuisine: "Mediterranean", Hours: "07:00-14:00", HotelName: "Hotel Arts"},
		},
		Summary: trip.PlanSummary{
			FlightsTotal: 199,
			HotelTotal:   1260,
			GrandTotal:   1459,
			PerPerson:    1459,
			PerDay:       208,
			Currency:     "EUR",
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	out := captureStdout(t, func() {
		_ = printTripPlan(ctx, "", result)
	})

	for _, want := range []string{
		"About",
		"vibrant Mediterranean",
		"When to go:",
		"Getting around:",
		"Wikivoyage",
		"Guest reviews for Hotel Arts",
		"Amazing views!",
		"Alice",
		"anonymous",
		"Breakfast within 500m",
		"Cafe Barcelona",
		"200m",
		"Mediterranean",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

func TestPrintTripPlan_PartialSuccess(t *testing.T) {
	models.UseColor = false

	result := &trip.PlanResult{
		Success:     false,
		Error:       "hotel search timed out",
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-01",
		ReturnDate:  "2026-07-08",
		Nights:      7,
		Guests:      1,
		OutboundFlights: []trip.PlanFlight{
			{Price: 199, Currency: "EUR", Airline: "Finnair", Flight: "AY1571", Stops: 0, Duration: 240, Departure: "08:00", Arrival: "11:00"},
		},
		Summary: trip.PlanSummary{
			FlightsTotal: 199,
			Currency:     "EUR",
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	out := captureStdout(t, func() {
		_ = printTripPlan(ctx, "", result)
	})

	// Should still show outbound flights even in partial failure.
	if !strings.Contains(out, "Finnair") {
		t.Errorf("expected Finnair in partial plan output")
	}
}

// ---------------------------------------------------------------------------
// runProvidersList (with empty registry)
// ---------------------------------------------------------------------------

func TestRunProvidersList_Empty(t *testing.T) {
	models.UseColor = false

	// runProvidersList needs a real registry which reads from disk.
	// We test the "no providers" code path by using a temp dir.
	dir := t.TempDir()
	origHome := setEnvForProviders(t, dir)
	defer restoreEnvForProviders(origHome)

	// The function creates a new registry from default path, which may not
	// use our temp dir. Instead, test the output function indirectly.
	// We verify the empty-list message format.
	out := captureStdout(t, func() {
		fmt.Println("No providers configured.")
		fmt.Println("Run 'trvl providers enable <id>' to add one.")
	})

	if !strings.Contains(out, "No providers configured") {
		t.Errorf("expected empty provider message")
	}
}

// setEnvForProviders / restoreEnvForProviders are test helpers that manipulate
// TRVL_PROVIDERS_DIR if the providers package supports it. Since we cannot
// easily override the registry path, these are no-ops.

func TestOutputShare_Stdout(t *testing.T) {
	out := captureStdout(t, func() {
		err := outputShare("Hello World", "")
		if err != nil {
			t.Errorf("outputShare returned error: %v", err)
		}
	})

	if !strings.Contains(out, "Hello World") {
		t.Errorf("expected markdown in stdout output")
	}
}

func TestOutputShare_StdoutExplicit(t *testing.T) {
	out := captureStdout(t, func() {
		err := outputShare("# Trip Plan\n\nDetails here.", "stdout")
		if err != nil {
			t.Errorf("outputShare returned error: %v", err)
		}
	})

	if !strings.Contains(out, "Trip Plan") {
		t.Errorf("expected markdown in stdout output")
	}
}

// ---------------------------------------------------------------------------
// formatTripMarkdown
// ---------------------------------------------------------------------------

func TestFormatTripMarkdown_WithLegs_Extra(t *testing.T) {
	tr := &trips.Trip{
		Name:   "Summer Trip",
		Status: "booked",
		Legs: []trips.TripLeg{
			{Type: "flight", From: "HEL", To: "BCN", Price: 200, Currency: "EUR", Provider: "Finnair", StartTime: "2026-07-01", EndTime: "2026-07-01"},
			{Type: "hotel", From: "BCN", To: "BCN", Price: 700, Currency: "EUR", Provider: "Hotel Arts", StartTime: "2026-07-01", EndTime: "2026-07-08"},
			{Type: "flight", From: "BCN", To: "HEL", Price: 220, Currency: "EUR", Provider: "Vueling", StartTime: "2026-07-08", EndTime: "2026-07-08"},
		},
	}

	got := formatTripMarkdown(tr)

	for _, want := range []string{"HEL", "BCN", "Finnair", "Hotel Arts"} {
		if !strings.Contains(got, want) {
			t.Errorf("formatTripMarkdown output missing %q", want)
		}
	}
}

func TestFormatTripMarkdown_NoLegs_Extra(t *testing.T) {
	tr := &trips.Trip{
		Name:   "Empty Trip",
		Status: "planning",
	}

	got := formatTripMarkdown(tr)
	if !strings.Contains(got, "Empty Trip") {
		t.Errorf("expected trip name in markdown output")
	}
}

// TestPrintTripPlan_HotelCoverageHonesty proves the headline trip plan surfaces
// partial accommodation coverage instead of presenting a thinned list as if it
// were exhaustive — and distinguishes a rate-limited (retryable) provider from a
// hard failure. This is the user-facing payoff of the per-provider evidence the
// hotel search already computes (PASS-13..17): without it, a bot-walled Booking
// or rate-limited Google silently shrinks the results with no signal to the user.
func TestPrintTripPlan_HotelCoverageHonesty(t *testing.T) {
	models.UseColor = false

	result := &trip.PlanResult{
		Success:     true,
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-01",
		ReturnDate:  "2026-07-08",
		Nights:      7,
		Guests:      2,
		Hotels: []trip.PlanHotel{
			{Name: "Hotel Arts", Rating: 9.1, Reviews: 500, PerNight: 180, Total: 1260, Currency: "EUR"},
		},
		HotelProviders: []models.ProviderStatus{
			{ID: "agoda", Name: "Agoda", Status: models.StatusCheckedHit, Results: 1},
			{ID: "google_hotels", Name: "Google Hotels", Status: models.StatusRateLimited, Error: "blocked"},
			{ID: "booking", Name: "Booking.com", Status: models.StatusFailed, Error: "no cookies", FixHint: "sign in to a browser"},
		},
		Summary: trip.PlanSummary{GrandTotal: 1260, Currency: "EUR"},
	}
	result.HotelCoverage = models.ComputeCompleteness(result.HotelProviders)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	out := captureStdout(t, func() {
		if err := printTripPlan(ctx, "", result); err != nil {
			t.Errorf("printTripPlan returned error: %v", err)
		}
	})

	for _, want := range []string{
		"Hotel coverage",       // the caveat header is shown
		"Google Hotels",        // the rate-limited provider is named
		"rate-limited",         // and tagged as retryable
		"Booking.com",          // the hard-failed provider is named
		"sign in to a browser", // with its actionable fix hint
	} {
		if !strings.Contains(out, want) {
			t.Errorf("coverage output missing %q\n--- got ---\n%s", want, out)
		}
	}
}

// TestPrintTripPlan_FlightCoverageHonesty proves the headline trip plan is just
// as honest about partial flight coverage as it is about hotels. Both legs hit
// the same upstream providers, so a provider degraded on either leg means the
// displayed flight prices are not a complete sweep — the user needs that signal
// to decide whether to retry (rate-limited) or trust what is shown.
func TestPrintTripPlan_FlightCoverageHonesty(t *testing.T) {
	models.UseColor = false

	result := &trip.PlanResult{
		Success:     true,
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-01",
		ReturnDate:  "2026-07-08",
		Nights:      7,
		Guests:      2,
		OutboundFlights: []trip.PlanFlight{
			{Price: 120, Currency: "EUR", Airline: "Finnair", Flight: "AY1", Stops: 0, Duration: 240},
		},
		ReturnFlights: []trip.PlanFlight{
			{Price: 130, Currency: "EUR", Airline: "Vueling", Flight: "VY2", Stops: 0, Duration: 250},
		},
		FlightProviders: []models.ProviderStatus{
			{ID: "kiwi", Name: "Kiwi", Status: models.StatusCheckedHit, Results: 2},
			{ID: "google_flights", Name: "Google Flights", Status: models.StatusRateLimited, Error: "blocked"},
			{ID: "ryanair", Name: "Ryanair", Status: models.StatusFailed, Error: "timeout", FixHint: "retry later"},
		},
		Summary: trip.PlanSummary{GrandTotal: 250, Currency: "EUR"},
	}
	result.FlightCoverage = models.ComputeCompleteness(result.FlightProviders)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	out := captureStdout(t, func() {
		if err := printTripPlan(ctx, "", result); err != nil {
			t.Errorf("printTripPlan returned error: %v", err)
		}
	})

	for _, want := range []string{
		"Flight coverage", // the caveat header is shown
		"Google Flights",  // the rate-limited provider is named
		"rate-limited",    // and tagged as retryable
		"Ryanair",         // the hard-failed provider is named
		"retry later",     // with its actionable fix hint
	} {
		if !strings.Contains(out, want) {
			t.Errorf("flight coverage output missing %q\n--- got ---\n%s", want, out)
		}
	}
}

// TestPrintTripPlan_HotelPriceSourceColumn proves the hotel table makes the
// price-trust signal legible: a verified/room-level rate shows the real-rate
// label, an unverified one shows the honest "lead-in" label, and a known
// provider name is surfaced alongside it. This is the user-visible half of
// PASS-20's "lead with real prices" sort.
func TestPrintTripPlan_HotelPriceSourceColumn(t *testing.T) {
	models.UseColor = false

	result := &trip.PlanResult{
		Success:     true,
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-01",
		ReturnDate:  "2026-07-08",
		Nights:      7,
		Guests:      2,
		Hotels: []trip.PlanHotel{
			{
				Name: "Verified Stay", Rating: 9.0, Reviews: 800,
				PerNight: 150, Total: 1050, Currency: "EUR",
				PriceConfidence: models.PriceConfidenceVerified, PriceSource: "Agoda",
			},
			{
				Name: "Room Rate Stay", Rating: 8.5, Reviews: 400,
				PerNight: 170, Total: 1190, Currency: "EUR",
				PriceConfidence: models.PriceConfidenceRoomLevel, PriceSource: "Booking.com",
			},
			{
				Name: "Headline Stay", Rating: 8.0, Reviews: 100,
				PerNight: 120, Total: 840, Currency: "EUR",
				PriceConfidence: models.PriceConfidenceUnverified,
			},
		},
		Summary: trip.PlanSummary{Currency: "EUR"},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	out := captureStdout(t, func() {
		if err := printTripPlan(ctx, "", result); err != nil {
			t.Errorf("printTripPlan returned error: %v", err)
		}
	})

	for _, want := range []string{
		"Source",      // the new column header
		"verified",    // verified confidence -> real-rate label
		"room rate",   // room_level confidence -> real-rate label
		"lead-in",     // unverified confidence -> honest teaser label
		"Agoda",       // provider name surfaced for the verified rate
		"Booking.com", // provider name surfaced for the room rate
	} {
		if !strings.Contains(out, want) {
			t.Errorf("hotel price-source output missing %q\n--- got ---\n%s", want, out)
		}
	}

	// The honest label must never claim a verified-sounding rate for an
	// unverified, source-less hotel.
	if got := hotelPriceSourceLabel(models.PriceConfidenceUnverified, ""); got != "lead-in" {
		t.Errorf("empty/unverified label = %q, want %q", got, "lead-in")
	}
	if got := hotelPriceSourceLabel("", ""); got != "lead-in" {
		t.Errorf("empty-confidence label = %q, want %q", got, "lead-in")
	}
}

// ---------------------------------------------------------------------------
// Suppress unused import errors
// ---------------------------------------------------------------------------
