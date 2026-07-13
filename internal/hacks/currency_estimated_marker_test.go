package hacks

import (
	"context"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/ground"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// ---------------------------------------------------------------------------
// Estimated-fare provenance marker (see groundLegPriceInTarget in currency.go).
// A ground/ferry leg priced from the static EUR fallback must render an
// "(estimated fare)" marker; a live provider quote must not. No t.Parallel:
// convertCurrencyFn is a shared package-level var restored via t.Cleanup.
// ---------------------------------------------------------------------------

const estimatedFareMarker = "estimated fare"

// chLiveGroundResult returns a successful single-route ground search priced in
// the given currency — exercising the live-quote branch of
// groundLegPriceInTarget (estimated=false).
func chLiveGroundResult(price float64, currency string) *models.GroundSearchResult {
	return &models.GroundSearchResult{
		Success: true,
		Routes:  []models.GroundRoute{{Provider: "flixbus", Type: "bus", Price: price, Currency: currency}},
	}
}

func chMultiModalFlights(_ context.Context, origin, _, _ string, _ flights.SearchOptions) (*models.FlightSearchResult, error) {
	switch origin {
	case "HEL":
		return chSearchResult(400, "EUR"), nil // direct: 440 USD
	case "TLL":
		return chSearchResult(100, "EUR"), nil // hub->dest: 110 USD
	default:
		return &models.FlightSearchResult{Success: false}, nil // RIX, ARN dropped
	}
}

// TestGroundLegEstimatedFareMarker_presentOnStaticFallback: with no live ground
// quote the detector falls back to the static EUR estimate, and the rendered
// description flags the fare as estimated so the number is honest.
func TestGroundLegEstimatedFareMarker_presentOnStaticFallback(t *testing.T) {
	withConvertCurrencyFn(t, eurToUSDConvert)

	hacks := detectMultiModalPositioning(context.Background(), DetectorInput{
		Origin:               "HEL",
		Destination:          "BCN",
		Date:                 "2026-06-01",
		Currency:             "USD",
		SearchOverride:       chMultiModalFlights,
		GroundSearchOverride: chFailedGroundSearch, // no live quote -> static estimate
	})

	if len(hacks) != 1 {
		t.Fatalf("expected exactly 1 hack, got %d: %+v", len(hacks), hacks)
	}
	if !strings.Contains(hacks[0].Description, estimatedFareMarker) {
		t.Errorf("static-estimate fare should be flagged %q; description = %q", estimatedFareMarker, hacks[0].Description)
	}
}

// TestGroundLegEstimatedFareMarker_absentOnLiveQuote: a live provider quote
// priced the ground leg, so it must not be mislabelled as an estimate.
func TestGroundLegEstimatedFareMarker_absentOnLiveQuote(t *testing.T) {
	withConvertCurrencyFn(t, eurToUSDConvert)

	hacks := detectMultiModalPositioning(context.Background(), DetectorInput{
		Origin:         "HEL",
		Destination:    "BCN",
		Date:           "2026-06-01",
		Currency:       "USD",
		SearchOverride: chMultiModalFlights,
		GroundSearchOverride: func(_ context.Context, _, _, _ string, _ ground.SearchOptions) (*models.GroundSearchResult, error) {
			return chLiveGroundResult(10, "EUR"), nil // live quote -> 11 USD, not an estimate
		},
	})

	if len(hacks) != 1 {
		t.Fatalf("expected exactly 1 hack, got %d: %+v", len(hacks), hacks)
	}
	if strings.Contains(hacks[0].Description, estimatedFareMarker) {
		t.Errorf("live-quote fare must not be flagged as estimated; description = %q", hacks[0].Description)
	}
}
