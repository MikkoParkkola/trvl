package hotels

import (
	"context"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestEnrichHotelRooms_AttachesRoomLevelPricing proves the core search room
// enrichment drills into a hotel with a HotelID and attaches the drilled-down
// room as verified (tax-inclusive) inventory, via the offline seam.
func TestEnrichHotelRooms_AttachesRoomLevelPricing(t *testing.T) {
	orig := fetchRoomAvailability
	t.Cleanup(func() { fetchRoomAvailability = orig })

	// The rate manager is a package-level singleton; other tests may have
	// throttled "google". Reset it so enrichment is not skipped here.
	origRM := HotelRateManager
	t.Cleanup(func() { HotelRateManager = origRM })
	HotelRateManager = NewRateManager()

	tt := true
	fetchRoomAvailability = func(_ context.Context, _ RoomSearchOptions) (*RoomAvailability, error) {
		return &RoomAvailability{
			Success: true,
			Rooms: []RoomType{{
				Name:              "Deluxe Double Room",
				Price:             120,
				NightlyPrice:      120,
				TotalPrice:        240,
				Currency:          "EUR",
				TaxesFeesIncluded: &tt,
			}},
		}, nil
	}

	hotels := []models.HotelResult{{HotelID: "/g/abc", Name: "Hotel X"}}
	out := enrichHotelRooms(context.Background(), hotels, HotelSearchOptions{
		EnrichRooms: true,
		CheckIn:     "2026-07-10",
		CheckOut:    "2026-07-11",
		Currency:    "EUR",
	})

	if len(out[0].RoomTypes) != 1 {
		t.Fatalf("RoomTypes len = %d, want 1", len(out[0].RoomTypes))
	}
	rt := out[0].RoomTypes[0]
	if rt.Name != "Deluxe Double Room" {
		t.Errorf("room name = %q, want Deluxe Double Room", rt.Name)
	}
	if rt.PriceConfidence != models.PriceConfidenceVerified {
		t.Errorf("PriceConfidence = %q, want verified", rt.PriceConfidence)
	}
	if rt.PriceBasis != models.PriceBasisTaxInclusiveTotal {
		t.Errorf("PriceBasis = %q, want tax_inclusive_total", rt.PriceBasis)
	}
}

// TestEnrichHotelRooms_SkipsWithoutHotelID proves a hotel lacking a HotelID is
// left untouched and the drill-down seam is never invoked for it.
func TestEnrichHotelRooms_SkipsWithoutHotelID(t *testing.T) {
	orig := fetchRoomAvailability
	t.Cleanup(func() { fetchRoomAvailability = orig })

	called := false
	fetchRoomAvailability = func(_ context.Context, _ RoomSearchOptions) (*RoomAvailability, error) {
		called = true
		return &RoomAvailability{Success: true, Rooms: []RoomType{{Name: "x"}}}, nil
	}

	hotels := []models.HotelResult{{Name: "No ID Hotel"}}
	out := enrichHotelRooms(context.Background(), hotels, HotelSearchOptions{EnrichRooms: true})

	if called {
		t.Error("fetchRoomAvailability was called for a hotel without a HotelID")
	}
	if len(out[0].RoomTypes) != 0 {
		t.Errorf("RoomTypes len = %d, want 0 (untouched)", len(out[0].RoomTypes))
	}
}
