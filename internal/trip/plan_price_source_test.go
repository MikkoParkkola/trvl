package trip

import (
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestExtractTopHotels_PriceSourceIsProviderID pins what PlanHotel.PriceSource
// holds: the hotel's headline_provider, a trvl provider id, never the seller
// that offered the room (#808). The renderer maps the id to a label.
func TestExtractTopHotels_PriceSourceIsProviderID(t *testing.T) {
	h := models.HotelResult{
		Name: "MEININGER Hotel Wien Downtown Franz",
		Sources: []models.PriceSource{
			{Provider: "google_hotels", Price: 40, Currency: "EUR"},
			{
				Provider: "google_hotels", Seller: "MEININGER Hotel Wien Downtown Franz",
				Price: 45, Currency: "EUR",
				PriceBasis: models.PriceBasisRoomNightly, PriceConfidence: models.PriceConfidenceRoomLevel,
			},
		},
	}
	models.FinalizeHotelResultPriceTrust(&h, "EUR", time.Now(), nil)

	got := extractTopHotels([]models.HotelResult{h}, 2, 5)
	if len(got) != 1 {
		t.Fatalf("got %d hotels, want the room-rate hotel", len(got))
	}
	if got[0].PriceSource != "google_hotels" {
		t.Errorf("PriceSource = %q, want the provider id google_hotels", got[0].PriceSource)
	}
	if got[0].PerNight != 45 || got[0].PriceConfidence != models.PriceConfidenceRoomLevel {
		t.Errorf("got %v at %q, want the 45 EUR room_level rate", got[0].PerNight, got[0].PriceConfidence)
	}
}
