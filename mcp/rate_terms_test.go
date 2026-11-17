package mcp

import (
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// googlePricedRoom is a merged room whose top level is Hotels.com's quote,
// with its own refund and board terms (#823).
func googlePricedRoom(seller string) hotels.RoomType {
	yes := true
	return hotels.RoomType{
		Name: "Double Room", Price: 200, TotalPrice: 200, Currency: "EUR",
		Provider: seller, ProviderURL: "https://www.hotels.com/ho1",
		MatchConfidence: models.RoomInventoryMatchExact,
		Refundable:      &yes, Board: "breakfast_included",
	}
}

var rateTermsNeed = models.AccommodationNeed{Adults: 2, Rooms: 1, Currency: "EUR"}

// Another seller's quote never borrows the room's seller-bound fields, even
// explicit false values; the stay fields still fill.
func TestCompleteRoomInventoryQuote_ForeignSellerKeepsItsGaps(t *testing.T) {
	now := time.Now()
	no := false
	room := hotels.RoomType{
		Name: "Double Room", NightlyPrice: 210, TotalPrice: 420, TaxesAndFees: 30, TaxesFeesIncluded: &no,
		Currency: "EUR", Provider: "Hotels.com", ProviderURL: "https://www.hotels.com/ho1",
		MatchConfidence: models.RoomInventoryMatchExact, CancellationPolicy: "Non-refundable",
		Refundable: &no, FreeCancellation: &no, Board: "room_only", BreakfastIncluded: &no,
	}
	foreign := models.RoomInventoryQuote{Provider: "Booking.com", ProviderRoomName: "Double Room", Currency: "EUR"}
	got := completeRoomInventoryQuote(foreign, room, rateTermsNeed, now)
	if got.ProviderURL != "" || got.NightlyPrice != 0 || got.TotalPrice != 0 || got.TaxesAndFees != 0 || got.TaxesFeesIncluded != nil ||
		got.PriceBasis != "" || got.PriceConfidence != "" || got.CancellationPolicy != "" || got.Refundable != nil ||
		got.FreeCancellation != nil || got.Board != "" || got.BreakfastIncluded != nil {
		t.Errorf("Booking quote borrowed Hotels.com's fields: %+v", got)
	}
	if got.Rooms != 1 || got.CheckedAt.IsZero() || got.Freshness == "" || got.OccupancyAdults != 2 {
		t.Errorf("stay fields not filled: %+v", got)
	}
}

// The room's own seller still fills its gaps, also when the names differ only
// in case and spacing.
func TestCompleteRoomInventoryQuote_OwnSellerFills(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct{ roomSeller, quoteSeller string }{
		{"Hotels.com", "Hotels.com"},
		{"Google Hotels", "Google Hotels"},
		{"Google Hotels", "google  hotels"},
	} {
		got := completeRoomInventoryQuote(models.RoomInventoryQuote{Provider: tc.quoteSeller, NightlyPrice: 200, Currency: "EUR"},
			googlePricedRoom(tc.roomSeller), rateTermsNeed, now)
		if got.Refundable == nil || !*got.Refundable || got.Board != "breakfast_included" || got.ProviderURL != "https://www.hotels.com/ho1" {
			t.Errorf("%q vs %q: own quote not filled: %+v", tc.roomSeller, tc.quoteSeller, got)
		}
	}
}

// An offer built from another seller's cheaper quote shows that seller's link
// or none, never the room's or the hotel's (#823 review).
func TestAccommodationOffersFromRooms_ForeignQuoteKeepsItsOwnLink(t *testing.T) {
	room := googlePricedRoom("Hotels.com")
	room.InventoryOptions = []models.RoomInventoryQuote{
		{Provider: "Hotels.com", ProviderRoomName: "Double Room", ProviderURL: "https://www.hotels.com/ho1", NightlyPrice: 200, Currency: "EUR",
			MatchConfidence: models.RoomInventoryMatchExact, PriceBasis: models.PriceBasisRoomNightly, PriceConfidence: models.PriceConfidenceRoomLevel},
		{Provider: "Booking.com", ProviderRoomName: "Double Room", NightlyPrice: 95, Currency: "EUR",
			MatchConfidence: models.RoomInventoryMatchExact, PriceBasis: models.PriceBasisRoomNightly, PriceConfidence: models.PriceConfidenceRoomLevel},
	}
	hotel := models.HotelResult{Name: "Hotel X", HotelID: "/g/11x", BookingURL: "https://www.google.com/travel/hotels/entity/x"}
	offers := accommodationOffersFromRooms(hotel, []hotels.RoomType{room}, rateTermsNeed, time.Now())
	if len(offers) != 1 {
		t.Fatalf("offers = %d, want 1", len(offers))
	}
	o := offers[0]
	if o.Provider != "Booking.com" {
		t.Fatalf("selected seller = %q, want the cheaper Booking.com quote", o.Provider)
	}
	if o.ProviderURL != "" {
		t.Errorf("Booking's offer links to %q, another seller's page", o.ProviderURL)
	}
	if o.Refundable != nil || o.Board != "" {
		t.Errorf("Booking's offer shows Hotels.com's terms: refundable %v board %q", o.Refundable, o.Board)
	}
}

// A selected quote whose own room has no link gets none: the hotel's booking
// link can belong to another seller (#832 review).
func TestOfferAndVerifiedRate_NoHotelLinkFallback(t *testing.T) {
	room := googlePricedRoom("Hotels.com")
	room.ProviderURL = ""
	hotel := models.HotelResult{Name: "Hotel X", HotelID: "/g/11x", BookingURL: "https://www.booking.com/hotel/at/x.html"}
	now := time.Now()
	offers := accommodationOffersFromRooms(hotel, []hotels.RoomType{room}, rateTermsNeed, now)
	if len(offers) != 1 || offers[0].ProviderURL != "" {
		t.Errorf("offer link = %+v, want none: the hotel link is another seller's", offers)
	}
	if v := verifiedRateFromRooms(hotel, []hotels.RoomType{room}, now); v == nil || v.BookingURL != "" {
		t.Errorf("verified rate = %+v, want no booking link", v)
	}
}

// Seller names and room names normalise the same way.
func TestSellerKey_MatchesRoomNameNormalisation(t *testing.T) {
	if sellerKey("  Google   Hotels ") != normalizeName("google hotels") {
		t.Error("sellerKey and the room-name normaliser disagree")
	}
}
