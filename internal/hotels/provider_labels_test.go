package hotels

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/serpapi"
)

// TestBestRoomSource_CreditsFetcherKeepsSeller is the #808 fail-fast: a room
// whose Provider is a seller name must not put that name in sources[].provider.
func TestBestRoomSource_CreditsFetcherKeepsSeller(t *testing.T) {
	src, ok := bestRoomSource([]models.Room{{
		Name:            "Standard Room",
		NightlyPrice:    44,
		Currency:        "EUR",
		Provider:        "Hotels.com",
		Source:          "google_hotels",
		ProviderURL:     "https://www.hotels.com/ho123",
		PriceBasis:      models.PriceBasisRoomNightly,
		PriceConfidence: models.PriceConfidenceRoomLevel,
	}})
	if !ok {
		t.Fatal("bestRoomSource found no priced room")
	}
	if src.Provider != "google_hotels" {
		t.Errorf("Provider = %q, want google_hotels (the trvl provider that fetched it)", src.Provider)
	}
	if src.Seller != "Hotels.com" {
		t.Errorf("Seller = %q, want Hotels.com", src.Seller)
	}
	if src.BookingURL != "https://www.hotels.com/ho123" {
		t.Errorf("BookingURL = %q, want the seller's link", src.BookingURL)
	}
}

// An untagged room never gets a guessed provider id or a seller name.
func TestBestRoomSource_UntaggedRoomIsUnknown(t *testing.T) {
	src, _ := bestRoomSource([]models.Room{{Name: "Room", Price: 50, Currency: "EUR", Provider: "Hotels.com"}})
	if src.Provider != "unknown" {
		t.Errorf("Provider = %q, want unknown", src.Provider)
	}
	if src.Seller != "Hotels.com" {
		t.Errorf("Seller = %q, want Hotels.com", src.Seller)
	}
}

// When the seller is the fetcher itself, there is no separate seller, also
// when the seller is the fetcher's display name.
func TestBestRoomSource_NoSellerWhenFetcherSells(t *testing.T) {
	for _, tc := range []struct{ seller, source string }{
		{"agoda", "agoda"},
		{"Google Hotels", "google_hotels"},
		{"Booking.com", "booking.com"},
	} {
		src, _ := bestRoomSource([]models.Room{{Name: "Room", Price: 50, Currency: "EUR", Provider: tc.seller, Source: tc.source}})
		if src.Provider != tc.source || src.Seller != "" {
			t.Errorf("seller %q: got provider %q seller %q, want %s and no seller", tc.seller, src.Provider, src.Seller, tc.source)
		}
	}
}

// TestEnrichHotelRooms_HeadlineNamesFetcher replays the MEININGER case from
// #808 through enrichment, the trust rule and confidence scoring.
func TestEnrichHotelRooms_HeadlineNamesFetcher(t *testing.T) {
	orig := fetchRoomAvailability
	t.Cleanup(func() { fetchRoomAvailability = orig })
	origRM := HotelRateManager
	t.Cleanup(func() { HotelRateManager = origRM })
	HotelRateManager = NewRateManager()

	const seller = "MEININGER Hotel Wien Downtown Franz"
	fetchRoomAvailability = func(context.Context, RoomSearchOptions) (*RoomAvailability, error) {
		return &RoomAvailability{Success: true, Rooms: []RoomType{{
			Name:            "Standard Room",
			Price:           45,
			NightlyPrice:    45,
			Currency:        "EUR",
			Provider:        seller,
			Source:          "google_hotels",
			ProviderURL:     "https://www.meininger-hotels.com/en/hotels/vienna/",
			MatchConfidence: models.RoomInventoryMatchSimilar,
		}}}, nil
	}

	hotels := []models.HotelResult{{
		HotelID:  "/g/11meininger",
		Name:     seller,
		Price:    40,
		Currency: "EUR",
		Sources:  []models.PriceSource{{Provider: "google_hotels", Price: 40, Currency: "EUR"}},
	}}
	now := time.Now()
	hotels = enrichHotelRooms(context.Background(), hotels, HotelSearchOptions{
		EnrichRooms: true, CheckIn: "2026-11-18", CheckOut: "2026-11-20", Currency: "EUR",
	})
	models.FinalizeHotelPriceTrust(hotels, "EUR", now, nil)
	annotateHotelConfidence(hotels, now)

	h := hotels[0]
	if h.HeadlineProvider != "google_hotels" {
		t.Errorf("headline_provider = %q, want google_hotels", h.HeadlineProvider)
	}
	if h.Price != 45 {
		t.Errorf("headline price = %v, want the 45 EUR room rate", h.Price)
	}
	sellerKept := false
	for _, s := range h.Sources {
		if s.Provider != "google_hotels" {
			t.Errorf("sources[].provider = %q, want only trvl tags (google_hotels)", s.Provider)
		}
		sellerKept = sellerKept || s.Seller == seller
	}
	if !sellerKept {
		t.Errorf("no source keeps seller %q: %+v", seller, h.Sources)
	}
	if h.Confidence == nil {
		t.Fatal("confidence not scored")
	}
	for _, want := range []string{"provider:scrape", "sources:1"} {
		if !slices.Contains(h.Confidence.Signals, want) {
			t.Errorf("confidence signals %v lack %q", h.Confidence.Signals, want)
		}
	}
}

