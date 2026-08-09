package mcp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
)

func TestHandleSearchHotelsWithDetailsEnrichesTopResults(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)

	origSearchHotels := searchHotelsFunc
	origAmenities := fetchHotelAmenitiesFunc
	origRooms := getRoomAvailabilityWithOptsFunc
	t.Cleanup(func() {
		searchHotelsFunc = origSearchHotels
		fetchHotelAmenitiesFunc = origAmenities
		getRoomAvailabilityWithOptsFunc = origRooms
	})

	searchHotelsFunc = func(_ context.Context, location string, opts hotels.HotelSearchOptions) (*models.HotelSearchResult, error) {
		if location != "Paris" {
			t.Fatalf("location = %q, want Paris", location)
		}
		if opts.Currency != "EUR" {
			t.Fatalf("Currency = %q, want EUR", opts.Currency)
		}
		if len(opts.ChildrenAges) != 1 || opts.ChildrenAges[0] != 7 {
			t.Fatalf("ChildrenAges = %v, want [7]", opts.ChildrenAges)
		}
		if opts.Rooms != 1 {
			t.Fatalf("Rooms = %d, want 1", opts.Rooms)
		}
		if !opts.RefundableRequired {
			t.Fatal("RefundableRequired = false, want true")
		}
		if !opts.MustHaveKitchen || !opts.MustHaveWorkspace {
			t.Fatalf("must-have flags = kitchen %v workspace %v, want true/true", opts.MustHaveKitchen, opts.MustHaveWorkspace)
		}
		return &models.HotelSearchResult{
			Success: true,
			Count:   2,
			Hotels: []models.HotelResult{
				{
					Name:       "Hotel One",
					HotelID:    "hotel-one",
					Price:      150,
					Currency:   "EUR",
					BookingURL: "https://booking.example/hotel-one",
				},
				{
					Name:     "Hotel Two",
					HotelID:  "hotel-two",
					Price:    180,
					Currency: "EUR",
				},
			},
		}, nil
	}
	fetchHotelAmenitiesFunc = func(_ context.Context, hotelID string) ([]string, error) {
		if hotelID != "hotel-one" {
			t.Fatalf("amenities hotelID = %q, want hotel-one", hotelID)
		}
		return []string{"Pool", "Spa"}, nil
	}
	getRoomAvailabilityWithOptsFunc = func(_ context.Context, opts hotels.RoomSearchOptions) (*hotels.RoomAvailability, error) {
		if opts.HotelID != "hotel-one" {
			t.Fatalf("room HotelID = %q, want hotel-one", opts.HotelID)
		}
		if opts.BookingURL != "https://booking.example/hotel-one" {
			t.Fatalf("BookingURL = %q, want search result URL", opts.BookingURL)
		}
		if opts.Location != "Paris" {
			t.Fatalf("Location = %q, want Paris", opts.Location)
		}
		if opts.Guests != 3 {
			t.Fatalf("Guests = %d, want requested guests 3", opts.Guests)
		}
		if len(opts.ChildrenAges) != 1 || opts.ChildrenAges[0] != 7 {
			t.Fatalf("room ChildrenAges = %v, want [7]", opts.ChildrenAges)
		}
		if opts.Rooms != 1 {
			t.Fatalf("room Rooms = %d, want 1", opts.Rooms)
		}
		return &hotels.RoomAvailability{
			Success: true,
			HotelID: "hotel-one",
			Name:    "Hotel One",
			Rooms: []hotels.RoomType{
				{
					Name:               "Deluxe Room",
					Price:              160,
					NightlyPrice:       160,
					TotalPrice:         320,
					TaxesAndFees:       24,
					TaxesFeesIncluded:  boolPtr(true),
					Currency:           "EUR",
					Provider:           "Booking.com",
					MaxGuests:          3,
					Amenities:          []string{"Kitchen", "Workspace", "Free WiFi"},
					CancellationPolicy: "free_cancellation",
					Refundable:         boolPtr(true),
					FreeCancellation:   boolPtr(true),
					Board:              "breakfast_included",
					BreakfastIncluded:  boolPtr(true),
				},
			},
		}, nil
	}

	content, structured, err := handleSearchHotelsWithDetails(context.Background(), map[string]any{
		"location":            "Paris",
		"check_in":            futureDateAfter(30),
		"check_out":           futureDateAfter(32),
		"guests":              3,
		"children_ages":       []any{float64(7)},
		"rooms":               1,
		"currency":            "eur",
		"room_type":           "hotel_room",
		"refundable_required": true,
		"must_have_kitchen":   true,
		"must_have_workspace": true,
		"amenities":           "kitchen,workspace",
		"max_hotels":          1,
		"include_rooms":       true,
		"include_amenities":   true,
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("handleSearchHotelsWithDetails: %v", err)
	}
	if len(content) == 0 {
		t.Fatal("expected summary content")
	}
	if got := content[0].Text; got != "Enriched 1 of 2 hotels in Paris. Found 1 room type. 1 hotel has room-level verified_rate. 1 with free cancellation. 1 with breakfast included." {
		t.Fatalf("summary = %q, want sensible detail summary", got)
	}
	resp, ok := structured.(hotelDetailsSearchResponse)
	if !ok {
		t.Fatalf("structured type = %T, want hotelDetailsSearchResponse", structured)
	}
	if resp.TotalAvailable != 2 {
		t.Fatalf("TotalAvailable = %d, want 2", resp.TotalAvailable)
	}
	if resp.Count != 1 {
		t.Fatalf("Count = %d, want 1", resp.Count)
	}
	if len(resp.Hotels) != 1 {
		t.Fatalf("len(Hotels) = %d, want 1", len(resp.Hotels))
	}
	got := resp.Hotels[0]
	if got.Name != "Hotel One" {
		t.Fatalf("hotel name = %q, want Hotel One", got.Name)
	}
	if len(got.Amenities) != 2 {
		t.Fatalf("amenities = %#v, want two enriched amenities", got.Amenities)
	}
	if len(got.RoomTypes) != 1 {
		t.Fatalf("room types = %#v, want one enriched room", got.RoomTypes)
	}
	if len(got.DetailErrors) != 0 {
		t.Fatalf("DetailErrors = %#v, want none", got.DetailErrors)
	}
	if got.VerifiedRate == nil {
		t.Fatal("VerifiedRate = nil, want room-level rate")
	}
	if got.VerifiedRate.Price != 320 || got.VerifiedRate.PriceBasis != models.PriceBasisTaxInclusiveTotal || got.VerifiedRate.PriceConfidence != models.PriceConfidenceVerified {
		t.Fatalf("verified rate = %#v, want tax-inclusive total 320 with verified confidence", got.VerifiedRate)
	}
	assertBoolPointer(t, "verified_rate.Refundable", got.VerifiedRate.Refundable, true)
	assertBoolPointer(t, "verified_rate.FreeCancellation", got.VerifiedRate.FreeCancellation, true)
	if len(got.AccommodationOffers) != 1 {
		t.Fatalf("AccommodationOffers = %#v, want one matched offer", got.AccommodationOffers)
	}
	offer := got.AccommodationOffers[0]
	if !offer.CriteriaMatched || !offer.BookingReadyStatus || !offer.FinalTripCostReadyStatus {
		t.Fatalf("offer readiness = matched %v booking %v final %v, offer=%#v",
			offer.CriteriaMatched, offer.BookingReadyStatus, offer.FinalTripCostReadyStatus, offer)
	}
	if offer.BookingOrderHint != models.BookingOrderFlightsFirstOK {
		t.Fatalf("BookingOrderHint = %q, want %q", offer.BookingOrderHint, models.BookingOrderFlightsFirstOK)
	}
	room := got.RoomTypes[0]
	if room.NightlyPrice != 160 || room.TotalPrice != 320 || room.TaxesAndFees != 24 {
		t.Fatalf("room price metadata = nightly %v total %v fees %v, want 160/320/24", room.NightlyPrice, room.TotalPrice, room.TaxesAndFees)
	}
	if room.CancellationPolicy != "free_cancellation" || room.Board != "breakfast_included" {
		t.Fatalf("room decision metadata = cancellation %q board %q", room.CancellationPolicy, room.Board)
	}
	assertBoolPointer(t, "room.TaxesFeesIncluded", room.TaxesFeesIncluded, true)
	assertBoolPointer(t, "room.Refundable", room.Refundable, true)
	assertBoolPointer(t, "room.FreeCancellation", room.FreeCancellation, true)
	assertBoolPointer(t, "room.BreakfastIncluded", room.BreakfastIncluded, true)
}

