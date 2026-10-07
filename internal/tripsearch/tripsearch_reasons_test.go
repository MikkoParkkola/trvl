package tripsearch

import (
	"context"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// MIK-8041: an empty answer with failed providers must surface the failures,
// while a clean empty answer stays an ordinary empty result.
func TestSearchEmptyWithFailedProvidersReportsThem(t *testing.T) {
	statuses := []models.ProviderStatus{
		{ID: "google_flights", Name: "Google Flights (HEL-PRG)", Status: models.StatusRateLimited, Error: "HTTP 403 Forbidden"},
		{ID: "google_flights", Name: "Google Flights (AMS-PRG)", Status: models.StatusCheckedNoHit},
	}
	fake := func(context.Context, []string, []string, string, flights.SearchOptions) (*models.FlightSearchResult, error) {
		return &models.FlightSearchResult{Success: true, ProviderStatuses: statuses, Completeness: models.ComputeCompleteness(statuses)}, nil
	}
	req := Request{Origin: "home", Destination: "PRG", Date: "2026-04-23", TopN: 2, PreferencesOverride: mkPrefs()}

	_, err := Search(context.Background(), req, fake, nil)
	if err == nil || !strings.Contains(err.Error(), "Google Flights (HEL-PRG): blocked") {
		t.Fatalf("an incomplete empty search must report the failed provider, got %v", err)
	}

	clean := statuses[1:]
	cleanFake := func(context.Context, []string, []string, string, flights.SearchOptions) (*models.FlightSearchResult, error) {
		return &models.FlightSearchResult{Success: true, ProviderStatuses: clean, Completeness: models.ComputeCompleteness(clean)}, nil
	}
	res, err := Search(context.Background(), req, cleanFake, nil)
	if err != nil || res.Count != 0 {
		t.Fatalf("a clean empty search is an ordinary empty result, got %+v, %v", res, err)
	}
}
