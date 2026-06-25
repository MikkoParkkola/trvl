package hotels

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// agodaTestOffer is the room-level detail a test wants to inject into a property.
// Zero-value fields are left unset on the built room offer so a test only
// populates what it asserts on.
type agodaTestOffer struct {
	exclusive        float64
	inclusive        float64
	crossedOut       float64
	currency         string
	cancellationType string
}

// buildAgodaProperty constructs an agodaProperty carrying a single priced room
// offer, mirroring the real citySearch shape (one offer, one roomOffer). It
// hides the deeply-nested anonymous struct so tests stay readable and resilient
// to additive struct changes (e.g. the payment.cancellation field).
func buildAgodaProperty(id int, name string, o agodaTestOffer) agodaProperty {
	p := agodaProperty{PropertyID: id}
	p.Content.InformationSummary.DisplayName = name

	pr := agodaRoomPricing{Currency: o.currency}
	pr.Price.PerRoomPerNight.Exclusive.Display = o.exclusive
	pr.Price.PerRoomPerNight.Exclusive.CrossedOutPrice = o.crossedOut
	pr.Price.PerRoomPerNight.Inclusive.Display = o.inclusive

	offer := p.Pricing.Offers
	p.Pricing.Offers = append(offer, struct {
		RoomOffers []struct {
			Room struct {
				Pricing []agodaRoomPricing `json:"pricing"`
				Payment struct {
					Cancellation struct {
						CancellationType string `json:"cancellationType"`
					} `json:"cancellation"`
				} `json:"payment"`
			} `json:"room"`
		} `json:"roomOffers"`
	}{})

	ro := struct {
		Room struct {
			Pricing []agodaRoomPricing `json:"pricing"`
			Payment struct {
				Cancellation struct {
					CancellationType string `json:"cancellationType"`
				} `json:"cancellation"`
			} `json:"payment"`
		} `json:"room"`
	}{}
	ro.Room.Pricing = []agodaRoomPricing{pr}
	ro.Room.Payment.Cancellation.CancellationType = o.cancellationType
	p.Pricing.Offers[0].RoomOffers = append(p.Pricing.Offers[0].RoomOffers, ro)
	return p
}

// TestParseAgodaSearchEmitsRoomLevelInventory proves Agoda's per-room-per-night
// price is surfaced as room-level inventory (not just a property-level source),
// so it can reach the booking-ready gate. With only a pre-tax (exclusive) rate
// present, the offer stays room_nightly/room_level (no tax-inclusive upgrade).
func TestParseAgodaSearchEmitsRoomLevelInventory(t *testing.T) {
	resp := &agodaSearchResponse{}
	p := buildAgodaProperty(42, "Hotel Test", agodaTestOffer{exclusive: 120, currency: "EUR"})
	p.Content.InformationSummary.Rating = 4
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
	// No inclusive price → no tax-inclusive fields fabricated.
	if rt.TaxesFeesIncluded != nil {
		t.Errorf("TaxesFeesIncluded=%v, want nil when no inclusive price present", *rt.TaxesFeesIncluded)
	}
}

// TestParseAgodaSearchSurfacesTaxInclusiveTotal proves the tax-and-fees-inclusive
// per-room-per-night rate Agoda returns alongside the pre-tax rate (previously
// discarded) is surfaced as a verified all-in stay total, while the comparable
// headline stays the pre-tax rate. This upgrades Agoda to the top trust tier so
// the price-trust sort leads with its real all-in price.
func TestParseAgodaSearchSurfacesTaxInclusiveTotal(t *testing.T) {
	resp := &agodaSearchResponse{}
	// Real captured shape: exclusive 79.71, inclusive 99.26 (The Hoxton, Berlin).
	p := buildAgodaProperty(86662172, "The Hoxton, Berlin", agodaTestOffer{
		exclusive: 79.71,
		inclusive: 99.26,
		currency:  "EUR",
	})
	resp.Data.CitySearch.Properties = []agodaProperty{p}

	// 2-night stay.
	out := parseAgodaSearch(resp, HotelSearchOptions{CheckIn: "2026-07-10", CheckOut: "2026-07-12"})
	if len(out) != 1 || len(out[0].RoomTypes) != 1 {
		t.Fatalf("expected 1 hotel with 1 room, got %d hotels", len(out))
	}
	rt := out[0].RoomTypes[0]

	// Comparable headline preserved as the pre-tax rate.
	if rt.Price != 79.71 || rt.NightlyPrice != 79.71 {
		t.Errorf("Price=%v NightlyPrice=%v, want 79.71 (pre-tax comparable rate)", rt.Price, rt.NightlyPrice)
	}
	// Tax-inclusive stay total surfaced (99.26 x 2 nights).
	wantTotal := 99.26 * 2
	if rt.TotalPrice != wantTotal {
		t.Errorf("TotalPrice=%v, want %v (tax-inclusive 99.26 x 2)", rt.TotalPrice, wantTotal)
	}
	// Taxes/fees = inclusive total - pre-tax total.
	wantTaxes := wantTotal - 79.71*2
	if rt.TaxesAndFees < wantTaxes-0.001 || rt.TaxesAndFees > wantTaxes+0.001 {
		t.Errorf("TaxesAndFees=%v, want ~%v", rt.TaxesAndFees, wantTaxes)
	}
	if rt.TaxesFeesIncluded == nil || !*rt.TaxesFeesIncluded {
		t.Errorf("TaxesFeesIncluded=%v, want true", rt.TaxesFeesIncluded)
	}
	if rt.PriceBasis != models.PriceBasisTaxInclusiveTotal {
		t.Errorf("PriceBasis=%q, want tax_inclusive_total", rt.PriceBasis)
	}
	if rt.PriceConfidence != models.PriceConfidenceVerified {
		t.Errorf("PriceConfidence=%q, want verified", rt.PriceConfidence)
	}
}

