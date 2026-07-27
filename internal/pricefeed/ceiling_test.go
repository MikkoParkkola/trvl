package pricefeed

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestHotelPricesReadiness_DeclaresItsCeiling is the regression test for the
// report that started this: six indexed properties all returned Caution, and
// nothing in the output distinguished "these six are uncertain" from "this
// command never says better than Caution".
//
// The hotel-prices endpoint carries no cancellation terms, so refundability is
// unobtainable and Ready is unreachable here regardless of the property. The
// verdict has to say that, or a reader infers a finding that was never made.
func TestHotelPricesReadiness_DeclaresItsCeiling(t *testing.T) {
	// A property with everything this endpoint *can* establish, all positive.
	providers := []models.ProviderPrice{{
		Provider:        "Booking.com",
		Price:           199,
		Currency:        "EUR",
		LinkDurability:  "stable",
		PriceConfidence: models.PriceConfidenceVerified,
	}}

	v := HotelPricesReadiness("google-hotel-id", providers)

	if !v.Capped() {
		t.Fatal("hotel-prices must declare its ceiling; without it a caution verdict reads as a judgement about the hotel")
	}
	if v.Readiness == "ready" {
		t.Fatal("this endpoint cannot establish refundability, so ready must be unreachable")
	}
	found := false
	for _, r := range v.CeilingReasons {
		if r == "refundability_known not available from this source" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the ceiling to name refundability as unobtainable, got %v", v.CeilingReasons)
	}
}

// TestRoomsReadiness_IsNotCapped is the contrast that makes the flag useful. The
// room-level path carries cancellation terms, so Ready is genuinely reachable and
// a Caution there really is about the property. Marking both paths capped would
// make the signal noise.
func TestRoomsReadiness_IsNotCapped(t *testing.T) {
	v := RoomsReadiness(nil)

	if v.Capped() {
		t.Fatalf("the rooms path can supply every signal and must not claim a ceiling, got %q", v.Ceiling)
	}
}
