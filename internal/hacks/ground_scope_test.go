package hacks

import (
	"context"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/ground"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestDetectorInputFromGround_CarriesSearchScope pins D4's hacks plumbing:
// the ground compose request's provider filter and no-cache flag reach the
// detector input.
func TestDetectorInputFromGround_CarriesSearchScope(t *testing.T) {
	in := detectorInputFromGround(ground.HackComposeRequest{
		Origin: "Prague", Destination: "Vienna", Date: "2026-07-01", Currency: "EUR", NaivePrice: 20,
		Providers: []string{"flixbus"}, ExcludeProviders: []string{"rome2rio"}, NoCache: true,
	})
	if in.Origin != "Prague" || in.Destination != "Vienna" || in.Date != "2026-07-01" || in.Currency != "EUR" || in.NaivePrice != 20 {
		t.Errorf("route fields not mapped: %+v", in)
	}
	if strings.Join(in.GroundProviders, ",") != "flixbus" || strings.Join(in.GroundExcludeProviders, ",") != "rome2rio" || !in.NoCache {
		t.Errorf("scope = providers %v exclude %v nocache %v; want [flixbus] [rome2rio] true", in.GroundProviders, in.GroundExcludeProviders, in.NoCache)
	}
}

// TestDetectorGroundSearch_ForwardsSearchScope pins that a detector's own
// ground search carries the caller's provider filter and no-cache flag.
func TestDetectorGroundSearch_ForwardsSearchScope(t *testing.T) {
	withConvertCurrencyFn(t, func(_ context.Context, amount float64, from, to string) (float64, string) {
		if from == to {
			return amount, to
		}
		return 0, ""
	})
	var got *ground.SearchOptions
	capture := func(_ context.Context, _, _, _ string, opts ground.SearchOptions) (*models.GroundSearchResult, error) {
		o := opts
		got = &o
		return &models.GroundSearchResult{Success: false}, nil
	}
	_ = detectMultiModalSkipFlight(context.Background(), DetectorInput{
		Origin: "PRG", Destination: "VIE", Date: "2026-07-01", Currency: "EUR",
		SearchOverride: func(context.Context, string, string, string, flights.SearchOptions) (*models.FlightSearchResult, error) {
			return chSearchResult(200, "EUR"), nil
		},
		GroundSearchOverride:   capture,
		GroundProviders:        []string{"flixbus"},
		GroundExcludeProviders: []string{"rome2rio"},
		NoCache:                true,
	})
	if got == nil {
		t.Fatal("detector never ran its ground search")
	}
	if strings.Join(got.Providers, ",") != "flixbus" || strings.Join(got.ExcludeProviders, ",") != "rome2rio" || !got.NoCache {
		t.Errorf("ground search scope = providers %v exclude %v nocache %v; want [flixbus] [rome2rio] true", got.Providers, got.ExcludeProviders, got.NoCache)
	}
}
