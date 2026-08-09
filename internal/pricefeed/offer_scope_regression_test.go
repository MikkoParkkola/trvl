package pricefeed

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/booking"
	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// Regression coverage for the v1.21.0 result-integrity report: readiness must
// describe one concrete room offer, not a synthetic offer assembled from the
// strongest signal found anywhere in the response.
func TestRoomsReadinessDoesNotCombineSignalsAcrossRooms(t *testing.T) {
	refundable := true
	result := &hotels.RoomAvailability{
		HotelID: "/g/continental-mare",
		Rooms: []hotels.RoomType{
			{
				Name:        "verified offer without cancellation terms",
				Provider:    "seller-a",
				ProviderURL: "https://seller-a.example/book",
				Readiness:   hotels.ReadinessCaution,
				InventoryOptions: []models.RoomInventoryQuote{{
					PriceConfidence: models.PriceConfidenceVerified,
				}},
			},
			{
				Name:       "cancellation terms without a verified booking link",
				Provider:   "seller-b",
				Refundable: &refundable,
				Readiness:  hotels.ReadinessCaution,
			},
		},
	}

	got := RoomsReadiness(result)
	if got.Readiness != booking.Caution {
		t.Fatalf("aggregate readiness = %q, want %q when no individual room is ready (reasons: %v)",
			got.Readiness, booking.Caution, got.Reasons)
	}
}

// The prices verdict uses the cheapest seller's price, verification, and link.
// Its refundability signal must therefore come from that same seller.
func TestHotelPricesReadinessKeepsRefundabilitySellerScoped(t *testing.T) {
	refundable := true
	providers := []models.ProviderPrice{
		{
			Provider:        "cheapest-seller",
			Price:           100,
			Currency:        "EUR",
			ProviderURL:     "https://cheapest.example/book",
			PriceConfidence: models.PriceConfidenceVerified,
			LinkDurability:  "stable",
		},
		{
			Provider:         "other-seller",
			Price:            150,
			Currency:         "EUR",
			FreeCancellation: &refundable,
		},
	}

	got := HotelPricesReadiness("/g/example", providers)
	if got.Readiness != booking.Caution {
		t.Fatalf("cheapest-seller readiness = %q, want %q when only another seller states cancellation terms (reasons: %v)",
			got.Readiness, booking.Caution, got.Reasons)
	}
}
