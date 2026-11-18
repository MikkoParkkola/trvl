package transfer

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestBuildOptions_ProvenanceDrivesEstimateAndTotal pins D1 for transfers: an
// estimated ground price is flagged as an estimate, a live one is not, and an
// unavailable route is marked price_unavailable so renderers show no total;
// TotalPrice itself is unchanged, since a walk can honestly cost 0.
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
	if !unavail.PriceUnavailable || unavail.TotalPrice != 0 {
		t.Errorf("unavailable option = price_unavailable %v total %v, want true and the total left at 0", unavail.PriceUnavailable, unavail.TotalPrice)
	}
	if live.PriceUnavailable || est.PriceUnavailable {
		t.Errorf("priced options marked unavailable: live %v estimated %v", live.PriceUnavailable, est.PriceUnavailable)
	}
	raw, err := json.Marshal(unavail)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"price_unavailable":true`) {
		t.Errorf("unavailable option JSON lacks price_unavailable: %s", raw)
	}
}
