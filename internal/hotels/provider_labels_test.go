package hotels

import (
	"context"
	"slices"
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

// TestMergeRoomTypes_AttributionFollowsHeadlineNumber: when Booking supplies
// the nightly rate that roomHeadlinePrice publishes, the room is credited to
// Booking, not to the Google seller whose total price it kept.
func TestMergeRoomTypes_AttributionFollowsHeadlineNumber(t *testing.T) {
	google := []RoomType{{
		Name: "Double Room", Price: 100, Currency: "EUR",
		Provider: "Hotels.com", Source: "google_hotels", ProviderURL: "https://www.hotels.com/ho1",
	}}
	booking := []RoomType{{
		Name: "Double Room", Price: 95, NightlyPrice: 95, Currency: "EUR",
		Provider: "Booking.com", Source: "booking.com", ProviderURL: "https://www.booking.com/hotel/at/x.html",
	}}
	merged := mergeRoomTypes(google, booking)
	if len(merged) != 1 {
		t.Fatalf("merged %d rooms, want 1", len(merged))
	}
	src, ok := bestRoomSource([]models.Room{roomTypeToModelRoom(merged[0])})
	if !ok {
		t.Fatal("no priced room")
	}
	if src.Price != 95 {
		t.Fatalf("published price = %v, want Booking's nightly 95", src.Price)
	}
	if src.Provider != "booking.com" || src.BookingURL != "https://www.booking.com/hotel/at/x.html" {
		t.Errorf("published 95 credited to %q via %q, want booking.com and the Booking link", src.Provider, src.BookingURL)
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
