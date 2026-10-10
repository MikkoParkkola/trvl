package main

import (
	"context"
	"encoding/json"
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

func stubFindSearch(t *testing.T, fn tripsearch.SearchFunc) {
	t.Helper()
	prev := findSearch
	t.Cleanup(func() { findSearch = prev })
	findSearch = fn
}

var findPartialStatuses = []models.ProviderStatus{
	{ID: "kiwi", Name: "Kiwi", Status: models.StatusTimeout, Error: "deadline exceeded"},
	{ID: "google_flights", Name: "Google Flights", Status: models.StatusOK},
}

func partialFindFake(_ context.Context, _, _ []string, date string, _ flights.SearchOptions) (*models.FlightSearchResult, error) {
	return &models.FlightSearchResult{Success: true, Count: 1, ProviderStatuses: findPartialStatuses, Flights: []models.FlightResult{{
		Price: 99, Currency: "EUR",
		Legs: []models.FlightLeg{{DepartureAirport: models.AirportInfo{Code: "AMS"}, ArrivalAirport: models.AirportInfo{Code: "PRG"}, DepartureTime: date + "T09:00"}},
	}}}, nil
}

func TestRunFindSweep_DateFailingWithoutStatusesIsNotComplete(t *testing.T) {
	models.UseColor = false
	dates := sweepSaturdays("2026-10-17", 14, 4)
	stubFindSearch(t, func(ctx context.Context, o, d []string, date string, opts flights.SearchOptions) (*models.FlightSearchResult, error) {
		if date == dates[0] {
			res, _ := partialFindFake(ctx, o, d, date, opts)
			res.ProviderStatuses = findPartialStatuses[1:] // this date is complete
			return res, nil
		}
		return nil, errors.New("upstream exploded")
	})
	base := tripsearch.Request{Origin: "AMS", Destination: "PRG", Date: "2026-10-17", TopN: 3, PreferencesOverride: &preferences.Preferences{}}
	out := captureStdout(t, func() { _ = runFindSweep(context.Background(), base, "", 14) })
	if !strings.Contains(out, "Partial coverage: counts are provider checks across") || !strings.Contains(out, "Search ("+dates[1]+")") {
		t.Errorf("a date that failed without statuses must still make the sweep partial:\n%s", out)
	}
}

func TestRunFind_SingleDateJSONKeepsProviderEvidence(t *testing.T) {
	withTempHome(t)
	stubFindSearch(t, partialFindFake)
	req := tripsearch.Request{Origin: "AMS", Destination: "PRG", Date: "2026-10-17", TopN: 3, PreferencesOverride: &preferences.Preferences{}}
	out := captureStdout(t, func() {
		if err := runFind(context.Background(), req, "json", false, 3); err != nil {
			t.Errorf("runFind: %v", err)
		}
	})
	var got models.FlightSearchResult
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("JSON output: %v\n%s", err, out)
	}
	if got.Completeness.State != models.CompletenessPartial || len(got.ProviderStatuses) != 2 {
		t.Errorf("completeness=%+v statuses=%d, want partial with both statuses", got.Completeness, len(got.ProviderStatuses))
	}
}

func TestRunFind_SingleDateFilteredToZeroNamesFailures(t *testing.T) {
	models.UseColor = false
	withTempHome(t)
	stubFindSearch(t, partialFindFake)
	req := tripsearch.Request{Origin: "AMS", Destination: "PRG", Date: "2026-10-17", TopN: 3, MinLayoverMinutes: 600,
		PreferencesOverride: &preferences.Preferences{}}
	out := captureStdout(t, func() { _ = runFind(context.Background(), req, "", false, 3) })
	if !strings.Contains(out, "Partial coverage:") || !strings.Contains(out, "- Kiwi: ") || !strings.Contains(out, "No profile-compliant flights found") {
		t.Errorf("filtered-to-zero output must name the failed provider:\n%s", out)
	}
}