// TestMergeRoomTypes_OneSellerPerRoom: a priced Google room keeps every price
// field of its own quote; Booking's numbers (and its taxes-included flag) stay
// in Booking's inventory quote instead of being spliced in.
func TestMergeRoomTypes_OneSellerPerRoom(t *testing.T) {
	yes := true
	google := []RoomType{{
		Name: "Double Room", Price: 100, Currency: "EUR",
		Provider: "Hotels.com", Source: "google_hotels", ProviderURL: "https://www.hotels.com/ho1",
	}}
	booking := []RoomType{{
		Name: "Double Room", Price: 95, NightlyPrice: 95, TotalPrice: 190, TaxesAndFees: 12, TaxesFeesIncluded: &yes,
		Currency: "EUR", Provider: "Booking.com", Source: "booking.com", ProviderURL: "https://www.booking.com/hotel/at/x.html",
		Description: "Quiet courtyard room",
	}}
	merged := mergeRoomTypes(google, booking)
	if len(merged) != 1 {
		t.Fatalf("merged %d rooms, want 1", len(merged))
	}
	m := merged[0]
	if m.Price != 100 || m.NightlyPrice != 0 || m.TotalPrice != 0 || m.TaxesAndFees != 0 || m.TaxesFeesIncluded != nil {
		t.Errorf("price fields = price %v nightly %v total %v taxes %v included %v, want Google's quote only",
			m.Price, m.NightlyPrice, m.TotalPrice, m.TaxesAndFees, m.TaxesFeesIncluded)
	}
	if m.Provider != "Hotels.com" || m.Source != "google_hotels" || m.ProviderURL != "https://www.hotels.com/ho1" {
		t.Errorf("seller = %q via %q (%s), want Hotels.com via google_hotels", m.Provider, m.ProviderURL, m.Source)
	}
	if m.Description != "Quiet courtyard room" {
		t.Errorf("Description = %q: non-price enrichment must still merge", m.Description)
	}
	if len(m.InventoryOptions) != 2 || m.InventoryOptions[1].TotalPrice != 190 {
		t.Errorf("InventoryOptions = %+v, want Booking's 190 quote kept", m.InventoryOptions)
	}
	src, _ := bestRoomSource([]models.Room{roomTypeToModelRoom(m)})
	if src.Price != 100 || src.Provider != "google_hotels" || src.Seller != "Hotels.com" {
		t.Errorf("published %v by %q (seller %q), want 100 by google_hotels (Hotels.com)", src.Price, src.Provider, src.Seller)
	}
	if src.PriceConfidence == models.PriceConfidenceVerified {
		t.Error("Booking's taxes-included flag verified Google's price")
	}
}

