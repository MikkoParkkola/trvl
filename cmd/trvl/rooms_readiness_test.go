package main

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/booking"
	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// refBool returns a pointer to b, eliminating the &local dance in table cells.
func refBool(b bool) *bool { return &b }

// TestBookingReadinessForRooms_NoRefundabilitySignal verifies that when no room
// carries any refundability evidence the verdict is downgraded below "ready".
func TestBookingReadinessForRooms_NoRefundabilitySignal(t *testing.T) {
	result := &hotels.RoomAvailability{
		HotelID: "/g/11b6d4_v_4",
		Rooms: []hotels.RoomType{
			{
				Name:        "Standard",
				ProviderURL: "https://www.booking.com/hotel/x.html",
				InventoryOptions: []models.RoomInventoryQuote{
					{PriceConfidence: models.PriceConfidenceVerified},
				},
				// Refundable, FreeCancellation, CancellationPolicy all zero-value.
			},
		},
	}

	v := bookingReadinessForRooms(result)
	if v.Readiness == booking.Ready {
		t.Errorf("expected non-ready when refundability is absent, got %s", v.Readiness)
	}
}

// TestBookingReadinessForRooms_AllUnverifiedPriceConfidence verifies that when
// every InventoryOption carries "unverified" PriceConfidence the Verified signal
// is set to false, contributing a downgrade reason.
func TestBookingReadinessForRooms_AllUnverifiedPriceConfidence(t *testing.T) {
	result := &hotels.RoomAvailability{
		HotelID: "/g/11b6d4_v_4",
		Rooms: []hotels.RoomType{
			{
				Name:             "Deluxe",
				FreeCancellation: refBool(true),
				ProviderURL:      "https://www.booking.com/hotel/x.html",
				InventoryOptions: []models.RoomInventoryQuote{
					{PriceConfidence: models.PriceConfidenceUnverified},
					{PriceConfidence: models.PriceConfidenceUnverified},
				},
			},
		},
	}

	v := bookingReadinessForRooms(result)
	if v.Readiness == booking.Ready {
		t.Errorf("expected non-ready for all-unverified confidence, got ready")
	}
}

// TestBookingReadinessForRooms_ReadyReachableWithStableLink verifies that a room
// with a durable (stable) booking link plus refundability, identity, and a
// verified price reaches the "ready" verdict — the rooms endpoint is where all
// four signals can be present at once.
func TestBookingReadinessForRooms_ReadyReachableWithStableLink(t *testing.T) {
	result := &hotels.RoomAvailability{
		HotelID: "/g/11b6d4_v_4",
		Rooms: []hotels.RoomType{
			{
				Name:               "Superior",
				Refundable:         refBool(true),
				FreeCancellation:   refBool(true),
				CancellationPolicy: "free_cancellation",
				ProviderURL:        "https://www.booking.com/hotel/x.html",
				InventoryOptions: []models.RoomInventoryQuote{
					{PriceConfidence: models.PriceConfidenceVerified},
				},
			},
		},
	}

	v := bookingReadinessForRooms(result)
	if v.Readiness != booking.Ready {
		t.Errorf("expected ready with stable link + refundability + identity + verified, got %s (reasons: %v)", v.Readiness, v.Reasons)
	}
}

// TestBookingReadinessForRooms_ExpiringLinkDowngrades verifies that an expiring
// ad-click link sets LinkStable false and downgrades the verdict below ready.
func TestBookingReadinessForRooms_ExpiringLinkDowngrades(t *testing.T) {
	result := &hotels.RoomAvailability{
		HotelID: "/g/11b6d4_v_4",
		Rooms: []hotels.RoomType{
			{
				Name:               "Superior",
				Refundable:         refBool(true),
				CancellationPolicy: "free_cancellation",
				ProviderURL:        "https://www.google.com/aclk?adurl=x",
				InventoryOptions: []models.RoomInventoryQuote{
					{PriceConfidence: models.PriceConfidenceVerified},
				},
			},
		},
	}

	v := bookingReadinessForRooms(result)
	if v.Readiness == booking.Ready {
		t.Errorf("expiring link must downgrade below ready, got ready")
	}
}