func TestHandleSearchHotelsWithDetailsPartialFailuresUseTypedDetailErrors(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)

	origSearchHotels := searchHotelsFunc
	origAmenities := fetchHotelAmenitiesFunc
	origRooms := getRoomAvailabilityWithOptsFunc
	t.Cleanup(func() {
		searchHotelsFunc = origSearchHotels
		fetchHotelAmenitiesFunc = origAmenities
		getRoomAvailabilityWithOptsFunc = origRooms
	})

	searchHotelsFunc = func(_ context.Context, location string, opts hotels.HotelSearchOptions) (*models.HotelSearchResult, error) {
		return &models.HotelSearchResult{
			Success: true,
			Count:   2,
			Hotels: []models.HotelResult{
				{Name: "Hotel Broken", HotelID: "hotel-broken", Price: 120, Currency: "EUR"},
				{Name: "Hotel OK", HotelID: "hotel-ok", Price: 140, Currency: "EUR"},
			},
		}, nil
	}
	fetchHotelAmenitiesFunc = func(_ context.Context, hotelID string) ([]string, error) {
		if hotelID == "hotel-broken" {
			return nil, errors.New("upstream timeout")
		}
		return []string{"Free WiFi"}, nil
	}
	getRoomAvailabilityWithOptsFunc = func(_ context.Context, opts hotels.RoomSearchOptions) (*hotels.RoomAvailability, error) {
		if opts.HotelID == "hotel-broken" {
			return nil, errors.New("room fetch failed")
		}
		return &hotels.RoomAvailability{
			Success: true,
			HotelID: opts.HotelID,
			Rooms:   []hotels.RoomType{{Name: "Standard Room", Price: 140, Currency: "EUR"}},
		}, nil
	}

	content, structured, err := handleSearchHotelsWithDetails(context.Background(), map[string]any{
		"location":          "Paris",
		"check_in":          futureDateAfter(30),
		"check_out":         futureDateAfter(32),
		"currency":          "EUR",
		"max_hotels":        2,
		"include_rooms":     true,
		"include_amenities": true,
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("handleSearchHotelsWithDetails: %v", err)
	}
	if got := content[0].Text; got != "Enriched 2 of 2 hotels in Paris. Found 1 room type. 1 hotel has room-level verified_rate. 2 detail lookups had partial failures." {
		t.Fatalf("summary = %q, want partial-success summary", got)
	}
	resp := structured.(hotelDetailsSearchResponse)
	if !resp.Success {
		t.Fatal("response should remain successful when one hotel's detail fetch fails")
	}
	if len(resp.Hotels) != 2 {
		t.Fatalf("len(Hotels) = %d, want 2", len(resp.Hotels))
	}
	if len(resp.Hotels[0].DetailErrors) != 2 {
		t.Fatalf("DetailErrors = %#v, want two typed errors", resp.Hotels[0].DetailErrors)
	}
	if resp.Hotels[0].DetailErrors[0].Scope != "amenities" || resp.Hotels[0].DetailErrors[0].Code != "amenities_fetch_failed" {
		t.Fatalf("first detail error = %#v, want amenities_fetch_failed", resp.Hotels[0].DetailErrors[0])
	}
	if resp.Hotels[0].DetailErrors[1].Scope != "rooms" || resp.Hotels[0].DetailErrors[1].Code != "rooms_fetch_failed" {
		t.Fatalf("second detail error = %#v, want rooms_fetch_failed", resp.Hotels[0].DetailErrors[1])
	}
	if len(resp.Hotels[1].DetailErrors) != 0 || len(resp.Hotels[1].RoomTypes) != 1 {
		t.Fatalf("successful hotel = errors %#v rooms %#v, want no errors and one room", resp.Hotels[1].DetailErrors, resp.Hotels[1].RoomTypes)
	}
	if resp.Hotels[1].VerifiedRate == nil || resp.Hotels[1].VerifiedRate.PriceBasis != models.PriceBasisRoomNightly {
		t.Fatalf("successful hotel verified rate = %#v, want nightly room-level rate", resp.Hotels[1].VerifiedRate)
	}
}

