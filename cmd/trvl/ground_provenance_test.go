package main

import (
	"context"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestPrepareGroundRows_ProvenanceMarkers pins D1's CLI table: an estimated
// price reads "~€22 (est.)", an unavailable one reads "—", and a live fare
// keeps its plain amount.
func TestPrepareGroundRows_ProvenanceMarkers(t *testing.T) {
	routes := []models.GroundRoute{
		{Provider: "flixbus", Type: "bus", Price: 22, Currency: "EUR", PriceProvenance: models.PriceLive},
		{Provider: "vikingline", Type: "ferry", Price: 22, Currency: "EUR", PriceProvenance: models.PriceEstimated},
		{Provider: "transitous", Type: "train", Price: 0, PriceProvenance: models.PriceUnavailable},
	}
	_, _, rows := prepareGroundRows(context.Background(), "", routes)
	if len(rows) != 3 {
		t.Fatalf("want 3 rows, got %d", len(rows))
	}
	if cell := rows[0][0]; !strings.Contains(cell, "22.00") || strings.Contains(cell, "est.") {
		t.Errorf("live price cell = %q, want the plain amount without an estimate marker", cell)
	}
	if cell := rows[1][0]; !strings.Contains(cell, "~€22 (est.)") {
		t.Errorf("estimated price cell = %q, want it to contain \"~€22 (est.)\"", cell)
	}
	if cell := rows[2][0]; !strings.Contains(cell, "—") {
		t.Errorf("unavailable price cell = %q, want it to contain \"—\"", cell)
	}
}
