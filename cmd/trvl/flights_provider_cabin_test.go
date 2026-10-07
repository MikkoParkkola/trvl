package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// MIK-8041: --provider and cabin comparison must not hide why providers failed.

func TestSoloProviderFailureBecomesAStatus(t *testing.T) {
	base := errors.New("ryanair: HTTP 403 Forbidden")
	var out bytes.Buffer
	err := reportFlightFailure(&out, "json", soloFailureResult(nil, "ryanair", base), base)
	if !errors.Is(err, base) {
		t.Fatalf("the failure must still be returned, got %v", err)
	}
	var got models.FlightSearchResult
	if jsonErr := json.Unmarshal(out.Bytes(), &got); jsonErr != nil {
		t.Fatalf("stdout must be JSON: %v\n%s", jsonErr, out.String())
	}
	if len(got.ProviderStatuses) != 1 || got.ProviderStatuses[0].ID != "ryanair" || got.ProviderStatuses[0].Reason != models.ReasonBlocked {
		t.Fatalf("JSON must carry the provider and its reason, got %+v", got.ProviderStatuses)
	}
	keep := &models.FlightSearchResult{ProviderStatuses: []models.ProviderStatus{{ID: "x", Error: "y"}}}
	if soloFailureResult(keep, "ryanair", base) != keep {
		t.Fatal("an existing result must be kept as it is")
	}
}

func TestCabinComparisonFailsWhenEveryCabinFails(t *testing.T) {
	opts := flights.SearchOptions{SearchOverride: func(_ context.Context, _, _, _ string, _ flights.SearchOptions) (*models.FlightSearchResult, error) {
		err := errors.New("all flight providers failed")
		return &models.FlightSearchResult{Error: err.Error(), ProviderStatuses: []models.ProviderStatus{
			{ID: "google_flights", Name: "Google Flights", Status: models.StatusRateLimited, Error: "HTTP 403 Forbidden"},
		}}, err
	}}
	err := runCabinComparison(context.Background(), []string{"HEL"}, []string{"CDG"}, "2026-11-01", opts, "json")
	if err == nil {
		t.Fatal("every cabin search failed; the command must exit non-zero")
	}
	if !strings.Contains(err.Error(), "Google Flights: blocked") {
		t.Fatalf("the error must name the failed provider and reason, got %q", err)
	}
}

func TestCabinComparisonSucceedsWhenACabinAnswers(t *testing.T) {
	opts := flights.SearchOptions{SearchOverride: func(_ context.Context, _, _, _ string, o flights.SearchOptions) (*models.FlightSearchResult, error) {
		if o.CabinClass == models.Economy {
			return &models.FlightSearchResult{Success: true, Count: 1, Flights: []models.FlightResult{{Price: 99, Currency: "EUR"}}}, nil
		}
		err := errors.New("all flight providers failed")
		return &models.FlightSearchResult{Error: err.Error()}, err
	}}
	if err := runCabinComparison(context.Background(), []string{"HEL"}, []string{"CDG"}, "2026-11-01", opts, "json"); err != nil {
		t.Fatalf("one cabin answered; the comparison succeeds, got %v", err)
	}
}