func TestHotelDetailsSummaryAbsentSafe(t *testing.T) {
	// Rooms that expose no cancellation/board metadata must not produce
	// fabricated "free cancellation" / "breakfast included" summary clauses.
	resp := hotelDetailsSearchResponse{
		Success:        true,
		Count:          1,
		TotalAvailable: 1,
		Hotels: []hotelWithDetails{
			{
				HotelResult: models.HotelResult{Name: "Plain Hotel"},
				RoomTypes: []hotels.RoomType{
					{Name: "Standard Room", Price: 100, Currency: "EUR"},
				},
			},
		},
	}
	got := hotelDetailsSummary(resp, "Paris")
	want := "Enriched 1 of 1 hotels in Paris. Found 1 room type."
	if got != want {
		t.Fatalf("summary = %q, want %q (no fabricated enrichment clauses)", got, want)
	}
}

func TestHotelDetailsSummaryEnrichmentClauses(t *testing.T) {
	resp := hotelDetailsSearchResponse{
		Success:        true,
		Count:          1,
		TotalAvailable: 1,
		Hotels: []hotelWithDetails{
			{
				HotelResult: models.HotelResult{Name: "Rich Hotel"},
				RoomTypes: []hotels.RoomType{
					{Name: "Flex Room", Price: 200, Currency: "EUR", FreeCancellation: boolPtr(true), BreakfastIncluded: boolPtr(true)},
					{Name: "Saver Room", Price: 150, Currency: "EUR", CancellationPolicy: "non_refundable"},
				},
			},
		},
	}
	got := hotelDetailsSummary(resp, "Paris")
	want := "Enriched 1 of 1 hotels in Paris. Found 2 room types. 1 with free cancellation. 1 with breakfast included."
	if got != want {
		t.Fatalf("summary = %q, want %q", got, want)
	}
}

