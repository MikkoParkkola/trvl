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

// TestSortHotelsPricedFirstAcrossSortModes asserts the operator rule "lead with
// the ones that have proper price available" holds for every sort mode, not just
// "cheapest". A listing with no bookable price must never lead a priced one, even
// when it scores higher on the chosen key (rating/stars/distance). Within each
// price-presence group the chosen key still orders the results.
func TestSortHotelsPricedFirstAcrossSortModes(t *testing.T) {
	cases := []struct {
		sort   string
		hotels []models.HotelResult
		// wantLead is the name expected first: a priced listing, even though the
		// unpriced one wins the raw key.
		wantLead     string
		wantUnpriced string // expected last
	}{
		{
			sort: "rating",
			hotels: []models.HotelResult{
				{Name: "Unpriced top-rated", Price: 0, Rating: 9.8},
				{Name: "Priced decent", Price: 120, Rating: 8.1, PriceConfidence: models.PriceConfidenceRoomLevel},
			},
			wantLead: "Priced decent", wantUnpriced: "Unpriced top-rated",
		},
		{
			sort: "stars",
			hotels: []models.HotelResult{
				{Name: "Unpriced 5star", Price: 0, Stars: 5},
				{Name: "Priced 3star", Price: 90, Stars: 3, PriceConfidence: models.PriceConfidenceRoomLevel},
			},
			wantLead: "Priced 3star", wantUnpriced: "Unpriced 5star",
		},
		{
			sort: "distance",
			hotels: []models.HotelResult{
				{Name: "Unpriced near", Price: 0, Lat: 60.18, Lon: 24.94},
				{Name: "Priced far", Price: 100, Lat: 61.0, Lon: 25.0, PriceConfidence: models.PriceConfidenceRoomLevel},
			},
			wantLead: "Priced far", wantUnpriced: "Unpriced near",
		},
	}
	for _, c := range cases {
		t.Run(c.sort, func(t *testing.T) {
			sortHotels(c.hotels, c.sort, 60.17, 24.93)
			if c.hotels[0].Name != c.wantLead {
				t.Errorf("%s sort: lead = %q, want priced %q", c.sort, c.hotels[0].Name, c.wantLead)
			}
			if c.hotels[len(c.hotels)-1].Name != c.wantUnpriced {
				t.Errorf("%s sort: last = %q, want unpriced %q", c.sort, c.hotels[len(c.hotels)-1].Name, c.wantUnpriced)
			}
		})
	}
}

// TestSortHotelsKeySortWithinPricedGroup confirms the chosen key still orders
// listings that all have prices (the priced-first rule must not flatten the key).
func TestSortHotelsKeySortWithinPricedGroup(t *testing.T) {
	hotels := []models.HotelResult{
		{Name: "Low rating", Price: 100, Rating: 7.0, PriceConfidence: models.PriceConfidenceRoomLevel},
		{Name: "High rating", Price: 100, Rating: 9.0, PriceConfidence: models.PriceConfidenceRoomLevel},
	}
	sortHotels(hotels, "rating", 0, 0)
	if hotels[0].Name != "High rating" {
		t.Errorf("rating sort within priced group: lead = %q, want High rating", hotels[0].Name)
	}
}

// TestSortHotelsKeyBeatsPriceWithinPricedGroup is the regression guard for the
// pricedLead over-reach: when two listings are BOTH priced, the chosen non-price
// key (rating/stars/distance) must decide their order, even when price order
// points the other way. The earlier same-price test passed by luck (name-lex
// tiebreak aligned with rating); here the cheaper hotel is the lower-rated one,
// so a pricedLead that leaks price ordering into the rating sort is caught.
func TestSortHotelsKeyBeatsPriceWithinPricedGroup(t *testing.T) {
	hotels := []models.HotelResult{
		{Name: "Cheap low-rating", Price: 80, Rating: 7.0, PriceConfidence: models.PriceConfidenceRoomLevel},
		{Name: "Pricey high-rating", Price: 120, Rating: 9.0, PriceConfidence: models.PriceConfidenceRoomLevel},
	}
	sortHotels(hotels, "rating", 0, 0)
	if hotels[0].Name != "Pricey high-rating" {
		t.Errorf("rating sort must beat price within priced group: lead = %q, want Pricey high-rating (price must not decide)", hotels[0].Name)
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
