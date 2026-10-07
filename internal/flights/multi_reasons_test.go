package flights

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// MIK-8041: a multi-airport search must not turn blocked providers into an
// empty result.

func stubCombos(t *testing.T, fn func(origin, dest string) (*models.FlightSearchResult, error)) {
	t.Helper()
	orig := searchFlightsWithClientFunc
	t.Cleanup(func() { searchFlightsWithClientFunc = orig })
	searchFlightsWithClientFunc = func(_ context.Context, _ *batchexec.Client, origin, dest, _ string, _ SearchOptions) (*models.FlightSearchResult, error) {
		return fn(origin, dest)
	}
}

func blockedCombo(origin, dest string) (*models.FlightSearchResult, error) {
	err := errors.New("google flights blocked the request (HTTP 403)")
	return &models.FlightSearchResult{Error: err.Error(), ProviderStatuses: []models.ProviderStatus{
		{ID: "google_flights", Name: "Google Flights", Status: models.StatusRateLimited, Error: "HTTP 403 Forbidden"},
	}}, err
}

func TestSearchMultiAirportAllCombosFailedReturnsErrorWithStatuses(t *testing.T) {
	stubCombos(t, blockedCombo)

	result, err := SearchMultiAirport(context.Background(), []string{"HEL", "TKU"}, []string{"CDG"}, "2026-11-01", SearchOptions{})
	if err == nil {
		t.Fatal("every combination failed; the search must return an error, not an empty success")
	}
	if result == nil || len(result.ProviderStatuses) != 2 {
		t.Fatalf("result must carry one status per failed combination, got %+v", result)
	}
	lines := models.ProviderFailureLines(result.ProviderStatuses)
	for _, want := range []string{"HEL-CDG", "TKU-CDG", "blocked"} {
		if !strings.Contains(lines, want) {
			t.Fatalf("failure lines must name %q, got %q", want, lines)
		}
	}
	if result.Completeness.State != models.CompletenessBlocked {
		t.Fatalf("completeness = %q, want blocked", result.Completeness.State)
	}
}

func TestSearchMultiAirportPartialFailureIsPartial(t *testing.T) {
	stubCombos(t, func(origin, dest string) (*models.FlightSearchResult, error) {
		if origin == "TKU" {
			return blockedCombo(origin, dest)
		}
		return &models.FlightSearchResult{Success: true, ProviderStatuses: []models.ProviderStatus{
			{ID: "google_flights", Name: "Google Flights", Status: models.StatusCheckedNoHit},
		}}, nil
	})

	result, err := SearchMultiAirport(context.Background(), []string{"HEL", "TKU"}, []string{"CDG"}, "2026-11-01", SearchOptions{})
	if err != nil {
		t.Fatalf("one combination answered; the search succeeds, got %v", err)
	}
	if result.Completeness.State != models.CompletenessPartial {
		t.Fatalf("completeness = %q, want partial", result.Completeness.State)
	}
	if !strings.Contains(models.ProviderFailureLines(result.ProviderStatuses), "TKU-CDG") {
		t.Fatalf("the failed combination must be reported, got %+v", result.ProviderStatuses)
	}
}

func TestSearchMultiAirportCleanEmptyStaysComplete(t *testing.T) {
	stubCombos(t, func(origin, dest string) (*models.FlightSearchResult, error) {
		return &models.FlightSearchResult{Success: true, ProviderStatuses: []models.ProviderStatus{
			{ID: "google_flights", Name: "Google Flights", Status: models.StatusCheckedNoHit},
		}}, nil
	})
	result, err := SearchMultiAirport(context.Background(), []string{"HEL", "TKU"}, []string{"CDG"}, "2026-11-01", SearchOptions{})
	if err != nil {
		t.Fatalf("clean empty search must not error: %v", err)
	}
	if !result.Completeness.MayClaimExhaustive() {
		t.Fatalf("every combination answered; completeness = %q", result.Completeness.State)
	}
}
