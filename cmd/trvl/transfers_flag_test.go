package main

import (
	"context"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// The CLI never prints "Direct" for a change count it does not know.
func TestTransfersFlag_CLILabels(t *testing.T) {
	models.UseColor = false
	_, _, rows := prepareGroundRows(context.Background(), "", []models.GroundRoute{
		{Provider: "oebb", Type: "train", Price: 30, Currency: "EUR", PriceProvenance: models.PriceLive, TransfersUnknown: true},
	})
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0][4] != "unknown" {
		t.Errorf("transfers cell = %q, want \"unknown\"", rows[0][4])
	}

	out := captureStdout(t, func() {
		printItinerary(1, models.RouteItinerary{TotalPrice: 30, Currency: "EUR", TotalDuration: 311, TransfersUnknown: true}, "")
	})
	if strings.Contains(out, "direct") || !strings.Contains(out, "transfers unknown") {
		t.Errorf("itinerary line = %q, want \"transfers unknown\" and no \"direct\"", out)
	}
}
