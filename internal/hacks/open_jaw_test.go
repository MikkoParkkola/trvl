package hacks

import (
	"context"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/models"
)

func openJawFlightResult(price float64, currency string) *models.FlightSearchResult {
	return &models.FlightSearchResult{
		Success: true,
		Flights: []models.FlightResult{{Price: price, Currency: currency}},
	}
}

// TestDetectOpenJaw_eurTargetPassthrough covers currency honesty case (a): an
// EUR target with EUR-denominated flights and ground costs passes through
// labelled EUR.
func TestDetectOpenJaw_eurTargetPassthrough(t *testing.T) {
	hacks := detectOpenJaw(context.Background(), DetectorInput{
		Origin:      "LHR",
		Destination: "PRG",
		Date:        "2026-06-01",
		ReturnDate:  "2026-06-08",
		Currency:    "EUR",
		SearchOverride: func(_ context.Context, _, _, _ string, opts flights.SearchOptions) (*models.FlightSearchResult, error) {
			if opts.ReturnDate != "" {
				// Round-trip baseline.
				return openJawFlightResult(400, "EUR"), nil
			}
			// One-way outbound and one-way alt-return legs.
			return openJawFlightResult(150, "EUR"), nil
		},
	})
	if len(hacks) == 0 {
		t.Fatalf("expected at least one hack, got 0")
	}
	for _, h := range hacks {
		if h.Currency != "EUR" {
			t.Errorf("expected Currency=EUR, got %q", h.Currency)
		}
	}
}

// TestDetectOpenJaw_nonEURTargetSuppressedWhenInconvertible covers case (b): a
// non-EUR target with no usable rate table must suppress rather than show an
// unconverted or mislabelled figure. Placeholder currency codes (ZZZ/ZZY) are
// used by convention (see currency_sweep_test.go) since they appear in no
// real exchange-rate table, and the context is cancelled so no live FX fetch
// can populate the cache either.
func TestDetectOpenJaw_nonEURTargetSuppressedWhenInconvertible(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	hacks := detectOpenJaw(ctx, DetectorInput{
		Origin:      "LHR",
		Destination: "PRG",
		Date:        "2026-06-01",
		ReturnDate:  "2026-06-08",
		Currency:    "ZZY",
		SearchOverride: func(_ context.Context, _, _, _ string, _ flights.SearchOptions) (*models.FlightSearchResult, error) {
			return openJawFlightResult(400, "ZZZ"), nil
		},
	})
	if len(hacks) != 0 {
		t.Errorf("expected suppression when round-trip baseline is inconvertible to target, got %d hacks", len(hacks))
	}
}

// TestDetectOpenJaw_hackCurrencyMatchesTarget covers case (c): whenever a
// hack surfaces, its Currency field equals the normalized target currency.
func TestDetectOpenJaw_hackCurrencyMatchesTarget(t *testing.T) {
	hacks := detectOpenJaw(context.Background(), DetectorInput{
		Origin:      "LHR",
		Destination: "PRG",
		Date:        "2026-06-01",
		ReturnDate:  "2026-06-08",
		Currency:    "eur", // lower-case input must normalize to EUR
		SearchOverride: func(_ context.Context, _, _, _ string, opts flights.SearchOptions) (*models.FlightSearchResult, error) {
			if opts.ReturnDate != "" {
				return openJawFlightResult(400, "EUR"), nil
			}
			return openJawFlightResult(150, "EUR"), nil
		},
	})
	if len(hacks) == 0 {
		t.Fatalf("expected at least one hack, got 0")
	}
	for _, h := range hacks {
		if h.Currency != "EUR" {
			t.Errorf("expected Hack.Currency == target EUR, got %q", h.Currency)
		}
	}
}
