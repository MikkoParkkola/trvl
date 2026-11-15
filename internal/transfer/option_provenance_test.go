package transfer

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestBuildOptions_ProvenanceDrivesEstimateAndTotal pins D1 for transfers: an
// estimated ground price is flagged as an estimate, a live one is not, and an
// unavailable route carries no total at all instead of a 0 that reads as free.
func TestBuildOptions_ProvenanceDrivesEstimateAndTotal(t *testing.T) {
	routes := []models.GroundRoute{
		{Provider: "flixbus", Type: "bus", Price: 22, Currency: "EUR", Duration: 40, PriceProvenance: models.PriceLive},
		{Provider: "rome2rio", Type: "bus", Price: 18, Currency: "EUR", Duration: 45, PriceProvenance: models.PriceEstimated},
		{Provider: "transitous", Type: "train", Price: 0, Duration: 30, PriceProvenance: models.PriceUnavailable},
	}
	cmp := BuildOptions(routes, "", "Airport", "Centre")
	if len(cmp.Options) < 3 {
		t.Fatalf("want at least 3 options, got %d", len(cmp.Options))
	}
	live, est, unavail := cmp.Options[0], cmp.Options[1], cmp.Options[2]
	if live.PriceIsEstimate {
		t.Error("live fare flagged as an estimate")
	}
	if !est.PriceIsEstimate {
		t.Error("estimated fare not flagged as an estimate")
	}
	if unavail.PriceIsEstimate {
		t.Error("unavailable fare flagged as an estimate; it has no price at all")
	}
	raw, err := json.Marshal(unavail)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), `"total_price"`) {
		t.Errorf("unavailable option JSON carries a total: %s", raw)
	}
}
