package hacks

import (
	"context"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/models"
)

func positioningFlightResult(price float64, currency string) *models.FlightSearchResult {
	return &models.FlightSearchResult{
		Success: true,
		Flights: []models.FlightResult{{Price: price, Currency: currency}},
	}
}

// TestDetectPositioning_eurTargetPassthrough covers currency honesty case (a):
// an EUR target with EUR-denominated flights and ground costs passes through
// labelled EUR.
func TestDetectPositioning_eurTargetPassthrough(t *testing.T) {
	hacks := detectPositioning(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		Date:        "2026-06-01",
		Currency:    "EUR",
		SearchOverride: func(_ context.Context, origin, _, _ string, _ flights.SearchOptions) (*models.FlightSearchResult, error) {
			if origin == "HEL" {
				return positioningFlightResult(200, "EUR"), nil
			}
			// TLL alt leg (100) + ground cost (30 EUR) = 130 vs 200 direct -> 70 saved.
			return positioningFlightResult(100, "EUR"), nil
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

// TestDetectPositioning_nonEURTargetSuppressedWhenInconvertible covers case
// (b): a non-EUR target with no usable rate table must suppress rather than
// show an unconverted or mislabelled figure. Placeholder currency codes
// (ZZZ/ZZY) are used by convention (see currency_sweep_test.go) since they
// appear in no real exchange-rate table, and the context is cancelled so no
// live FX fetch can populate the cache either.
func TestDetectPositioning_nonEURTargetSuppressedWhenInconvertible(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	hacks := detectPositioning(ctx, DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		Date:        "2026-06-01",
		Currency:    "ZZY",
		SearchOverride: func(_ context.Context, _, _, _ string, _ flights.SearchOptions) (*models.FlightSearchResult, error) {
			return positioningFlightResult(200, "ZZZ"), nil
		},
	})
	if len(hacks) != 0 {
		t.Errorf("expected suppression when direct flight is inconvertible to target, got %d hacks", len(hacks))
	}
}

// TestDetectPositioning_hackCurrencyMatchesTarget covers case (c): whenever a
// hack surfaces, its Currency field equals the normalized target currency.
func TestDetectPositioning_hackCurrencyMatchesTarget(t *testing.T) {
	hacks := detectPositioning(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		Date:        "2026-06-01",
		Currency:    "eur", // lower-case input must normalize to EUR
		SearchOverride: func(_ context.Context, origin, _, _ string, _ flights.SearchOptions) (*models.FlightSearchResult, error) {
			if origin == "HEL" {
				return positioningFlightResult(200, "EUR"), nil
			}
			return positioningFlightResult(100, "EUR"), nil
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

// TestDetectPositioning_groundCostInconvertibleSkipsCandidate verifies that a
// candidate whose EUR ground-cost estimate cannot be converted into the
// target is skipped even though the flight legs themselves are convertible.
//
// The flight legs are priced directly in the target currency ("XXX", ISO
// 4217's reserved "no currency" code — guaranteed absent from any real or
// live rate table) so their own conversion succeeds via ConvertCurrency's
// identity short-circuit (from == to, no network call). Only the
// EUR-denominated static ground-cost estimate then needs an actual
// EUR-><target> lookup; the cancelled context makes that live fetch fail
// deterministically offline, so it has no rate and is dropped. This proves
// the candidate is skipped specifically because its ground cost could not be
// converted — not because a flight leg was inconvertible (that path is
// already covered by TestDetectPositioning_nonEURTargetSuppressedWhenInconvertible).
// Previously this test left target=EUR, where ConvertCurrency's identity
// short-circuit made GroundCost trivially "convert" too, so the drop path was
// never actually exercised.
func TestDetectPositioning_groundCostInconvertibleSkipsCandidate(t *testing.T) {
	const target = "XXX"

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	hacks := detectPositioning(ctx, DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		Date:        "2026-06-01",
		Currency:    target,
		SearchOverride: func(_ context.Context, origin, _, _ string, _ flights.SearchOptions) (*models.FlightSearchResult, error) {
			if origin == "HEL" {
				return positioningFlightResult(200, target), nil
			}
			return positioningFlightResult(100, target), nil
		},
	})
	if len(hacks) != 0 {
		t.Errorf("expected the candidate to be dropped when its ground cost cannot be converted, got %d hacks", len(hacks))
	}
}
