package main

import (
	"context"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/trip"
)

// The built-in Booking search tags its quotes "booking.com"; the provider
// runtime writes "booking". Both are Booking (#808).
func TestHotelSourceLabel_BothBookingTags(t *testing.T) {
	for _, tag := range []string{"booking", "booking.com"} {
		if got := hotelSourceLabel(tag); got != "Booking" {
			t.Errorf("hotelSourceLabel(%q) = %q, want Booking", tag, got)
		}
	}
}

// TestPrintTripPlan_CoverageBulletsMatchNote: a 403 is a refusal in the note,
// so the bullet under it must not call it a rate limit (#807).
func TestPrintTripPlan_CoverageBulletsMatchNote(t *testing.T) {
	models.UseColor = false
	result := &trip.PlanResult{
		Success: true, Origin: "HEL", Destination: "VIE",
		DepartDate: "2026-11-18", ReturnDate: "2026-11-20", Nights: 2, Guests: 1,
		Hotels: []trip.PlanHotel{{Name: "Hotel X", PerNight: 80, Total: 160, Currency: "EUR"}},
		HotelProviders: []models.ProviderStatus{
			{ID: "google_hotels", Name: "Google Hotels", Status: models.StatusCheckedHit, Results: 5},
			{ID: "trivago", Name: "Trivago", Status: models.StatusRateLimited, Error: "trivago: HTTP 403: forbidden"},
		},
		FlightProviders: []models.ProviderStatus{
			{ID: "kiwi", Name: "Kiwi", Status: models.StatusCheckedHit, Results: 2},
			{ID: "google_flights", Name: "Google Flights", Status: models.StatusRateLimited, Error: "HTTP 503 Service Unavailable"},
		},
		Summary: trip.PlanSummary{GrandTotal: 160, Currency: "EUR"},
	}
	result.HotelCoverage = models.ComputeCompleteness(result.HotelProviders)
	result.FlightCoverage = models.ComputeCompleteness(result.FlightProviders)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out := captureStdout(t, func() {
		if err := printTripPlan(ctx, "", result); err != nil {
			t.Errorf("printTripPlan: %v", err)
		}
	})
	for _, want := range []string{"• Trivago: refused the request", "• Google Flights: returned a server error"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n--- got ---\n%s", want, out)
		}
	}
	if strings.Contains(out, "rate-limited") {
		t.Errorf("a 403 and a 503 are called rate-limited\n--- got ---\n%s", out)
	}
}

// The trip hotel table shows a provider label, never a raw source tag (#808).
func TestPrintTripPlan_SourceColumnShowsLabel(t *testing.T) {
	models.UseColor = false
	result := &trip.PlanResult{
		Success: true, Origin: "HEL", Destination: "VIE",
		DepartDate: "2026-11-18", ReturnDate: "2026-11-20", Nights: 2, Guests: 1,
		Hotels: []trip.PlanHotel{
			{Name: "Grand Pods", PerNight: 44, Total: 88, Currency: "EUR", PriceConfidence: models.PriceConfidenceRoomLevel, PriceSource: "google_hotels"},
			{Name: "Hotel Y", PerNight: 90, Total: 180, Currency: "EUR", PriceConfidence: models.PriceConfidenceVerified, PriceSource: "booking.com"},
		},
		Summary: trip.PlanSummary{GrandTotal: 88, Currency: "EUR"},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out := captureStdout(t, func() {
		if err := printTripPlan(ctx, "", result); err != nil {
			t.Errorf("printTripPlan: %v", err)
		}
	})
	for _, want := range []string{"Google · room rate", "Booking · verified"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n--- got ---\n%s", want, out)
		}
	}
	for _, raw := range []string{"google_hotels", "booking.com"} {
		if strings.Contains(out, raw) {
			t.Errorf("raw tag %q printed\n--- got ---\n%s", raw, out)
		}
	}
}

// A real rate limit keeps its retry hint in the trip bullets; a refusal does
// not get one (#817 review).
func TestPrintTripPlan_RateLimitBulletKeepsRetryHint(t *testing.T) {
	models.UseColor = false
	result := &trip.PlanResult{
		Success: true, Origin: "HEL", Destination: "VIE",
		DepartDate: "2026-11-18", ReturnDate: "2026-11-20", Nights: 2, Guests: 1,
		Hotels: []trip.PlanHotel{{Name: "Hotel X", PerNight: 80, Total: 160, Currency: "EUR"}},
		HotelProviders: []models.ProviderStatus{
			{ID: "google_hotels", Name: "Google Hotels", Status: models.StatusCheckedHit, Results: 5},
			{ID: "hostelworld", Name: "Hostelworld", Status: models.StatusRateLimited, Error: "HTTP 429 Too Many Requests"},
			{ID: "trivago", Name: "Trivago", Status: models.StatusRateLimited, Error: "trivago: HTTP 403: forbidden"},
		},
		Summary: trip.PlanSummary{GrandTotal: 160, Currency: "EUR"},
	}
	result.HotelCoverage = models.ComputeCompleteness(result.HotelProviders)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out := captureStdout(t, func() {
		if err := printTripPlan(ctx, "", result); err != nil {
			t.Errorf("printTripPlan: %v", err)
		}
	})
	if want := "• Hostelworld: rate-limited the request — retryable, try again shortly"; !strings.Contains(out, want) {
		t.Errorf("output missing %q\n--- got ---\n%s", want, out)
	}
	if strings.Contains(out, "• Trivago: refused the request — retryable") {
		t.Errorf("a refusal got the retry hint\n--- got ---\n%s", out)
	}
}
