package hacks

import (
	"context"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/ground"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestGroundLeg_IgnoresNonLiveRoutes pins D1 through a detector that prices a
// ground leg: only a live fare counts as a live ground quote. An estimated
// route (static table, range low) and an unavailable one are ignored, so the
// leg falls back to the detector's own static estimate and is flagged
// estimated rather than presented as a quote.
func TestGroundLeg_IgnoresNonLiveRoutes(t *testing.T) {
	withConvertCurrencyFn(t, eurToUSDConvert)
	hacks := detectMultiModalPositioning(context.Background(), DetectorInput{
		Origin:         "HEL",
		Destination:    "BCN",
		Date:           "2026-06-01",
		Currency:       "USD",
		SearchOverride: chMultiModalFlights,
		GroundSearchOverride: func(context.Context, string, string, string, ground.SearchOptions) (*models.GroundSearchResult, error) {
			return &models.GroundSearchResult{Success: true, Routes: []models.GroundRoute{
				{Provider: "rome2rio", Type: "bus", Price: 10, Currency: "EUR", PriceProvenance: models.PriceEstimated},
				{Provider: "transitous", Type: "train", Price: 0, PriceProvenance: models.PriceUnavailable},
			}}, nil
		},
	})
	if len(hacks) != 1 {
		t.Fatalf("expected exactly 1 hack, got %d: %+v", len(hacks), hacks)
	}
	if !strings.Contains(hacks[0].Description, estimatedFareMarker) {
		t.Errorf("a leg priced only by non-live routes must fall back to the flagged estimate; description = %q", hacks[0].Description)
	}
}
