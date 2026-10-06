package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/ground"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// MIK-7989: when every provider fails (the cloud-IP case), the reply must
// still name each provider and why it failed. tools/call keeps only the error
// text on failure, so that is where the diagnostics have to be.
func TestAllProvidersBlockedStillReportsReasons(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
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
