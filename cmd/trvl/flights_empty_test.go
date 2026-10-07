package main

import (
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

func TestFlightsEmptyMessageAdmitsFailedProviders(t *testing.T) {
	statuses := []models.ProviderStatus{
		{ID: "google_flights", Name: "Google Flights (TKU-CDG)", Status: models.StatusRateLimited, Error: "HTTP 403 Forbidden"},
		{ID: "google_flights", Name: "Google Flights (HEL-CDG)", Status: models.StatusCheckedNoHit},
	}
	msg := flightsEmptyMessage(&models.FlightSearchResult{Success: true, ProviderStatuses: statuses, Completeness: models.ComputeCompleteness(statuses)})
	if strings.Contains(msg, "No flights found") || !strings.Contains(msg, "TKU-CDG): blocked") {
		t.Fatalf("a failed pair must be reported, not absence: %q", msg)
	}
	clean := statuses[1:]
	if msg := flightsEmptyMessage(&models.FlightSearchResult{Success: true, ProviderStatuses: clean, Completeness: models.ComputeCompleteness(clean)}); msg != "No flights found." {
		t.Fatalf("a fully checked empty search may say so, got %q", msg)
	}
}
