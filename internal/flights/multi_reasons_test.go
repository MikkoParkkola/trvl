package flights

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// MIK-8041: a multi-airport search must not turn blocked providers into an
// empty result, nor lose fares or evidence while aggregating.

func comboOpts(fn func(origin, dest string) (*models.FlightSearchResult, error)) SearchOptions {
	return SearchOptions{SearchOverride: func(_ context.Context, origin, dest, _ string, _ SearchOptions) (*models.FlightSearchResult, error) {
		return fn(origin, dest)
	}}
}

func blockedCombo(origin, dest string) (*models.FlightSearchResult, error) {
	err := errors.New("google flights blocked the request (HTTP 403)")
	return &models.FlightSearchResult{Error: err.Error(), ProviderStatuses: []models.ProviderStatus{
		{ID: "google_flights", Name: "Google Flights", Status: models.StatusRateLimited, Error: "HTTP 403 Forbidden"},
	}}, err
}

func emptyCombo(origin, dest string) (*models.FlightSearchResult, error) {
	return &models.FlightSearchResult{Success: true, ProviderStatuses: []models.ProviderStatus{
		{ID: "google_flights", Name: "Google Flights", Status: models.StatusCheckedNoHit},
	}}, nil
}

func TestSearchMultiAirportAllCombosFailedReturnsErrorWithStatuses(t *testing.T) {
	result, err := SearchMultiAirport(context.Background(), []string{"HEL", "TKU"}, []string{"CDG"}, "2026-11-01", comboOpts(blockedCombo))
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
	opts := comboOpts(func(origin, dest string) (*models.FlightSearchResult, error) {
		if origin == "TKU" {
			return blockedCombo(origin, dest)
		}
		return emptyCombo(origin, dest)
	})
	result, err := SearchMultiAirport(context.Background(), []string{"HEL", "TKU"}, []string{"CDG"}, "2026-11-01", opts)
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

func TestSearchMultiAirportFailureWithoutEvidenceStillCounts(t *testing.T) {
	opts := comboOpts(func(origin, dest string) (*models.FlightSearchResult, error) {
		if origin == "TKU" {
			return nil, context.DeadlineExceeded
		}
		return emptyCombo(origin, dest)
	})
	result, err := SearchMultiAirport(context.Background(), []string{"HEL", "TKU"}, []string{"CDG"}, "2026-11-01", opts)
	if err != nil {
		t.Fatalf("one combination answered; the search succeeds, got %v", err)
	}
	if result.Completeness.MayClaimExhaustive() {
		t.Fatalf("a timed-out pair must keep the search from claiming completeness, got %q", result.Completeness.State)
	}
	if !strings.Contains(models.ProviderFailureLines(result.ProviderStatuses), "TKU-CDG") {
		t.Fatalf("the timed-out pair must be reported, got %+v", result.ProviderStatuses)
	}
}

func TestSearchMultiAirportCleanEmptyIsASuccess(t *testing.T) {
	result, err := SearchMultiAirport(context.Background(), []string{"HEL", "TKU"}, []string{"CDG"}, "2026-11-01", comboOpts(emptyCombo))
	if err != nil {
		t.Fatalf("clean empty search must not error: %v", err)
	}
	if !result.Success {
		t.Fatal("every pair answered with no flights: Success must be true, like a single-route search")
	}
	if !result.Completeness.MayClaimExhaustive() {
		t.Fatalf("every combination answered; completeness = %q", result.Completeness.State)
	}
}

func TestSearchMultiAirportKeepsAFKLMFaresWhenPairsFail(t *testing.T) {
	opts := comboOpts(blockedCombo)
	opts.ReturnDate = "2026-11-08"
	opts.Currency = "EUR"
	opts.afklmTestFlights = []models.FlightResult{{Price: 210, Currency: "EUR"}}

	result, err := SearchMultiAirport(context.Background(), []string{"HEL", "TKU"}, []string{"CDG"}, "2026-11-01", opts)
	if err != nil {
		t.Fatalf("AFKLM returned a fare; the search must not fail, got %v", err)
	}
	if !result.Success || result.Count != 1 || result.Flights[0].Price != 210 {
		t.Fatalf("the AFKLM fare must be kept as a success, got success=%v %+v", result.Success, result.Flights)
	}
	if result.Completeness.State != models.CompletenessPartial {
		t.Fatalf("completeness = %q, want partial", result.Completeness.State)
	}
	foundAFKLM := false
	for _, st := range result.ProviderStatuses {
		if strings.HasPrefix(st.ID, "native_roundtrip:afklm") {
			foundAFKLM = true
		}
	}
	if !foundAFKLM {
		t.Fatalf("the AFKLM status must be kept, got %+v", result.ProviderStatuses)
	}
	if !strings.Contains(models.ProviderFailureLines(result.ProviderStatuses), "HEL-CDG") {
		t.Fatalf("the failed pairs must still be reported, got %+v", result.ProviderStatuses)
	}
}
