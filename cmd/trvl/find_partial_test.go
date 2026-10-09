package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/preferences"
	"github.com/MikkoParkkola/trvl/internal/tripsearch"
)

// MIK-8088: a date sweep where one date fails outright must still name the
// providers that failed on that date. Not parallel: it swaps findSearch.
func TestRunFindSweep_FailedDateNamesItsProviders(t *testing.T) {
	models.UseColor = false
	dates := sweepSaturdays("2026-10-17", 14, 4)
	if len(dates) < 2 {
		t.Fatalf("sweep dates = %v, want at least two", dates)
	}
	prev := findSearch
	t.Cleanup(func() { findSearch = prev })
	findSearch = func(_ context.Context, _, _ []string, date string, _ flights.SearchOptions) (*models.FlightSearchResult, error) {
		ok := []models.ProviderStatus{{ID: "google_flights", Name: "Google Flights", Status: models.StatusOK}}
		if date == dates[0] {
			return &models.FlightSearchResult{Success: true, Count: 1, ProviderStatuses: ok, Flights: []models.FlightResult{{
				Price: 99, Currency: "EUR",
				Legs: []models.FlightLeg{{DepartureAirport: models.AirportInfo{Code: "AMS"}, ArrivalAirport: models.AirportInfo{Code: "PRG"}, DepartureTime: date + "T09:00"}},
			}}}, nil
		}
		failed := []models.ProviderStatus{{ID: "google_flights", Name: "Google Flights", Status: models.StatusRateLimited, Error: "HTTP 429"}}
		return &models.FlightSearchResult{ProviderStatuses: failed}, errors.New("all providers failed")
	}
	base := tripsearch.Request{Origin: "AMS", Destination: "PRG", Date: "2026-10-17", TopN: 3,
		PreferencesOverride: &preferences.Preferences{}}
	out := captureStdout(t, func() {
		if err := runFindSweep(context.Background(), base, "", 14); err != nil {
			t.Errorf("runFindSweep: %v", err)
		}
	})
	if !strings.Contains(out, "Partial coverage:") || !strings.Contains(out, "Google Flights ("+dates[1]+")") {
		t.Errorf("sweep output must name the provider that failed on %s:\n%s", dates[1], out)
	}
	if !strings.Contains(out, "EUR 99") {
		t.Errorf("sweep output lost the answering date's bundle:\n%s", out)
	}
}
