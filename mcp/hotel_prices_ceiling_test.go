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

func TestHotelPricesSchemaExposesFactualTrustSignals(t *testing.T) {
	raw, err := json.Marshal(hotelPricesOutputSchema())
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	schema := string(raw)
	for _, field := range []string{"official", "free_cancellation", "free_cancellation_until"} {
		if !strings.Contains(schema, field) {
			t.Errorf("hotel-prices MCP schema omits %q", field)
		}
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

// TestCappedVerdictStillCarriesPropertyFindings guards against the schema
// telling an agent to ignore a real problem.
//
// An earlier version of the ceiling description said "do NOT read the verdict as
// a finding about this hotel". That is false whenever a capped verdict also
// carries a genuine failure: this endpoint can report an expiring link or an
// unverified price, and an agent following that instruction would discard a
// warning that matters more than the ceiling does. The two kinds of reason live
// in separate fields precisely so both stay actionable.
func TestCappedVerdictStillCarriesPropertyFindings(t *testing.T) {
	// Expiring link: a real finding about this offer, on a path that is also capped.
	providers := []models.ProviderPrice{{
		Provider:        "Booking.com",
		Price:           199,
		Currency:        "EUR",
		LinkDurability:  "expiring",
		PriceConfidence: models.PriceConfidenceVerified,
	}}

	v := pricefeed.HotelPricesReadiness("google-hotel-id", providers)

	if !v.Capped() {
		t.Fatal("expected the path to remain capped")
	}

	var sawLink bool
	for _, r := range v.Reasons {
		if strings.Contains(r, "link_stable") && strings.Contains(r, "false") {
			sawLink = true
		}
	}
	if !sawLink {
		t.Fatalf("the expiring link disappeared from the actionable reasons; reasons were %v", v.Reasons)
	}

	// The ceiling reasons must not absorb it: they name only what is unobtainable.
	for _, r := range v.CeilingReasons {
		if strings.Contains(r, "link_stable") {
			t.Fatalf("a real finding was filed as a structural limit: %q", r)
		}
	}
}

// TestCeilingSchema_DoesNotTellAgentsToIgnoreFindings pins the wording itself,
// since the defect was in the instruction rather than the data.
func TestCeilingSchema_DoesNotTellAgentsToIgnoreFindings(t *testing.T) {
	raw, err := json.Marshal(hotelPricesOutputSchema())
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	s := string(raw)

	if strings.Contains(s, "do NOT read the verdict as a finding about this hotel") {
		t.Fatal("the schema tells agents to disregard the verdict when capped; a capped verdict can still carry a genuine expiring-link or price finding")
	}
	if !strings.Contains(s, "booking_readiness_reasons may still report") {
		t.Fatal("the ceiling description should say other reasons remain actionable, or an agent has to infer it")
	}
}