// An unpriced Google room takes Booking's whole quote, seller and link included.
func TestMergeRoomTypes_UnpricedGoogleTakesBookingQuote(t *testing.T) {
	google := []RoomType{{Name: "Double Room", Currency: "EUR", Provider: "Hotels.com", Source: "google_hotels"}}
	booking := []RoomType{{
		Name: "Double Room", Price: 95, NightlyPrice: 95, TotalPrice: 190, Currency: "EUR",
		Provider: "Booking.com", Source: "booking.com", ProviderURL: "https://www.booking.com/hotel/at/x.html",
	}}
	m := mergeRoomTypes(google, booking)[0]
	if m.Price != 95 || m.NightlyPrice != 95 || m.TotalPrice != 190 || m.Provider != "Booking.com" || m.Source != "booking.com" || m.ProviderURL != "https://www.booking.com/hotel/at/x.html" {
		t.Errorf("got %+v, want Booking's whole quote", m)
	}
}

// TestGetRoomAvailability_WatchSeesOneSellerPerRoom runs the room fetch the
// room watch uses (cmd/trvl/watch.go and mcp/watch_room_checker.go report each
// room's Price under its Provider) with a Google room and a same-named Booking
// room. The watch must see one seller's price under that seller's name.
func TestGetRoomAvailability_WatchSeesOneSellerPerRoom(t *testing.T) {
	t.Setenv("GOOGLE_HOTELS_DETAIL_API_BASE", "")
	origKey := serpapiAPIKeyFunc
	serpapiAPIKeyFunc = func() string { return "" }
	t.Cleanup(func() { serpapiAPIKeyFunc = origKey })

	// The Google entity page answers locally with two partner rooms.
	page := `<html><head><title>Hotel X</title></head><body>` + strings.Repeat("<!-- pad -->", 50) +
		`<script>AF_initDataCallback({key: 'ds:1', data:[[["Deluxe Suite",120,"EUR","Hotels.com"],["Standard Room",90,"EUR","Expedia"]]]});</script></body></html>`
	useTestDefaultClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(page))
	}))

	yes := true
	origFetch := FetchBookingRooms
	FetchBookingRooms = func(context.Context, string, string, string, string) ([]RoomType, error) {
		return []RoomType{{
			Name: "Deluxe Suite", Price: 95, NightlyPrice: 95, TotalPrice: 190, TaxesFeesIncluded: &yes,
			Currency: "EUR", Provider: "Booking.com", Source: "booking.com",
			ProviderURL: "https://www.booking.com/hotel/at/x.html",
		}}, nil
	}
	t.Cleanup(func() { FetchBookingRooms = origFetch })

	av, err := GetRoomAvailabilityWithOpts(context.Background(), RoomSearchOptions{
		HotelID: "/g/11hotelx", CheckIn: "2026-11-18", CheckOut: "2026-11-20", Currency: "EUR",
		BookingURL: "https://www.booking.com/hotel/at/x.html",
	})
	if err != nil {
		t.Fatalf("GetRoomAvailabilityWithOpts: %v", err)
	}
	var suite *RoomType
	for i := range av.Rooms {
		if av.Rooms[i].Name == "Deluxe Suite" {
			suite = &av.Rooms[i]
		}
	}
	if suite == nil {
		t.Fatalf("no Deluxe Suite in %+v", av.Rooms)
	}
	// What the watch reports: Price under Provider.
	if suite.Price != 120 || suite.Provider != "Hotels.com" {
		t.Errorf("watch would report %v under %q, want Hotels.com's 120", suite.Price, suite.Provider)
	}
	// What the headline would publish from the same room.
	src, _ := bestRoomSource([]models.Room{roomTypeToModelRoom(*suite)})
	if src.Price != 120 || src.Provider != "google_hotels" || src.Seller != "Hotels.com" {
		t.Errorf("headline would publish %v by %q (seller %q), want 120 by google_hotels (Hotels.com)", src.Price, src.Provider, src.Seller)
	}
}

// When Google already has the nightly rate, the room stays Google's.
func TestMergeRoomTypes_GoogleNightlyKeepsGoogle(t *testing.T) {
	google := []RoomType{{Name: "Double Room", Price: 100, NightlyPrice: 100, Currency: "EUR", Provider: "Hotels.com", Source: "google_hotels"}}
	booking := []RoomType{{Name: "Double Room", Price: 95, NightlyPrice: 95, Currency: "EUR", Provider: "Booking.com", Source: "booking.com"}}
	merged := mergeRoomTypes(google, booking)
	if merged[0].Source != "google_hotels" || merged[0].Provider != "Hotels.com" {
		t.Errorf("got source %q seller %q, want google_hotels and Hotels.com", merged[0].Source, merged[0].Provider)
	}
}

