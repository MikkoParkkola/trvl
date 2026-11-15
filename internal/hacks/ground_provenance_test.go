package hacks

import (
	"context"
	"math"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/ground"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestGroundLegPriceInTarget_IgnoresNonLiveRoutes pins D1: only a live fare
// counts as a live ground quote. An estimated route (static table, range low)
// is ignored, so the leg falls back to the detector's own static estimate and
// is flagged estimated rather than presented as a quote.
func TestGroundLegPriceInTarget_IgnoresNonLiveRoutes(t *testing.T) {
	withConvertCurrencyFn(t, eurToUSDConvert)
	override := func(context.Context, string, string, string, ground.SearchOptions) (*models.GroundSearchResult, error) {
		return &models.GroundSearchResult{Success: true, Routes: []models.GroundRoute{
			{Provider: "rome2rio", Type: "bus", Price: 10, Currency: "EUR", PriceProvenance: models.PriceEstimated},
			{Provider: "transitous", Type: "train", Price: 0, PriceProvenance: models.PriceUnavailable},
		}}, nil
	}
	price, ok, estimated := groundLegPriceInTarget(context.Background(), "Helsinki", "Tallinn", "2026-07-01", "", 100, "USD", override)
	if !ok || !estimated {
		t.Fatalf("ok=%v estimated=%v, want the static fallback (ok, estimated)", ok, estimated)
	}
	if math.Abs(price-110) > 1e-9 {
		t.Errorf("price = %v USD, want 110 (static EUR 100 converted), not the estimated route", price)
	}
}
