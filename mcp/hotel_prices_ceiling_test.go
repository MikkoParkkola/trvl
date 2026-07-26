package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/booking"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/pricefeed"
)

// TestHotelPricesSchema_DocumentsTheCeiling guards the field an agent needs in
// order to read a capped verdict correctly.
//
// An external tester saw "caution" on six indexed hotels and could not tell a
// structurally capped path from a property-specific finding. Fixing that in the
// CLI while leaving the MCP surface silent would fix it for one reader and not the
// one that matters most: an agent acting without a human checking the result.
func TestHotelPricesSchema_DocumentsTheCeiling(t *testing.T) {
	raw, err := json.Marshal(hotelPricesOutputSchema())
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	s := string(raw)

	if !strings.Contains(s, "booking_readiness_ceiling") {
		t.Fatal("the schema omits booking_readiness_ceiling; an agent has no way to learn the verdict is capped")
	}
	if !strings.Contains(s, "hotel_rooms") {
		t.Fatal("the ceiling description should point at the endpoint that can reach ready, or the agent knows it is stuck without knowing the way out")
	}
}

// TestHotelPricesReadiness_CeilingIsSerialisable proves the value the response
// carries is actually populated for this endpoint, rather than the field existing
// and always being empty.
func TestHotelPricesReadiness_CeilingIsSerialisable(t *testing.T) {
	providers := []models.ProviderPrice{{
		Provider:        "Booking.com",
		Price:           199,
		Currency:        "EUR",
		LinkDurability:  "stable",
		PriceConfidence: models.PriceConfidenceVerified,
	}}

	v := pricefeed.HotelPricesReadiness("google-hotel-id", providers)

	if !v.Capped() {
		t.Fatal("hotel-prices must report a ceiling; the response field would otherwise always be empty")
	}
	if v.Ceiling != booking.Caution {
		t.Fatalf("expected a caution ceiling, got %q", v.Ceiling)
	}
	if len(v.CeilingReasons) == 0 {
		t.Fatal("expected at least one reason naming the unobtainable signal")
	}
}