func TestAccommodationOffersFromRoomsGroupsOTAInventoryByCanonicalRoom(t *testing.T) {
	checkedAt := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	refundable := true
	taxesIncluded := true
	need := models.AccommodationNeed{
		Adults:             2,
		Rooms:              1,
		AccommodationType:  models.AccommodationTypeHotelRoom,
		RefundableRequired: true,
		Currency:           "EUR",
	}
	hotel := models.HotelResult{
		Name:       "Hotel One",
		HotelID:    "hotel-one",
		BookingURL: "https://google.example/hotel-one",
	}
	rooms := []hotels.RoomType{
		{
			Name:              "Deluxe Room",
			Price:             180,
			NightlyPrice:      180,
			TotalPrice:        360,
			TaxesFeesIncluded: &taxesIncluded,
			Currency:          "EUR",
			Provider:          "Booking.com",
			ProviderURL:       "https://booking.example/deluxe",
			MaxGuests:         2,
			Refundable:        &refundable,
			MatchConfidence:   models.RoomInventoryMatchExact,
		},
		{
			Name:              "Deluxe Room",
			Price:             170,
			NightlyPrice:      170,
			TotalPrice:        340,
			TaxesFeesIncluded: &taxesIncluded,
			Currency:          "EUR",
			Provider:          "Hotels.com",
			ProviderURL:       "https://hotels.example/deluxe",
			MaxGuests:         2,
			Refundable:        &refundable,
			MatchConfidence:   models.RoomInventoryMatchExact,
		},
	}

	offers := accommodationOffersFromRooms(hotel, rooms, need, checkedAt)
	if len(offers) != 1 {
		t.Fatalf("offers = %#v, want one canonical room offer", offers)
	}
	offer := offers[0]
	if offer.RoomName != "Deluxe Room" {
		t.Fatalf("RoomName = %q, want Deluxe Room", offer.RoomName)
	}
	if offer.Provider != "Hotels.com" || offer.TotalPrice != 340 {
		t.Fatalf("selected quote = provider %q total %.0f, want Hotels.com 340", offer.Provider, offer.TotalPrice)
	}
	if offer.InventoryCompleteness != models.RoomInventoryCompletenessMultiProviderExact {
		t.Fatalf("InventoryCompleteness = %q, want %q", offer.InventoryCompleteness, models.RoomInventoryCompletenessMultiProviderExact)
	}
	if len(offer.InventoryOptions) != 2 {
		t.Fatalf("InventoryOptions = %#v, want two OTA quotes", offer.InventoryOptions)
	}
	if !offer.BookingReadyStatus || !offer.FinalTripCostReadyStatus {
		t.Fatalf("offer readiness = booking %v final %v, offer=%#v", offer.BookingReadyStatus, offer.FinalTripCostReadyStatus, offer)
	}
}

