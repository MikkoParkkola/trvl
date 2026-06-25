package hotels

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestSortHotelsLeadsWithRoomLevelPriceConfidence verifies the confidence-aware
// secondary sort: a real per-night room price (room_level/verified) leads even
// when a headline-only teaser (unverified) is cheaper, and zero-price listings
// stay demoted to the end. Confidence values mirror the constants in
// internal/models/hotel_price_trust.go.
func TestSortHotelsLeadsWithRoomLevelPriceConfidence(t *testing.T) {
	hotels := []models.HotelResult{
		{Name: "Google headline", Price: 80, PriceConfidence: models.PriceConfidenceUnverified},
		{Name: "Agoda room", Price: 110, PriceConfidence: models.PriceConfidenceRoomLevel},
		{Name: "No price", Price: 0, PriceConfidence: models.PriceConfidenceUnverified},
		{Name: "Booking verified", Price: 130, PriceConfidence: models.PriceConfidenceVerified},
	}

	sortHotels(hotels, "cheapest", 0, 0)

	gotOrder := make([]string, len(hotels))
	for i, h := range hotels {
		gotOrder[i] = h.Name
	}
	wantOrder := []string{"Booking verified", "Agoda room", "Google headline", "No price"}
	for i := range wantOrder {
		if gotOrder[i] != wantOrder[i] {
			t.Fatalf("sort order = %v, want %v", gotOrder, wantOrder)
		}
	}

	// The pricier room-level provider must lead the cheaper headline teaser.
	if hotels[0].Name != "Booking verified" || hotels[1].Name != "Agoda room" {
		t.Errorf("room-level/verified providers did not lead: got %q then %q", hotels[0].Name, hotels[1].Name)
	}
	// Zero-price listing must stay last.
	if hotels[len(hotels)-1].Price != 0 {
		t.Errorf("zero-price listing not demoted to end: %+v", hotels[len(hotels)-1])
	}
}

// TestLessPriceConfidenceTiers checks the comparator directly across tiers.
func TestLessPriceConfidenceTiers(t *testing.T) {
	roomLevel := models.HotelResult{Price: 110, PriceConfidence: models.PriceConfidenceRoomLevel}
	headline := models.HotelResult{Price: 80, PriceConfidence: models.PriceConfidenceUnverified}
	empty := models.HotelResult{Price: 90, PriceConfidence: ""}
	zero := models.HotelResult{Price: 0, PriceConfidence: models.PriceConfidenceRoomLevel}

	if !lessPrice(roomLevel, headline) {
		t.Error("pricier room_level should sort before cheaper unverified")
	}
	if lessPrice(headline, roomLevel) {
		t.Error("cheaper unverified should not sort before room_level")
	}
	// Empty confidence ranks the same as unverified (lowest priced tier).
	if !lessPrice(headline, empty) {
		t.Error("within lowest tier, cheaper price should win (80 < 90)")
	}
	// Zero price always sinks below any priced entry.
	if lessPrice(zero, headline) {
		t.Error("zero-price entry should never sort before a priced one")
	}
	if !lessPrice(headline, zero) {
		t.Error("priced entry should sort before a zero-price entry")
	}
}