// Every room converter stamps the trvl provider that fetched the room.
func TestRoomConvertersTagSource(t *testing.T) {
	check := func(name, want string, rooms []RoomType) {
		t.Helper()
		if len(rooms) == 0 {
			t.Fatalf("%s: no rooms", name)
		}
		for _, r := range rooms {
			if r.Source != want {
				t.Errorf("%s: room %q (seller %q) Source = %q, want %q", name, r.Name, r.Provider, r.Source, want)
			}
		}
	}

	detail, _ := parseGoogleRoomDetail(readGoogleRoomDetailFixture(t), "EUR")
	check("google room detail", "google_hotels", detail)

	page := `AF_initDataCallback({key: 'ds:1', data:[[["Deluxe Suite",299,"EUR","Booking.com"],["Standard Room",149,"EUR","Expedia"]]]});`
	entity, _ := parseRoomsFromPage(page, "EUR")
	check("google entity page", "google_hotels", entity)

	check("booking rooms", "booking.com", []RoomType{roomTypeFromBookingOffer(bookingRoomOffer{Name: "Deluxe", Price: 120, Currency: "EUR"}, "https://www.booking.com/hotel/at/x.html", "EUR")})

	check("serpapi fallback", "serpapi", roomTypesFromSerpAPIHotel(&serpapi.Hotel{Name: "Hotel X", TotalRate: serpapi.Rate{Extracted: 300}}, "EUR"))
	check("serpapi offer", "serpapi", roomTypesFromSerpAPIHotel(&serpapi.Hotel{Name: "Hotel X", Prices: []serpapi.PriceOption{
		{Source: "Expedia", RatePerNight: serpapi.Rate{Extracted: 120}, TotalRate: serpapi.Rate{Extracted: 240}},
	}}, "EUR"))

	agoda := parseAgodaSearch(loadAgodaSearchFixture(t), HotelSearchOptions{})
	if len(agoda) == 0 || len(agoda[0].RoomTypes) == 0 {
		t.Fatal("agoda fixture has no rooms")
	}
	check("agoda", "agoda", hotelRoomsToRoomTypes(agoda[0].RoomTypes, "EUR"))

	sp := searchPageRooms(&models.HotelResult{
		Price: 140, Currency: "EUR", HeadlineProvider: "trivago",
		// The headline quote is not Sources[0]: seller and tag must still agree.
		Sources: []models.PriceSource{
			{Provider: "booking.com", Price: 150, Currency: "EUR"},
			{Provider: "trivago", Price: 140, Currency: "EUR"},
		},
	}, "EUR")
	if len(sp) != 2 || sp[0].Source != "trivago" || sp[1].Source != "booking.com" {
		t.Errorf("search page rooms: got %+v, want sources trivago (headline) and booking.com", sp)
	}
	// The headline room names the same quote it is tagged with, so no seller
	// other than the fetcher appears.
	if src, _ := bestRoomSource([]models.Room{roomTypeToModelRoom(sp[0])}); src.Provider != "trivago" || src.Seller != "" {
		t.Errorf("search page headline: provider %q seller %q, want trivago and no seller", src.Provider, src.Seller)
	}
}