func TestAccommodationOffersFromRoomsKeepsPropertyLevelFallbackOutOfBookingReady(t *testing.T) {
	checkedAt := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	need := models.AccommodationNeed{
		Adults:            2,
		AccommodationType: models.AccommodationTypeHotelRoom,
		Currency:          "EUR",
	}
	hotel := models.HotelResult{Name: "Hotel One", HotelID: "hotel-one"}
	rooms := []hotels.RoomType{{
		Name:            "Standard Room",
		Price:           99,
		Currency:        "EUR",
		Provider:        "Google Hotels",
		ProviderURL:     "https://google.example/hotel-one",
		MatchConfidence: models.RoomInventoryMatchPropertyLevelOnly,
	}}

	offers := accommodationOffersFromRooms(hotel, rooms, need, checkedAt)
	if len(offers) != 1 {
		t.Fatalf("offers = %#v, want one fallback offer", offers)
	}
	offer := offers[0]
	if offer.BookingReadyStatus || offer.FinalTripCostReadyStatus {
		t.Fatalf("fallback readiness = booking %v final %v, want false/false", offer.BookingReadyStatus, offer.FinalTripCostReadyStatus)
	}
	if offer.CriteriaMatched {
		t.Fatalf("fallback CriteriaMatched = true, want false for property-level inventory")
	}
	if !stringSliceContains(offer.UnknownCriteria, "room_inventory") {
		t.Fatalf("UnknownCriteria = %v, want room_inventory", offer.UnknownCriteria)
	}
	if offer.PriceBasis != models.PriceBasisLeadIn || offer.PriceConfidence != models.PriceConfidenceUnverified {
		t.Fatalf("price trust = %s/%s, want lead_in/unverified", offer.PriceBasis, offer.PriceConfidence)
	}
	if offer.InventoryCompleteness != models.RoomInventoryCompletenessPropertyLevelOnly {
		t.Fatalf("InventoryCompleteness = %q, want property_level_only", offer.InventoryCompleteness)
	}
	if len(offer.InventoryOptions) != 1 || offer.InventoryOptions[0].MatchConfidence != models.RoomInventoryMatchPropertyLevelOnly {
		t.Fatalf("InventoryOptions = %#v, want property-level quote", offer.InventoryOptions)
	}
}

func TestAccommodationOffersFromRoomsUsesPropertyTypeAsEvidence(t *testing.T) {
	checkedAt := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	taxesIncluded := true
	need := models.AccommodationNeed{
		Adults:            2,
		AccommodationType: models.AccommodationTypeEntireApartment,
		Currency:          "EUR",
		RequiredAmenities: []string{"kitchen"},
	}
	hotel := models.HotelResult{
		Name:         "City Hostel",
		HotelID:      "city-hostel",
		PropertyType: "hostel",
	}
	rooms := []hotels.RoomType{{
		Name:              "Private Room",
		Price:             100,
		NightlyPrice:      100,
		TotalPrice:        200,
		TaxesFeesIncluded: &taxesIncluded,
		Currency:          "EUR",
		Provider:          "Google Hotels",
		MaxGuests:         2,
		Amenities:         []string{"Kitchen"},
		MatchConfidence:   models.RoomInventoryMatchExact,
	}}

	offers := accommodationOffersFromRooms(hotel, rooms, need, checkedAt)
	if len(offers) != 1 {
		t.Fatalf("offers = %#v, want one offer", offers)
	}
	offer := offers[0]
	if offer.AccommodationType != models.AccommodationTypeHostelBed {
		t.Fatalf("AccommodationType = %q, want hostel_bed from property evidence", offer.AccommodationType)
	}
	if offer.CriteriaMatched {
		t.Fatalf("CriteriaMatched = true, want false for hostel when user requested entire apartment")
	}
	if !stringSliceContains(offer.MissingCriteria, "accommodation_type") {
		t.Fatalf("MissingCriteria = %v, want accommodation_type", offer.MissingCriteria)
	}
}

func boolPtr(v bool) *bool {
	return &v
}

func assertBoolPointer(t *testing.T, name string, got *bool, want bool) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s = nil, want %v", name, want)
	}
	if *got != want {
		t.Fatalf("%s = %v, want %v", name, *got, want)
	}
}
