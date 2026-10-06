package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/cars"
	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/ground"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// Empty-result summaries must not claim "nothing found" when providers failed.
func TestEmptySummariesDoNotClaimAbsenceWhenProvidersFailed(t *testing.T) {
	failed := []models.ProviderStatus{
		{ID: "booking", Name: "Booking.com", Status: models.StatusRateLimited, Error: "HTTP 403 Forbidden"},
		{ID: "google_hotels", Name: "Google Hotels", Status: models.StatusCheckedNoHit},
	}
	hotel := hotelSummary(&models.HotelSearchResult{ProviderStatuses: failed}, "Lyon")
	if strings.Contains(hotel, "No hotels found") || !strings.Contains(hotel, "Booking.com: blocked") {
		t.Fatalf("hotel summary must report the blocked provider, not absence: %q", hotel)
	}

	car := buildCarSearchSummary(&models.CarSearchResult{ProviderStatuses: failed}, cars.SearchOptions{PickupLocation: "LYS", PickupDate: "2026-11-01", DropoffDate: "2026-11-03"})
	if strings.Contains(car, "No rental car offers found") || !strings.Contains(car, "Booking.com: blocked") {
		t.Fatalf("car summary must report the blocked provider, not absence: %q", car)
	}

	// Every provider answered with nothing: the definitive wording stays.
	clean := hotelSummary(&models.HotelSearchResult{ProviderStatuses: failed[1:]}, "Lyon")
	if !strings.Contains(clean, "No hotels found") {
		t.Fatalf("a fully checked empty search may say so, got %q", clean)
	}

	details := hotelDetailsSummary(hotelDetailsSearchResponse{ProviderStatuses: failed}, "Lyon")
	if strings.Contains(details, "No hotels found") || !strings.Contains(details, "Booking.com: blocked") {
		t.Fatalf("detailed hotel summary must report the blocked provider, not absence: %q", details)
	}
	acc := accommodationSearchSummary(accommodationSearchResponse{ProviderStatuses: failed})
	if strings.Contains(acc, "No accommodation candidates found") || !strings.Contains(acc, "Booking.com: blocked") {
		t.Fatalf("accommodation summary must report the blocked provider, not absence: %q", acc)
	}
}

// A flight search where some providers answered must still expose which ones
// failed and why in its structured output.
func TestPartialFlightSuccessKeepsProviderReasons(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	date := time.Now().AddDate(0, 1, 0).Format("2006-01-02")
	orig := dispatchFlightSearchFunc
	t.Cleanup(func() { dispatchFlightSearchFunc = orig })
	statuses := []models.ProviderStatus{
		{ID: "kiwi", Name: "Kiwi", Status: models.StatusOK, Results: 0},
		{ID: "google_flights", Name: "Google Flights", Status: models.StatusRateLimited, Error: "google flights blocked the request (HTTP 403): " + models.ErrRateLimited.Error()},
	}
	dispatchFlightSearchFunc = func(context.Context, map[string]any, string, string, string, flights.SearchOptions) (*models.FlightSearchResult, error) {
		return &models.FlightSearchResult{Success: true, ProviderStatuses: statuses, Completeness: models.ComputeCompleteness(statuses)}, nil
	}

	_, structured, isErr := callTravel(t, NewServer(), map[string]any{
		"intent": "search_flights",
		"params": map[string]any{"origin": "HEL", "destination": "CDG", "departure_date": date},
	})
	if isErr {
		t.Fatal("partial success must not be an error")
	}
	result, _ := structured["result"].(map[string]any)
	list, _ := result["provider_statuses"].([]any)
	var reason any
	for _, st := range list {
		if m, _ := st.(map[string]any); m["id"] == "google_flights" {
			reason = m["reason"]
		}
	}
	if reason != models.ReasonBlocked {
		t.Fatalf("structured result must carry google_flights reason blocked, got statuses %#v", result["provider_statuses"])
	}
	if c, _ := result["completeness"].(map[string]any); c["state"] != models.CompletenessPartial {
		t.Fatalf("structured result must carry partial completeness, got %#v", result["completeness"])
	}
}

// MIK-7989: when every provider fails (the cloud-IP case), the reply must
// still name each provider and why it failed. tools/call keeps only the error
// text on failure, so that is where the diagnostics have to be.
func TestAllProvidersBlockedStillReportsReasons(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	date := time.Now().AddDate(0, 1, 0).Format("2006-01-02")

	origFlight := dispatchFlightSearchFunc
	t.Cleanup(func() { dispatchFlightSearchFunc = origFlight })
	dispatchFlightSearchFunc = func(context.Context, map[string]any, string, string, string, flights.SearchOptions) (*models.FlightSearchResult, error) {
		err := errors.New("all flight providers failed")
		return &models.FlightSearchResult{
			Error: err.Error(),
			ProviderStatuses: []models.ProviderStatus{
				{ID: "google_flights", Name: "Google Flights", Status: models.StatusRateLimited, Error: "HTTP 403 Forbidden"},
				{ID: "wizzair", Name: "Wizz Air", Status: models.StatusRateLimited, Error: "HTTP 503 Service Unavailable"},
			},
		}, err
	}

	origGround := searchGroundByNameFunc
	t.Cleanup(func() { searchGroundByNameFunc = origGround })
	searchGroundByNameFunc = func(context.Context, string, string, string, ground.SearchOptions) (*models.GroundSearchResult, error) {
		return &models.GroundSearchResult{
			Error: "all ground providers failed",
			ProviderStatuses: []models.ProviderStatus{
				{ID: "sncf", Name: "SNCF", Status: models.StatusRateLimited, Error: "HTTP 403 Forbidden"},
				{ID: "italo", Name: "Italo", Status: models.StatusFailed, Error: "dial tcp: lookup italotreno.com: no such host"},
			},
		}, nil
	}

	s := NewServer()
	cases := []struct {
		name string
		args map[string]any
		want []string
	}{
		{"flights", map[string]any{"intent": "search_flights", "params": map[string]any{"origin": "HEL", "destination": "CDG", "departure_date": date}},
			[]string{"Google Flights", "blocked", "Wizz Air", "unavailable"}},
		{"ground", map[string]any{"intent": "search_ground", "params": map[string]any{"from": "Torino", "to": "Lyon", "date": date}},
			[]string{"SNCF", "blocked", "Italo", "dns"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text, _, isErr := callTravel(t, s, tc.args)
			if !isErr {
				t.Fatalf("total failure must be an error, got %q", text)
			}
			for _, want := range tc.want {
				if !strings.Contains(text, want) {
					t.Fatalf("error text must contain %q, got %q", want, text)
				}
			}
			if strings.Contains(strings.ToLower(text), "no flights found") || strings.Contains(strings.ToLower(text), "no ground routes found") {
				t.Fatalf("a blocked search must not read as an empty one: %q", text)
			}
		})
	}
}