// TestEnrichHotelRooms_VerifiedQuoteWinsUnderItsSeller: a merged room keeps one
// seller's numbers, so a second seller's verified quote lives in
// InventoryOptions. It must still compete for the headline as its own
// candidate, credited to its own seller and fetcher, and must count as a
// verified room so later paid fallbacks are skipped (#817 review).
func TestEnrichHotelRooms_VerifiedQuoteWinsUnderItsSeller(t *testing.T) {
	yes := true
	merged := mergeRoomTypes(
		[]RoomType{{
			Name: "Double Room", Price: 100, Currency: "EUR",
			Provider: "Hotels.com", Source: "google_hotels", ProviderURL: "https://www.hotels.com/ho1",
			MatchConfidence: models.RoomInventoryMatchExact,
		}},
		[]RoomType{{
			Name: "Double Room", Price: 190, NightlyPrice: 95, TotalPrice: 190, TaxesFeesIncluded: &yes,
			Currency: "EUR", Provider: "Expedia", Source: "serpapi",
			ProviderURL:     "https://www.expedia.com/h1",
			MatchConfidence: models.RoomInventoryMatchExact,
		}},
	)
	if !hasVerifiedRoom(merged) {
		t.Error("hasVerifiedRoom ignores the verified Booking quote, so SerpAPI quota would still be spent")
	}

	orig := fetchRoomAvailability
	t.Cleanup(func() { fetchRoomAvailability = orig })
	origRM := HotelRateManager
	t.Cleanup(func() { HotelRateManager = origRM })
	HotelRateManager = NewRateManager()
	fetchRoomAvailability = func(context.Context, RoomSearchOptions) (*RoomAvailability, error) {
		return &RoomAvailability{Success: true, Rooms: merged}, nil
	}

	hotels := []models.HotelResult{{
		HotelID: "/g/11x", Name: "Hotel X", Price: 120, Currency: "EUR",
		Sources: []models.PriceSource{{Provider: "google_hotels", Price: 120, Currency: "EUR"}},
	}}
	hotels = enrichHotelRooms(context.Background(), hotels, HotelSearchOptions{
		EnrichRooms: true, CheckIn: "2026-11-18", CheckOut: "2026-11-20", Currency: "EUR",
	})
	models.FinalizeHotelPriceTrust(hotels, "EUR", time.Now(), nil)

	h := hotels[0]
	if h.HeadlineProvider != "serpapi" || h.Price != 95 || h.PriceConfidence != models.PriceConfidenceVerified {
		t.Errorf("headline = %v by %q (%s), want the verified 95 under serpapi", h.Price, h.HeadlineProvider, h.PriceConfidence)
	}
	if h.BookingURL != "https://www.expedia.com/h1" {
		t.Errorf("BookingURL = %q, want the seller's own link", h.BookingURL)
	}
	var src *models.PriceSource
	for i := range h.Sources {
		if h.Sources[i].Provider == "serpapi" {
			src = &h.Sources[i]
		}
	}
	if src == nil || src.Seller != "Expedia" || src.Currency != "EUR" || src.PriceBasis != models.PriceBasisTaxInclusiveTotal {
		t.Errorf("serpapi source = %+v, want seller Expedia, EUR, tax_inclusive_total", src)
	}
	if len(h.RoomTypes) != 1 || h.RoomTypes[0].Price != 100 || h.RoomTypes[0].Provider != "Hotels.com" {
		t.Errorf("room_types = %+v, want the room to keep Hotels.com's 100", h.RoomTypes)
	}
}

