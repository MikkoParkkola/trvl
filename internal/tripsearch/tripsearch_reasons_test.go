package tripsearch

import (
	"context"
	"errors"
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

// partialStatuses has one blocked and one answering provider.
var partialStatuses = []models.ProviderStatus{
	{ID: "google_flights", Name: "Google Flights (HEL-PRG)", Status: models.StatusRateLimited, Error: "HTTP 403 Forbidden"},
	{ID: "google_flights", Name: "Google Flights (AMS-PRG)", Status: models.StatusOK},
}

func directFlight() models.FlightResult {
	return models.FlightResult{Price: 120, Currency: "EUR", Legs: []models.FlightLeg{{
		DepartureAirport: models.AirportInfo{Code: "AMS"}, ArrivalAirport: models.AirportInfo{Code: "PRG"},
		DepartureTime: "2026-04-23T12:00", ArrivalTime: "2026-04-23T14:00",
	}}}
}

func partialFake(context.Context, []string, []string, string, flights.SearchOptions) (*models.FlightSearchResult, error) {
	return &models.FlightSearchResult{Success: true, Flights: []models.FlightResult{directFlight()}, Count: 1,
		ProviderStatuses: partialStatuses, Completeness: models.ComputeCompleteness(partialStatuses)}, nil
}

// MIK-8088: a partial answer keeps its failures whether or not results survive.
func TestSearchPartialResultKeepsFailures(t *testing.T) {
	req := Request{Origin: "home", Destination: "PRG", Date: "2026-04-23", TopN: 2, PreferencesOverride: mkPrefs()}
	res, err := Search(context.Background(), req, partialFake, nil)
	if err != nil || res.Count != 1 {
		t.Fatalf("got %+v, %v; want one bundle", res, err)
	}
	if res.Completeness.State != models.CompletenessPartial || !strings.Contains(res.PartialNote(), "Google Flights (HEL-PRG): blocked") {
		t.Fatalf("completeness=%+v note=%q, want partial naming the blocked provider", res.Completeness, res.PartialNote())
	}
}

func TestSearchPartialFilteredToZeroKeepsFailures(t *testing.T) {
	req := Request{Origin: "home", Destination: "PRG", Date: "2026-04-23", TopN: 2, PreferencesOverride: mkPrefs(), MinLayoverMinutes: 600}
	res, err := Search(context.Background(), req, partialFake, nil)
	if err != nil || res.Count != 0 || res.PreFilterCount != 1 {
		t.Fatalf("got %+v, %v; want zero bundles after filters", res, err)
	}
	if !strings.Contains(res.PartialNote(), "Google Flights (HEL-PRG): blocked") {
		t.Fatalf("note = %q, want the blocked provider named even though filters left nothing", res.PartialNote())
	}
}

// A failed search keeps its statuses on the error, even when the search did
// not fill in Completeness (the single-route all-fail path does not).
func TestSearchErrorKeepsProviderStatuses(t *testing.T) {
	failed := partialStatuses[:1]
	fake := func(context.Context, []string, []string, string, flights.SearchOptions) (*models.FlightSearchResult, error) {
		return &models.FlightSearchResult{ProviderStatuses: failed}, errors.New("all providers failed")
	}
	req := Request{Origin: "home", Destination: "PRG", Date: "2026-04-23", PreferencesOverride: mkPrefs()}
	_, err := Search(context.Background(), req, fake, nil)
	var serr *SearchError
	if !errors.As(err, &serr) || len(serr.ProviderStatuses) != 1 {
		t.Fatalf("err = %v, want a SearchError carrying the statuses", err)
	}
	if !strings.Contains(err.Error(), "Google Flights (HEL-PRG): blocked") {
		t.Fatalf("err = %q, want it to name the blocked provider", err)
	}
}

func TestLabelStatusesWithDate(t *testing.T) {
	got := LabelStatusesWithDate([]models.ProviderStatus{{ID: "kiwi"}, {ID: "google_flights", Name: "Google Flights"}}, "2026-10-17")
	if got[0].ID != "kiwi@2026-10-17" || got[0].Name != "kiwi (2026-10-17)" || got[1].Name != "Google Flights (2026-10-17)" {
		t.Fatalf("got %+v", got)
	}
}
