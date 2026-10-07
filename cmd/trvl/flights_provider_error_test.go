package main

import (
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
