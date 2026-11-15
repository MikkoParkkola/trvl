package ground

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestNotApplicableSites_WrapSentinel pins D3: every "does not serve this
// pair" answer in PR A's providers wraps models.ErrNotApplicable, so status
// mapping no longer depends on error wording. Each call uses an unserved pair
// and returns before any network call.
func TestNotApplicableSites_WrapSentinel(t *testing.T) {
	ctx := context.Background()
	const d = "2026-07-15"
	emptyAutocomplete := func(context.Context, string) ([]FlixBusCity, error) { return nil, nil }
	cases := []struct {
		name string
		call func() error
	}{
		{"dfds no port from", func() error { _, err := SearchDFDS(ctx, "Narnia", "Newcastle", d, "EUR"); return err }},
		{"dfds no port to", func() error { _, err := SearchDFDS(ctx, "Amsterdam", "Narnia", d, "EUR"); return err }},
		{"dfds no route", func() error { _, err := SearchDFDS(ctx, "Amsterdam", "Dieppe", d, "EUR"); return err }},
		{"eckeroline no port", func() error { _, err := SearchEckeroLine(ctx, "Narnia", "Tallinn", d, "EUR"); return err }},
		{"finnlines no port", func() error { _, err := SearchFinnlines(ctx, "Narnia", "Travemünde", d, "EUR"); return err }},
		{"stenaline no port", func() error { _, err := SearchStenaLine(ctx, "Narnia", "Kiel", d, "EUR"); return err }},
		{"vikingline no port", func() error { _, err := SearchVikingLine(ctx, "Narnia", "Tallinn", d, "EUR"); return err }},
		{"eurostar no station", func() error { _, err := SearchEurostar(ctx, "Narnia", "Paris", d, d, "EUR", false); return err }},
		{"digitransit no station", func() error { _, err := SearchDigitransit(ctx, "Narnia", "Tampere", d, "EUR"); return err }},
		{"ns no station", func() error { _, err := SearchNS(ctx, "Narnia", "Amsterdam", d, "EUR"); return err }},
		{"trainline no station", func() error { _, err := SearchTrainline(ctx, "Narnia", "Paris", d, "EUR", false); return err }},
		{"sncf no station", func() error { _, err := SearchSNCF(ctx, "Narnia", "Paris", d, "EUR", false); return err }},
		{"city found for (autocomplete empty)", func() error {
			_, err := resolveAndSearch(ctx, "Narnia", "Berlin", "FlixBus", emptyAutocomplete,
				func(FlixBusCity, FlixBusCity) ([]models.GroundRoute, error) { return nil, nil })
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
