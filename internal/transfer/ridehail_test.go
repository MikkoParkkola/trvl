package transfer

import (
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

func TestRideHailOptions_Success(t *testing.T) {
	opts := RideHailOptions("BCN airport", "Hotel Eixample")
	if len(opts) != 2 {
		t.Fatalf("want Uber + Bolt, got %d", len(opts))
	}
	for _, o := range opts {
		if o.Mode != "ride_hail" {
			t.Errorf("mode = %q, want ride_hail", o.Mode)
		}
		if o.BookURL == "" || !strings.Contains(o.BookURL, "Eixample") {
			t.Errorf("deep-link should encode the destination, got %q", o.BookURL)
		}
		if o.TotalPrice != 0 || o.DoorToDoorMin != 0 {
			t.Errorf("ride-hail price/duration must be unknown (0), got %v/%v", o.TotalPrice, o.DoorToDoorMin)
		}
		if len(o.Steps) == 0 || !o.Steps[0].Grounded {
			t.Errorf("ride-hail steps should be present and grounded (app instructions)")
		}
	}
}

func TestRideHailOptions_EmptyEndpoints(t *testing.T) {
	if RideHailOptions("", "x") != nil || RideHailOptions("x", "") != nil {
		t.Error("missing endpoints must yield no ride-hail options")
	}
}

// TestBuildOptions_RideHailDoesNotStealLabels guards that ride-hail's unknown
// price/duration never wins cheapest/fastest/best-value.
func TestBuildOptions_RideHailDoesNotStealLabels(t *testing.T) {
	routes := []models.GroundRoute{
		{Provider: "metro", Type: "train", Price: 5.15, Currency: "EUR", Duration: 48, Transfers: 2},
		{Provider: "taxi", Type: "taxi", Price: 35, Currency: "EUR", Duration: 25},
	}
	cmp := BuildOptions(routes, "BCN", "BCN airport", "Hotel Eixample")
	if cmp.Cheapest == "ride_hail" || cmp.Fastest == "ride_hail" || cmp.BestValue == "ride_hail" {
		t.Errorf("ride-hail (unknown price/time) must not win labels: cheapest=%q fastest=%q best=%q",
			cmp.Cheapest, cmp.Fastest, cmp.BestValue)
	}
	if cmp.Cheapest != "metro" {
		t.Errorf("cheapest should remain metro, got %q", cmp.Cheapest)
	}
}
