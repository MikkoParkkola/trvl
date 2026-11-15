package ground

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
	"golang.org/x/time/rate"
)

// TestNotApplicableSites_WrapSentinel pins D3: every "does not serve this
// pair" answer in PR A's providers wraps models.ErrNotApplicable, so status
// mapping no longer depends on error wording. Each call uses an unserved pair
// and returns before any network call. Finnlines, Eurostar, Digitransit, NS,
// Trainline and SNCF have no pre-network "valid stations, no route" branch;
// their route gates in search.go answer that case with a status row.
func TestNotApplicableSites_WrapSentinel(t *testing.T) {
	ctx := context.Background()
	const d = "2026-07-15"
	orig := stenalineLimiter
	stenalineLimiter = rate.NewLimiter(rate.Limit(1000), 1)
	t.Cleanup(func() { stenalineLimiter = orig })
	emptyAutocomplete := func(context.Context, string) ([]FlixBusCity, error) { return nil, nil }
	berlinOnly := func(_ context.Context, q string) ([]FlixBusCity, error) {
		if q == "Berlin" {
			return []FlixBusCity{{ID: "berlin", Name: "Berlin"}}, nil
		}
		return nil, nil
	}
	noSearch := func(FlixBusCity, FlixBusCity) ([]models.GroundRoute, error) { return nil, nil }
	cases := []struct {
		name string
		call func() error
	}{
		{"dfds no port from", func() error { _, err := SearchDFDS(ctx, "Narnia", "Newcastle", d, "EUR"); return err }},
		{"dfds no port to", func() error { _, err := SearchDFDS(ctx, "Amsterdam", "Narnia", d, "EUR"); return err }},
		{"dfds no route", func() error { _, err := SearchDFDS(ctx, "Amsterdam", "Dieppe", d, "EUR"); return err }},
		{"eckeroline no port from", func() error { _, err := SearchEckeroLine(ctx, "Narnia", "Tallinn", d, "EUR"); return err }},
		{"eckeroline no port to", func() error { _, err := SearchEckeroLine(ctx, "Helsinki", "Narnia", d, "EUR"); return err }},
		{"eckeroline no schedule", func() error { _, err := SearchEckeroLine(ctx, "Tallinn", "Tallinn", d, "EUR"); return err }},
		{"finnlines no port from", func() error { _, err := SearchFinnlines(ctx, "Narnia", "Travemünde", d, "EUR"); return err }},
		{"finnlines no port to", func() error { _, err := SearchFinnlines(ctx, "Helsinki", "Narnia", d, "EUR"); return err }},
		{"stenaline no port from", func() error { _, err := SearchStenaLine(ctx, "Narnia", "Kiel", d, "EUR"); return err }},
		{"stenaline no port to", func() error { _, err := SearchStenaLine(ctx, "Kiel", "Narnia", d, "EUR"); return err }},
		{"stenaline no schedule", func() error { _, err := SearchStenaLine(ctx, "Kiel", "Gdynia", d, "EUR"); return err }},
		{"vikingline no port from", func() error { _, err := SearchVikingLine(ctx, "Narnia", "Tallinn", d, "EUR"); return err }},
		{"vikingline no port to", func() error { _, err := SearchVikingLine(ctx, "Helsinki", "Narnia", d, "EUR"); return err }},
		{"vikingline no schedule", func() error { _, err := SearchVikingLine(ctx, "Helsinki", "Turku", d, "EUR"); return err }},
		{"eurostar no station from", func() error { _, err := SearchEurostar(ctx, "Narnia", "Paris", d, d, "EUR", false); return err }},
		{"eurostar no station to", func() error { _, err := SearchEurostar(ctx, "London", "Narnia", d, d, "EUR", false); return err }},
		{"digitransit no station from", func() error { _, err := SearchDigitransit(ctx, "Narnia", "Tampere", d, "EUR"); return err }},
		{"digitransit no station to", func() error { _, err := SearchDigitransit(ctx, "Helsinki", "Narnia", d, "EUR"); return err }},
		{"ns no station from", func() error { _, err := SearchNS(ctx, "Narnia", "Amsterdam", d, "EUR"); return err }},
		{"ns no station to", func() error { _, err := SearchNS(ctx, "Amsterdam", "Narnia", d, "EUR"); return err }},
		{"trainline no station from", func() error { _, err := SearchTrainline(ctx, "Narnia", "Paris", d, "EUR", false); return err }},
		{"trainline no station to", func() error { _, err := SearchTrainline(ctx, "Paris", "Narnia", d, "EUR", false); return err }},
		{"sncf no station from", func() error { _, err := SearchSNCF(ctx, "Narnia", "Paris", d, "EUR", false); return err }},
		{"sncf no station to", func() error { _, err := SearchSNCF(ctx, "Paris", "Narnia", d, "EUR", false); return err }},
		{"italo no station from", func() error { _, err := SearchItalo(ctx, "Narnia", "Rome", d, "EUR"); return err }},
		{"italo no station to", func() error { _, err := SearchItalo(ctx, "Milan", "Narnia", d, "EUR"); return err }},
		{"city found for, from side", func() error {
			_, err := resolveAndSearch(ctx, "Narnia", "Berlin", "FlixBus", emptyAutocomplete, noSearch)
			return err
		}},
		{"city found for, to side", func() error {
			_, err := resolveAndSearch(ctx, "Berlin", "Narnia", "FlixBus", berlinOnly, noSearch)
			return err
		}},
		{"rome2rio no route", func() error { return rome2rioParseFailure(0, 0, "Narnia", "Paris") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.call()
			if err == nil {
				t.Fatal("want an error for an unserved pair, got nil")
			}
			if !errors.Is(err, models.ErrNotApplicable) {
				t.Errorf("error %q does not wrap models.ErrNotApplicable", err)
			}
		})
	}
}

// TestIsProviderNotApplicable_Sentinel pins that the sentinel alone, with none
// of the legacy wording, classifies as not applicable.
func TestIsProviderNotApplicable_Sentinel(t *testing.T) {
	err := fmt.Errorf("someprovider: %w", models.ErrNotApplicable)
	if !isProviderNotApplicable(err) {
		t.Errorf("isProviderNotApplicable(%q) = false, want true", err)
	}
}
