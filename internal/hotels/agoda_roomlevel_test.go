package hotels

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestParseAgodaSearchEmitsRoomLevelInventory proves Agoda's per-room-per-night
// price is surfaced as room-level inventory (not just a property-level source),
// so it can reach the booking-ready gate. Tagged similar (real room rate, no
// nameable room identity) with room_nightly/room_level trust.
func TestParseAgodaSearchEmitsRoomLevelInventory(t *testing.T) {
	resp := &agodaSearchResponse{}
	p := agodaProperty{PropertyID: 42}
	p.Content.InformationSummary.DisplayName = "Hotel Test"
	p.Content.InformationSummary.Rating = 4
	// One room offer with a per-room-per-night exclusive display price.
	offer := struct {
		RoomOffers []struct {
			Room struct {
				Pricing []agodaRoomPricing `json:"pricing"`
			} `json:"room"`
		} `json:"roomOffers"`
	}{}
	ro := struct {
		Room struct {
			Pricing []agodaRoomPricing `json:"pricing"`
		} `json:"room"`
	}{}
	pr := agodaRoomPricing{Currency: "EUR"}
	pr.Price.PerRoomPerNight.Exclusive.Display = 120
	ro.Room.Pricing = []agodaRoomPricing{pr}
	offer.RoomOffers = append(offer.RoomOffers, ro)
	p.Pricing.Offers = append(p.Pricing.Offers, offer)
	resp.Data.CitySearch.Properties = []agodaProperty{p}

	out := parseAgodaSearch(resp, HotelSearchOptions{})
	if len(out) != 1 {
		t.Fatalf("expected 1 hotel, got %d", len(out))
	}
	h := out[0]
	if len(h.RoomTypes) != 1 {
		t.Fatalf("expected 1 room-level inventory entry, got %d", len(h.RoomTypes))
	}
	rt := h.RoomTypes[0]
	if rt.Price != 120 || rt.Currency != "EUR" {
		t.Errorf("room price=%v currency=%q, want 120 EUR", rt.Price, rt.Currency)
	}
	if rt.PriceConfidence != models.PriceConfidenceRoomLevel {
		t.Errorf("PriceConfidence=%q, want room_level", rt.PriceConfidence)
	}
	if rt.PriceBasis != models.PriceBasisRoomNightly {
		t.Errorf("PriceBasis=%q, want room_nightly", rt.PriceBasis)
	}
	if rt.MatchConfidence != models.RoomInventoryMatchSimilar {
		t.Errorf("MatchConfidence=%q, want similar_room_match", rt.MatchConfidence)
	}
}

// TestParseAgodaSearchDerivesStayTotal proves a multi-night search turns Agoda's
// per-night rate into a stay total, so its offers compare and display alongside
// providers that return a room total. The nightly rate is preserved.
func TestParseAgodaSearchDerivesStayTotal(t *testing.T) {
	resp := &agodaSearchResponse{}
	p := agodaProperty{PropertyID: 7}
	p.Content.InformationSummary.DisplayName = "Hotel Nights"
	offer := struct {
		RoomOffers []struct {
			Room struct {
				Pricing []agodaRoomPricing `json:"pricing"`
			} `json:"room"`
		} `json:"roomOffers"`
	}{}
	ro := struct {
		Room struct {
			Pricing []agodaRoomPricing `json:"pricing"`
		} `json:"room"`
	}{}
	pr := agodaRoomPricing{Currency: "EUR"}
	pr.Price.PerRoomPerNight.Exclusive.Display = 120
	ro.Room.Pricing = []agodaRoomPricing{pr}
	offer.RoomOffers = append(offer.RoomOffers, ro)
	p.Pricing.Offers = append(p.Pricing.Offers, offer)
	resp.Data.CitySearch.Properties = []agodaProperty{p}

	// 3-night stay → total = 120 * 3.
	out := parseAgodaSearch(resp, HotelSearchOptions{CheckIn: "2026-07-10", CheckOut: "2026-07-13"})
	if len(out) != 1 || len(out[0].RoomTypes) != 1 {
		t.Fatalf("expected 1 hotel with 1 room, got %d hotels", len(out))
	}
	rt := out[0].RoomTypes[0]
	if rt.NightlyPrice != 120 {
		t.Errorf("NightlyPrice=%v, want 120 (per-night rate preserved)", rt.NightlyPrice)
	}
	if rt.TotalPrice != 360 {
		t.Errorf("TotalPrice=%v, want 360 (120 x 3 nights)", rt.TotalPrice)
	}
}

// TestAgodaStayTotal pins the derivation helper, including the guards that keep
// it from publishing a misleading zero or negative total.
func TestAgodaStayTotal(t *testing.T) {
	cases := []struct {
		nightly float64
		nights  int
		want    float64
	}{
		{120, 3, 360},
		{99.5, 2, 199},
		{120, 1, 120},
		{0, 3, 0},   // no rate → no total
		{120, 0, 0}, // no nights → no total
		{-50, 2, 0}, // negative rate guarded
	}
	for _, tc := range cases {
		if got := agodaStayTotal(tc.nightly, tc.nights); got != tc.want {
			t.Errorf("agodaStayTotal(%v, %d) = %v, want %v", tc.nightly, tc.nights, got, tc.want)
		}
	}
}