// TestEnrichHotelRooms_TotalOnlyVerifiedQuoteHeadlines: a second seller's
// verified rate whose nightly amount is only in Price (TotalPrice set,
// NightlyPrice 0) still reaches the headline, as the room itself would.
func TestEnrichHotelRooms_TotalOnlyVerifiedQuoteHeadlines(t *testing.T) {
	yes := true
	merged := mergeRoomTypes(
		[]RoomType{{Name: "Double Room", Price: 100, Currency: "EUR", Provider: "Hotels.com", Source: "google_hotels",
			ProviderURL: "https://www.hotels.com/ho1", MatchConfidence: models.RoomInventoryMatchExact}},
		[]RoomType{{Name: "Double Room", Price: 95, TotalPrice: 190, TaxesFeesIncluded: &yes, Currency: "EUR",
			Provider: "Booking.com", Source: "booking.com", ProviderURL: "https://www.booking.com/hotel/at/x.html",
			MatchConfidence: models.RoomInventoryMatchExact}},
	)
	if !hasVerifiedRoom(merged) {
		t.Error("a usable verified quote must count as verified")
	}
	room := merged[0]
	if room.Price != 100 || room.NightlyPrice != 0 || room.TotalPrice != 0 || room.TaxesFeesIncluded != nil || room.Provider != "Hotels.com" {
		t.Errorf("room top-level = price %v nightly %v total %v included %v seller %q, want Hotels.com's 100 unchanged",
			room.Price, room.NightlyPrice, room.TotalPrice, room.TaxesFeesIncluded, room.Provider)
	}
	if len(room.InventoryOptions) != 2 {
		t.Fatalf("InventoryOptions = %+v, want the Google and Booking quotes", room.InventoryOptions)
	}
	q := room.InventoryOptions[1]
	if q.Provider != "Booking.com" || q.ProviderURL != "https://www.booking.com/hotel/at/x.html" || q.Currency != "EUR" ||
		q.PriceBasis != models.PriceBasisTaxInclusiveTotal || q.NightlyPrice != 95 || q.TotalPrice != 190 {
		t.Errorf("Booking quote = %+v, want Booking.com's EUR 95/190 tax_inclusive_total with its own link", q)
	}
	orig := fetchRoomAvailability
	t.Cleanup(func() { fetchRoomAvailability = orig })
	origRM := HotelRateManager
	t.Cleanup(func() { HotelRateManager = origRM })
	HotelRateManager = NewRateManager()
	fetchRoomAvailability = func(context.Context, RoomSearchOptions) (*RoomAvailability, error) {
		return &RoomAvailability{Success: true, Rooms: merged}, nil
	}
	hotels := enrichHotelRooms(context.Background(), []models.HotelResult{{
		HotelID: "/g/11x", Name: "Hotel X", Price: 120, Currency: "EUR",
		Sources: []models.PriceSource{{Provider: "google_hotels", Price: 120, Currency: "EUR"}},
	}}, HotelSearchOptions{EnrichRooms: true, CheckIn: "2026-11-18", CheckOut: "2026-11-20", Currency: "EUR"})
	models.FinalizeHotelPriceTrust(hotels, "EUR", time.Now(), nil)
	if h := hotels[0]; h.HeadlineProvider != "booking.com" || h.Price != 95 || h.PriceConfidence != models.PriceConfidenceVerified {
		t.Errorf("headline = %v by %q (%s), want Booking's verified 95", h.Price, h.HeadlineProvider, h.PriceConfidence)
	}
}

// A verified quote that cannot headline (no nightly amount at all) must not
// count as a verified room, or it would skip the fallbacks for nothing.
func TestHasVerifiedRoom_IgnoresQuoteThatCannotHeadline(t *testing.T) {
	rooms := []RoomType{{
		Name: "Double Room", Price: 100, Currency: "EUR", Provider: "Hotels.com", Source: "google_hotels",
		MatchConfidence: models.RoomInventoryMatchExact,
		InventoryOptions: []models.RoomInventoryQuote{{
			Provider: "Booking.com", Source: "booking.com", TotalPrice: 190, Currency: "EUR",
			PriceBasis: models.PriceBasisTaxInclusiveTotal, PriceConfidence: models.PriceConfidenceVerified,
		}},
	}}
	if hasVerifiedRoom(rooms) {
		t.Error("a verified quote with no nightly amount suppressed the fallbacks")
	}
}

// The room's own quote follows the same rule as a second seller's: a
// tax-inclusive room with only a stay total cannot headline, so it must not
// count as verified and skip SerpAPI and Agoda (#817 review round 3).
func TestHasVerifiedRoom_OwnQuoteMustHeadline(t *testing.T) {
	yes := true
	rooms := []RoomType{{
		Name: "Double Room", TotalPrice: 190, TaxesFeesIncluded: &yes, Currency: "EUR",
		Provider: "Booking.com", Source: "booking.com", MatchConfidence: models.RoomInventoryMatchExact,
	}}
	if hasVerifiedRoom(rooms) {
		t.Error("a total-only room that cannot headline suppressed the fallbacks")
	}
}

// A property-level lead-in beside a stay total gets no nightly backfill and
// does not count as verified.
func TestRoomInventoryQuote_PropertyLevelLeadInIsNotNightly(t *testing.T) {
	yes := true
	room := RoomType{
		Name: "Property lead-in", Price: 100, TotalPrice: 300, TaxesFeesIncluded: &yes, Currency: "EUR",
		Provider: "Hotels.com", Source: "google_hotels", MatchConfidence: models.RoomInventoryMatchPropertyLevelOnly,
	}
	if q := roomInventoryQuote(room); q.NightlyPrice != 0 {
		t.Errorf("NightlyPrice = %v, want 0: a lead-in beside a total is not a nightly rate", q.NightlyPrice)
	}
	if hasVerifiedRoom([]RoomType{room}) {
		t.Error("a property-level lead-in counted as a verified room")
	}
}
