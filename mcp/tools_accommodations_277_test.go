package mcp

import (
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestNormalizeOfferCurrency covers issue #277 defect 1: a provider offer in a
// foreign currency (e.g. HousingAnywhere returning USD when EUR was requested)
// must be FX-normalized to the requested currency before criteria evaluation
// rather than rejected for missing_criteria:["currency"].
func TestNormalizeOfferCurrency(t *testing.T) {
	// Same currency: untouched.
	in := models.AccommodationOffer{Currency: "EUR", NightlyPrice: 100, TotalPrice: 200}
	if out := normalizeOfferCurrency(in, "EUR"); out.Currency != "EUR" || out.NightlyPrice != 100 || out.TotalPrice != 200 {
		t.Fatalf("same currency must be untouched: %+v", out)
	}
	// Empty target: untouched.
	if out := normalizeOfferCurrency(in, ""); out.Currency != "EUR" || out.NightlyPrice != 100 {
		t.Fatalf("empty want must be untouched: %+v", out)
	}
	// USD -> EUR: converted, relabeled, warned. USD/EUR always resolves via
	// fx.go fallback rates, so this is deterministic offline.
	usd := models.AccommodationOffer{Currency: "USD", NightlyPrice: 109, TotalPrice: 218, TaxesAndFees: 20}
	out := normalizeOfferCurrency(usd, "EUR")
	if out.Currency != "EUR" {
		t.Fatalf("currency should be relabeled to EUR, got %q", out.Currency)
	}
	if out.NightlyPrice <= 0 || out.NightlyPrice >= 109 {
		t.Fatalf("USD->EUR nightly should be positive and below the USD nominal, got %v", out.NightlyPrice)
	}
	if out.TotalPrice <= 0 || out.TotalPrice >= 218 {
		t.Fatalf("USD->EUR total should be positive and below the USD nominal, got %v", out.TotalPrice)
	}
	found := false
	for _, w := range out.Warnings {
		if w == "currency_normalized" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected currency_normalized warning, got %v", out.Warnings)
	}
}

// TestSelectAccommodationCandidateHotelsMinStars covers issue #277 defect 3:
// min_stars must be enforced during stage-2 candidate selection. Properties
// with a known star rating below the minimum are dropped; unrated (stars==0)
// properties are kept because we cannot prove they fail the filter.
func TestSelectAccommodationCandidateHotelsMinStars(t *testing.T) {
	hotels := []models.HotelResult{
		{Name: "Four", Stars: 4},
		{Name: "Five", Stars: 5},
		{Name: "Unrated", Stars: 0},
	}
	got := selectAccommodationCandidateHotels(hotels, 10, models.AccommodationNeed{MinStars: 5})
	names := map[string]bool{}
	for _, h := range got {
		names[h.Name] = true
	}
	if names["Four"] {
		t.Fatalf("4-star property must be excluded for min_stars=5, got %v", names)
	}
	if !names["Five"] {
		t.Fatalf("5-star property must be kept for min_stars=5, got %v", names)
	}
	if !names["Unrated"] {
		t.Fatalf("unrated property must be kept (cannot prove failure), got %v", names)
	}
	// No min_stars: every candidate is kept.
	if all := selectAccommodationCandidateHotels(hotels, 10, models.AccommodationNeed{}); len(all) != 3 {
		t.Fatalf("no min_stars filter should keep all 3, got %d", len(all))
	}
}

// TestDefect2RoomLevelOfferIsBookingReady covers issue #277 defect 2: when the
// Booking.com room path yields an exact-match, room-level, tax-inclusive price,
// the offer MUST surface as booking_ready. This is the success criterion the
// timeout fix unblocks — if the fetch completes, the wiring already produces a
// bookable verdict. A lead-in property-level price MUST NOT (the honesty
// invariant: never fake booking_ready on a property-level figure).
func TestDefect2RoomLevelOfferIsBookingReady(t *testing.T) {
	checkedAt := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	hotel := models.HotelResult{Name: "Hotel Central", HotelID: "h1", BookingURL: "https://www.booking.com/hotel/fr/central.html"}
	need := models.AccommodationNeed{Adults: 2, Rooms: 1, Currency: "EUR"}
	taxIncluded := true

	// Booking.com exact-match room: room-level nightly + tax-inclusive total.
	bookingRoom := hotels.RoomType{
		Name:              "Deluxe Double",
		NightlyPrice:      120,
		TotalPrice:        240,
		TaxesFeesIncluded: &taxIncluded,
		Currency:          "EUR",
		Provider:          "Booking.com",
		MatchConfidence:   models.RoomInventoryMatchExact,
		MaxGuests:         2,
	}
	offers := accommodationOffersFromRooms(hotel, []hotels.RoomType{bookingRoom}, need, checkedAt)
	if len(offers) != 1 {
		t.Fatalf("expected 1 offer from booking room, got %d", len(offers))
	}
	got := offers[0]
	if !got.BookingReadyStatus {
		t.Fatalf("exact-match room-level offer must be booking_ready: matched=%v occ=%v conf=%q missing=%v",
			got.CriteriaMatched, got.OccupancyMatched, got.PriceConfidence, got.MissingCriteria)
	}
	if got.InventoryCompleteness == models.RoomInventoryCompletenessPropertyLevelOnly {
		t.Fatalf("room-level offer must not be property_level_only, got %q", got.InventoryCompleteness)
	}

	// Honesty invariant: a property-level lead-in price is NOT booking_ready.
	leadInRoom := hotels.RoomType{
		Name:            "Standard Room",
		Price:           99,
		Currency:        "EUR",
		Provider:        "Google",
		MatchConfidence: models.RoomInventoryMatchPropertyLevelOnly,
	}
	leadOffers := accommodationOffersFromRooms(hotel, []hotels.RoomType{leadInRoom}, need, checkedAt)
	if len(leadOffers) != 1 {
		t.Fatalf("expected 1 lead-in offer, got %d", len(leadOffers))
	}
	if leadOffers[0].BookingReadyStatus {
		t.Fatalf("property-level lead-in must never be booking_ready: %+v", leadOffers[0])
	}
}

// TestDefect2BookingRoomSurvivesMergeWithSearchLeadIn proves the Booking.com
// exact-match room is not dropped when merged with a property-level search
// lead-in for the same hotel (rooms.go merge + mergeDetailAndSearchInventoryRooms),
// so the booking_ready offer reaches the result set.
func TestDefect2BookingRoomSurvivesMergeWithSearchLeadIn(t *testing.T) {
	checkedAt := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	hotel := models.HotelResult{Name: "Hotel Central", HotelID: "h1"}
	need := models.AccommodationNeed{Adults: 2, Rooms: 1, Currency: "EUR"}
	taxIncluded := true

	searchLeadIn := hotels.RoomType{Name: "Standard Room", Price: 99, Currency: "EUR", Provider: "Google", MatchConfidence: models.RoomInventoryMatchPropertyLevelOnly}
	bookingRoom := hotels.RoomType{Name: "Deluxe Double", NightlyPrice: 120, TotalPrice: 240, TaxesFeesIncluded: &taxIncluded, Currency: "EUR", Provider: "Booking.com", MatchConfidence: models.RoomInventoryMatchExact, MaxGuests: 2}

	merged := mergeDetailAndSearchInventoryRooms([]hotels.RoomType{bookingRoom}, []hotels.RoomType{searchLeadIn})
	offers := accommodationOffersFromRooms(hotel, merged, need, checkedAt)

	bookingReady := false
	for _, o := range offers {
		if o.BookingReadyStatus {
			bookingReady = true
		}
	}
	if !bookingReady {
		t.Fatalf("merged rooms must still yield a booking_ready offer, got %d offers none ready", len(offers))
	}
}

// TestAccommodationRoomLookupBookingURLRecoversFromSources covers issue #277
// defect 2: selectPrimaryHotelSource overwrites HotelResult.BookingURL with the
// cheapest source (e.g. Agoda), which fails the booking.com/ gate. When a
// Booking.com source still exists among hotel.Sources, the room lookup must
// recover its URL so the only exact-room-match provider can run.
func TestAccommodationRoomLookupBookingURLRecoversFromSources(t *testing.T) {
	agoda := "https://www.agoda.com/hotel/fr/central.html"
	booking := "https://www.booking.com/hotel/fr/central.html"

	// Primary is Agoda (cheapest source clobbered it) but a Booking source exists.
	hotel := models.HotelResult{
		BookingURL: agoda,
		Sources: []models.PriceSource{
			{Provider: "agoda", BookingURL: agoda},
			{Provider: "booking", BookingURL: booking},
		},
	}
	if got := accommodationRoomLookupBookingURL(hotel); got != booking {
		t.Fatalf("must recover booking.com source URL for room lookup, got %q", got)
	}

	// No Booking source: leave the primary URL untouched.
	noBooking := models.HotelResult{BookingURL: agoda, Sources: []models.PriceSource{{Provider: "agoda", BookingURL: agoda}}}
	if got := accommodationRoomLookupBookingURL(noBooking); got != agoda {
		t.Fatalf("must preserve primary URL when no booking.com source, got %q", got)
	}

	// Primary already booking.com: returned as-is without scanning sources.
	primaryBooking := models.HotelResult{BookingURL: booking}
	if got := accommodationRoomLookupBookingURL(primaryBooking); got != booking {
		t.Fatalf("booking.com primary must be returned unchanged, got %q", got)
	}
}

// TestSelectAccommodationCandidateHotelsRanksBookingBacked covers issue #277
// defect 2 L3: a hotel whose Booking.com source is hidden behind a cheaper
// primary URL must still rank into the candidate pool, otherwise the room
// lookup never runs on it. Before the fix it scored +15 (any URL) instead of
// +60 (booking.com URL) and lost to non-bookable properties.
func TestSelectAccommodationCandidateHotelsRanksBookingBacked(t *testing.T) {
	agoda := "https://www.agoda.com/hotel/fr/central.html"
	booking := "https://www.booking.com/hotel/fr/central.html"

	bookingBacked := models.HotelResult{
		Name:       "Booking Backed",
		BookingURL: agoda, // primary clobbered to cheapest source
		Sources: []models.PriceSource{
			{Provider: "agoda", BookingURL: agoda},
			{Provider: "booking", BookingURL: booking},
		},
	}
	nonBookable := models.HotelResult{Name: "No Booking Source", BookingURL: agoda}

	// nonBookable listed first to prove ranking, not input order, decides.
	got := selectAccommodationCandidateHotels([]models.HotelResult{nonBookable, bookingBacked}, 1, models.AccommodationNeed{})
	if len(got) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(got))
	}
	if got[0].Name != "Booking Backed" {
		t.Fatalf("booking-backed hotel must rank into the top candidate, got %q", got[0].Name)
	}
}
