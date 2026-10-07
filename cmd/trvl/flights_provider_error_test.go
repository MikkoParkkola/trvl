package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

func TestFlightSearchErrorNamesFailedProviders(t *testing.T) {
	base := errors.New("all flight providers failed")
	result := &models.FlightSearchResult{ProviderStatuses: []models.ProviderStatus{
		{ID: "google_flights", Name: "Google Flights", Status: models.StatusRateLimited, Error: "google flights blocked the request (HTTP 403): " + models.ErrRateLimited.Error()},
		{ID: "kiwi", Name: "Kiwi", Status: models.StatusOK},
	}}

	err := flightSearchError(result, base)
	if !errors.Is(err, base) {
		t.Fatalf("the original error must stay wrapped: %v", err)
	}
	if msg := err.Error(); !strings.Contains(msg, "Google Flights: blocked") || strings.Contains(msg, "Kiwi:") {
		t.Fatalf("error must list only failed providers with reasons, got %q", msg)
	}
	if got := flightSearchError(nil, base); got != base {
		t.Fatalf("nil result must return the error unchanged, got %v", got)
	}
}

// MIK-8032: with --format json, a search where every provider failed still
// prints the result so scripts can read provider_statuses, and still fails.
func TestReportFlightFailureJSONPrintsStatuses(t *testing.T) {
	base := errors.New("all flight providers failed")
	result := &models.FlightSearchResult{Error: base.Error(), ProviderStatuses: []models.ProviderStatus{
		{ID: "google_flights", Name: "Google Flights", Status: models.StatusRateLimited, Error: "HTTP 403 Forbidden"},
	}}

	var out bytes.Buffer
	err := reportFlightFailure(&out, "json", result, base)
	if !errors.Is(err, base) {
		t.Fatalf("the failure must still be returned so the command exits non-zero, got %v", err)
	}
	var got models.FlightSearchResult
	if jsonErr := json.Unmarshal(out.Bytes(), &got); jsonErr != nil {
		t.Fatalf("stdout must be the result as JSON: %v\n%s", jsonErr, out.String())
	}
	if len(got.ProviderStatuses) != 1 || got.ProviderStatuses[0].Reason != models.ReasonBlocked {
		t.Fatalf("JSON must carry provider_statuses with reason, got %+v", got.ProviderStatuses)
	}

	out.Reset()
	_ = reportFlightFailure(&out, "table", result, base)
	if out.Len() != 0 {
		t.Fatalf("table format must not print JSON on failure, got %q", out.String())
	}
}

func TestHotelsEmptyMessageAdmitsFailedProviders(t *testing.T) {
	failed := []models.ProviderStatus{
		{ID: "booking", Name: "Booking.com", Status: models.StatusRateLimited, Error: "HTTP 403 Forbidden"},
		{ID: "google_hotels", Name: "Google Hotels", Status: models.StatusCheckedNoHit},
	}
	msg := hotelsEmptyMessage(&models.HotelSearchResult{ProviderStatuses: failed})
	if strings.Contains(msg, "No hotels found") || !strings.Contains(msg, "Booking.com: blocked") {
		t.Fatalf("a failed provider must be reported, not absence: %q", msg)
	}
	if msg := hotelsEmptyMessage(&models.HotelSearchResult{ProviderStatuses: failed[1:]}); msg != "No hotels found." {
		t.Fatalf("a fully checked empty search may say so, got %q", msg)
	}
}