// TestParseAgodaSearchSurfacesCancellation proves the real cancellation terms
// Agoda returns in payment.cancellation.cancellationType (previously discarded)
// are mapped onto the room's refundability fields.
func TestParseAgodaSearchSurfacesCancellation(t *testing.T) {
	resp := &agodaSearchResponse{}
	p := buildAgodaProperty(65086, "Maritim proArte Hotel Berlin", agodaTestOffer{
		exclusive:        96.25,
		inclusive:        107.81,
		currency:         "EUR",
		cancellationType: "NonRefundable",
	})
	resp.Data.CitySearch.Properties = []agodaProperty{p}

	out := parseAgodaSearch(resp, HotelSearchOptions{CheckIn: "2026-07-10", CheckOut: "2026-07-11"})
	if len(out) != 1 || len(out[0].RoomTypes) != 1 {
		t.Fatalf("expected 1 hotel with 1 room, got %d hotels", len(out))
	}
	rt := out[0].RoomTypes[0]
	if rt.Refundable == nil || *rt.Refundable {
		t.Errorf("Refundable=%v, want false (NonRefundable)", rt.Refundable)
	}
	if rt.FreeCancellation {
		t.Errorf("FreeCancellation=%v, want false", rt.FreeCancellation)
	}
	if rt.CancellationPolicy != "Non-refundable" {
		t.Errorf("CancellationPolicy=%q, want Non-refundable", rt.CancellationPolicy)
	}
}

// TestAgodaCancellationDetail pins the cancellation-type mapping, including the
// conservative default that leaves fields unset for unknown/empty values rather
// than guessing refundability.
func TestAgodaCancellationDetail(t *testing.T) {
	t.Run("nonrefundable", func(t *testing.T) {
		r, free, policy := agodaCancellationDetail("NonRefundable")
		if r == nil || *r || free || policy != "Non-refundable" {
			t.Errorf("got (%v,%v,%q), want (false,false,Non-refundable)", r, free, policy)
		}
	})
	t.Run("freecancellation", func(t *testing.T) {
		r, free, policy := agodaCancellationDetail("FreeCancellation")
		if r == nil || !*r || !free || policy != "Free cancellation" {
			t.Errorf("got (%v,%v,%q), want (true,true,Free cancellation)", r, free, policy)
		}
	})
	t.Run("unknown_left_unset", func(t *testing.T) {
		r, free, policy := agodaCancellationDetail("SomethingElse")
		if r != nil || free || policy != "" {
			t.Errorf("got (%v,%v,%q), want (nil,false,\"\") for unknown type", r, free, policy)
		}
	})
	t.Run("empty_left_unset", func(t *testing.T) {
		r, free, policy := agodaCancellationDetail("")
		if r != nil || free || policy != "" {
			t.Errorf("got (%v,%v,%q), want (nil,false,\"\") for empty type", r, free, policy)
		}
	})
}

// TestParseAgodaSearchDerivesStayTotal proves a multi-night search turns Agoda's
// per-night rate into a stay total, so its offers compare and display alongside
// providers that return a room total. The nightly rate is preserved.
func TestParseAgodaSearchDerivesStayTotal(t *testing.T) {
	resp := &agodaSearchResponse{}
	p := buildAgodaProperty(7, "Hotel Nights", agodaTestOffer{exclusive: 120, currency: "EUR"})
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
