package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
)

func TestHandleSearchAccommodationsReturnsOnlyCriteriaMatchedOffers(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	origSearchHotels := searchHotelsFunc
	origRooms := getRoomAvailabilityWithOptsFunc
	t.Cleanup(func() {
		searchHotelsFunc = origSearchHotels
		getRoomAvailabilityWithOptsFunc = origRooms
	})

	searchHotelsFunc = func(_ context.Context, location string, opts hotels.HotelSearchOptions) (*models.HotelSearchResult, error) {
		if location != "Paris" {
			t.Fatalf("location = %q, want Paris", location)
		}
		if opts.Guests != 3 {
			t.Fatalf("Guests = %d, want adults + child = 3", opts.Guests)
		}
		if len(opts.ChildrenAges) != 1 || opts.ChildrenAges[0] != 7 {
			t.Fatalf("ChildrenAges = %v, want [7]", opts.ChildrenAges)
		}
		if opts.RoomType != "hotel_room" || opts.PropertyType != "hotel" {
			t.Fatalf("type filters = room %q property %q, want hotel_room/hotel", opts.RoomType, opts.PropertyType)
		}
		if !opts.MustHaveKitchen || !opts.RefundableRequired {
			t.Fatalf("hard filters = kitchen %v refundable %v, want true/true", opts.MustHaveKitchen, opts.RefundableRequired)
		}
		return &models.HotelSearchResult{
			Success: true,
			Count:   2,
			Hotels: []models.HotelResult{
				{
					Name:            "Hotel One",
					HotelID:         "hotel-one",
					Price:           100,
					Currency:        "EUR",
					PropertyType:    "hotel",
					BookingURL:      "https://booking.example/hotel-one",
					PriceBasis:      models.PriceBasisLeadIn,
					PriceConfidence: models.PriceConfidenceUnverified,
				},
				{
					Name:            "Hotel Two",
					HotelID:         "hotel-two",
					Price:           90,
					Currency:        "EUR",
					PropertyType:    "hotel",
					PriceBasis:      models.PriceBasisLeadIn,
					PriceConfidence: models.PriceConfidenceUnverified,
				},
			},
		}, nil
	}

	getRoomAvailabilityWithOptsFunc = func(_ context.Context, opts hotels.RoomSearchOptions) (*hotels.RoomAvailability, error) {
		if opts.Guests != 3 {
			t.Fatalf("room Guests = %d, want 3", opts.Guests)
		}
		if len(opts.ChildrenAges) != 1 || opts.ChildrenAges[0] != 7 {
			t.Fatalf("room ChildrenAges = %v, want [7]", opts.ChildrenAges)
		}
		if opts.HotelID == "hotel-two" {
			return nil, errors.New("provider timeout")
		}
		if opts.HotelID != "hotel-one" {
			t.Fatalf("unexpected HotelID %q", opts.HotelID)
		}
		return &hotels.RoomAvailability{
			Success: true,
			HotelID: "hotel-one",
			Name:    "Hotel One",
			Rooms: []hotels.RoomType{
				{
					Name:               "Family Room",
					Price:              160,
					NightlyPrice:       160,
					TotalPrice:         320,
					TaxesAndFees:       24,
					TaxesFeesIncluded:  boolPtr(true),
					Currency:           "EUR",
					Provider:           "Booking.com",
					MaxGuests:          3,
					Amenities:          []string{"Kitchen", "Free WiFi"},
					CancellationPolicy: "free_cancellation",
					Refundable:         boolPtr(true),
					FreeCancellation:   boolPtr(true),
				},
				{
					Name:         "Double Room",
					Price:        110,
					NightlyPrice: 110,
					TotalPrice:   220,
					Currency:     "EUR",
					Provider:     "Booking.com",
					MaxGuests:    2,
					Amenities:    []string{"Free WiFi"},
				},
			},
		}, nil
	}

	content, structured, err := handleSearchAccommodations(context.Background(), map[string]any{
		"location":            "Paris",
		"check_in":            "2026-07-10",
		"check_out":           "2026-07-12",
		"adults":              2,
		"children_ages":       []any{float64(7)},
		"currency":            "eur",
		"accommodation_type":  "hotel_room",
		"amenities":           "wifi",
		"must_have_kitchen":   true,
		"refundable_required": true,
		"max_candidates":      2,
		"include_unmatched":   true,
		"include_candidates":  true,
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("handleSearchAccommodations: %v", err)
	}
	if len(content) == 0 || !strings.Contains(content[0].Text, "criteria-matched accommodation offer") {
		t.Fatalf("summary content = %#v, want criteria-matched summary", content)
	}
	resp, ok := structured.(accommodationSearchResponse)
	if !ok {
		t.Fatalf("structured type = %T, want accommodationSearchResponse", structured)
	}
	if resp.Count != 1 || resp.MatchingCount != 1 || resp.BookingReadyCount != 1 || resp.FinalTripCostReadyCount != 1 {
		t.Fatalf("counts = count %d matching %d booking %d final %d, want 1/1/1/1",
			resp.Count, resp.MatchingCount, resp.BookingReadyCount, resp.FinalTripCostReadyCount)
	}
	if len(resp.Offers) != 1 {
		t.Fatalf("Offers = %#v, want one matched offer", resp.Offers)
	}
	offer := resp.Offers[0]
	if offer.PropertyName != "Hotel One" || offer.RoomName != "Family Room" {
		t.Fatalf("offer = %s/%s, want Hotel One/Family Room", offer.PropertyName, offer.RoomName)
	}
	if !offer.CriteriaMatched || !offer.BookingReadyStatus || !offer.FinalTripCostReadyStatus {
		t.Fatalf("offer readiness = matched %v booking %v final %v, offer=%#v",
			offer.CriteriaMatched, offer.BookingReadyStatus, offer.FinalTripCostReadyStatus, offer)
	}
	if offer.PriceBasis != models.PriceBasisTaxInclusiveTotal || offer.PriceConfidence != models.PriceConfidenceVerified {
		t.Fatalf("offer price trust = %s/%s, want tax-inclusive verified", offer.PriceBasis, offer.PriceConfidence)
	}
	if offer.BookingOrderHint != models.BookingOrderFlightsFirstOK {
		t.Fatalf("BookingOrderHint = %q, want %q", offer.BookingOrderHint, models.BookingOrderFlightsFirstOK)
	}
	if len(resp.RejectedOffers) != 1 {
		t.Fatalf("RejectedOffers = %#v, want one rejected room", resp.RejectedOffers)
	}
	rejected := resp.RejectedOffers[0]
	if rejected.RoomName != "Double Room" {
		t.Fatalf("rejected room = %q, want Double Room", rejected.RoomName)
	}
	if rejected.CriteriaMatched {
		t.Fatalf("rejected offer unexpectedly matched: %#v", rejected)
	}
	if len(resp.Candidates) != 2 {
		t.Fatalf("Candidates = %#v, want two candidates", resp.Candidates)
	}
	if resp.Candidates[0].LeadInPrice != 100 || resp.Candidates[0].OfferCount != 2 || resp.Candidates[0].MatchingOfferCount != 1 {
		t.Fatalf("first candidate = %#v, want lead-in 100 with 2 offers/1 match", resp.Candidates[0])
	}
	if len(resp.Candidates[1].DetailErrors) != 1 || resp.Candidates[1].DetailErrors[0].Code != "rooms_fetch_failed" {
		t.Fatalf("second candidate errors = %#v, want rooms_fetch_failed", resp.Candidates[1].DetailErrors)
	}
	if !stringSliceContains(resp.Warnings, "lead_in_prices_are_candidates_only") {
		t.Fatalf("warnings = %v, want lead_in_prices_are_candidates_only", resp.Warnings)
	}
}

func TestHandleSearchAccommodationsUsesProviderRoomInventoryWithoutHotelID(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	origSearchHotels := searchHotelsFunc
	origRooms := getRoomAvailabilityWithOptsFunc
	t.Cleanup(func() {
		searchHotelsFunc = origSearchHotels
		getRoomAvailabilityWithOptsFunc = origRooms
	})

	searchHotelsFunc = func(_ context.Context, location string, opts hotels.HotelSearchOptions) (*models.HotelSearchResult, error) {
		if location != "Paris" {
			t.Fatalf("location = %q, want Paris", location)
		}
		if !opts.FreeCancellation {
			t.Fatal("FreeCancellation = false, want true from free_cancellation_required")
		}
		if len(opts.Amenities) != 0 {
			t.Fatalf("Amenities = %v, want relaxed discovery filters for need-level amenities", opts.Amenities)
		}
		return &models.HotelSearchResult{
			Success: true,
			Count:   1,
			Hotels: []models.HotelResult{{
				Name:         "Provider Apartment",
				Price:        500,
				Currency:     "EUR",
				PropertyType: "apartment",
				BookingURL:   "https://hometogo.example/provider-apartment",
				RoomTypes: []models.Room{{
					Name:             "Entire Apartment",
					Price:            500,
					TotalPrice:       500,
					Currency:         "EUR",
					Provider:         "hometogo",
					ProviderURL:      "https://hometogo.example/provider-apartment",
					MaxGuests:        3,
					Amenities:        []string{"Kitchen", "Free WiFi", "Washing Machine"},
					FreeCancellation: true,
					MatchConfidence:  models.RoomInventoryMatchExact,
					PriceBasis:       models.PriceBasisRoomTotal,
					PriceConfidence:  models.PriceConfidenceRoomLevel,
				}},
				Sources: []models.PriceSource{{
					Provider:        "hometogo",
					Price:           500,
					Currency:        "EUR",
					BookingURL:      "https://hometogo.example/provider-apartment",
					RoomCount:       1,
					PriceBasis:      models.PriceBasisRoomTotal,
					PriceConfidence: models.PriceConfidenceRoomLevel,
				}},
			}},
		}, nil
	}
	getRoomAvailabilityWithOptsFunc = func(context.Context, hotels.RoomSearchOptions) (*hotels.RoomAvailability, error) {
		t.Fatal("room availability fetch should not run without hotel_id when provider room inventory is present")
		return nil, nil
	}

	_, structured, err := handleSearchAccommodations(context.Background(), map[string]any{
		"location":                   "Paris",
		"check_in":                   "2026-07-10",
		"check_out":                  "2026-07-12",
		"adults":                     2,
		"currency":                   "eur",
		"accommodation_type":         "entire_apartment",
		"must_have_kitchen":          true,
		"must_have_washing_machine":  true,
		"free_cancellation_required": true,
		"max_candidates":             1,
		"include_candidates":         true,
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("handleSearchAccommodations: %v", err)
	}
	resp, ok := structured.(accommodationSearchResponse)
	if !ok {
		t.Fatalf("structured type = %T, want accommodationSearchResponse", structured)
	}
	if resp.Count != 1 || resp.BookingReadyCount != 1 || resp.FinalTripCostReadyCount != 0 {
		t.Fatalf("counts = count %d booking %d final %d, want 1/1/0", resp.Count, resp.BookingReadyCount, resp.FinalTripCostReadyCount)
	}
	offer := resp.Offers[0]
	if offer.Provider != "HomeToGo" || offer.TotalPrice != 500 || offer.InventoryCompleteness != models.RoomInventoryCompletenessSingleProvider {
		t.Fatalf("offer = %#v, want single-provider HomeToGo total 500", offer)
	}
	if !offer.BookingReadyStatus || offer.FinalTripCostReadyStatus {
		t.Fatalf("readiness = booking %v final %v, want true/false", offer.BookingReadyStatus, offer.FinalTripCostReadyStatus)
	}
	if len(resp.Candidates) != 1 || len(resp.Candidates[0].DetailErrors) != 0 {
		t.Fatalf("candidate = %#v, want provider inventory without detail errors", resp.Candidates)
	}
}

func TestHandleSearchAccommodationsPrioritizesVerifiableCandidateOverLeadInOnly(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	origSearchHotels := searchHotelsFunc
	origRooms := getRoomAvailabilityWithOptsFunc
	t.Cleanup(func() {
		searchHotelsFunc = origSearchHotels
		getRoomAvailabilityWithOptsFunc = origRooms
	})

	searchHotelsFunc = func(_ context.Context, location string, opts hotels.HotelSearchOptions) (*models.HotelSearchResult, error) {
		if location != "Paris" {
			t.Fatalf("location = %q, want Paris", location)
		}
		return &models.HotelSearchResult{
			Success: true,
			Count:   2,
			Hotels: []models.HotelResult{
				{
					Name:            "Cheap Lead-In Apartment",
					Price:           40,
					Currency:        "EUR",
					PropertyType:    "apartment",
					PriceBasis:      models.PriceBasisLeadIn,
					PriceConfidence: models.PriceConfidenceUnverified,
					RoomTypes: []models.Room{{
						Name:            "Property lead-in rate",
						Price:           40,
						Currency:        "EUR",
						Provider:        "HomeToGo",
						MatchConfidence: models.RoomInventoryMatchPropertyLevelOnly,
						PriceBasis:      models.PriceBasisLeadIn,
						PriceConfidence: models.PriceConfidenceUnverified,
					}},
				},
				{
					Name:            "Verified Apartment",
					HotelID:         "verified-apartment",
					Price:           120,
					Currency:        "EUR",
					PropertyType:    "apartment",
					BookingURL:      "https://booking.example/verified-apartment",
					PriceBasis:      models.PriceBasisLeadIn,
					PriceConfidence: models.PriceConfidenceUnverified,
				},
			},
		}, nil
	}
	getRoomAvailabilityWithOptsFunc = func(_ context.Context, opts hotels.RoomSearchOptions) (*hotels.RoomAvailability, error) {
		if opts.HotelID != "verified-apartment" {
			t.Fatalf("HotelID = %q, want verified-apartment", opts.HotelID)
		}
		return &hotels.RoomAvailability{
			Success: true,
			HotelID: "verified-apartment",
			Name:    "Verified Apartment",
			Rooms: []hotels.RoomType{{
				Name:               "Entire Apartment",
				NightlyPrice:       150,
				TotalPrice:         300,
				TaxesFeesIncluded:  boolPtr(true),
				Currency:           "EUR",
				Provider:           "Booking.com",
				ProviderURL:        "https://booking.example/verified-apartment/room",
				MaxGuests:          3,
				Amenities:          []string{"Kitchen", "Free WiFi", "Washing Machine"},
				FreeCancellation:   boolPtr(true),
				CancellationPolicy: "free_cancellation",
				MatchConfidence:    models.RoomInventoryMatchExact,
				InventoryOptions:   nil,
				BreakfastIncluded:  nil,
				Refundable:         nil,
				Price:              150,
				TaxesAndFees:       0,
				RateID:             "rate-1",
				RatePlanName:       "Free cancellation",
				Board:              "room_only",
			}},
		}, nil
	}

	_, structured, err := handleSearchAccommodations(context.Background(), map[string]any{
		"location":                   "Paris",
		"check_in":                   "2026-07-10",
		"check_out":                  "2026-07-12",
		"adults":                     2,
		"children_ages":              []any{float64(7)},
		"currency":                   "eur",
		"accommodation_type":         "entire_apartment",
		"must_have_kitchen":          true,
		"must_have_washing_machine":  true,
		"free_cancellation_required": true,
		"max_candidates":             1,
		"include_candidates":         true,
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("handleSearchAccommodations: %v", err)
	}
	resp := structured.(accommodationSearchResponse)
	if resp.Count != 1 || resp.BookingReadyCount != 1 || resp.FinalTripCostReadyCount != 1 {
		t.Fatalf("counts = count %d booking %d final %d, want 1/1/1", resp.Count, resp.BookingReadyCount, resp.FinalTripCostReadyCount)
	}
	if len(resp.Candidates) != 1 || resp.Candidates[0].Name != "Verified Apartment" {
		t.Fatalf("Candidates = %#v, want only the verifiable apartment", resp.Candidates)
	}
	if got := resp.Offers[0].PropertyName; got != "Verified Apartment" {
		t.Fatalf("offer property = %q, want Verified Apartment", got)
	}
}

func TestHandleSearchAccommodationsPrioritizesRequestedAccommodationType(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	origSearchHotels := searchHotelsFunc
	origRooms := getRoomAvailabilityWithOptsFunc
	t.Cleanup(func() {
		searchHotelsFunc = origSearchHotels
		getRoomAvailabilityWithOptsFunc = origRooms
	})

	searchHotelsFunc = func(context.Context, string, hotels.HotelSearchOptions) (*models.HotelSearchResult, error) {
		return &models.HotelSearchResult{
			Success: true,
			Count:   2,
			Hotels: []models.HotelResult{
				{
					Name:         "Cheap Hostel",
					HotelID:      "cheap-hostel",
					Price:        40,
					Currency:     "EUR",
					PropertyType: "hostel",
				},
				{
					Name:         "Actual Apartment",
					Price:        160,
					Currency:     "EUR",
					PropertyType: "apartment",
					RoomTypes: []models.Room{{
						Name:             "Entire Apartment",
						TotalPrice:       320,
						Currency:         "EUR",
						Provider:         "HomeToGo",
						Amenities:        []string{"Kitchen", "Free WiFi"},
						FreeCancellation: true,
						MatchConfidence:  models.RoomInventoryMatchExact,
						PriceBasis:       models.PriceBasisRoomTotal,
						PriceConfidence:  models.PriceConfidenceRoomLevel,
					}},
				},
			},
		}, nil
	}
	getRoomAvailabilityWithOptsFunc = func(context.Context, hotels.RoomSearchOptions) (*hotels.RoomAvailability, error) {
		t.Fatal("room availability should not be spent on a mismatched hostel when apartment inventory exists")
		return nil, nil
	}

	_, structured, err := handleSearchAccommodations(context.Background(), map[string]any{
		"location":                   "Paris",
		"check_in":                   "2026-07-10",
		"check_out":                  "2026-07-12",
		"adults":                     2,
		"currency":                   "eur",
		"accommodation_type":         "entire_apartment",
		"must_have_kitchen":          true,
		"free_cancellation_required": true,
		"max_candidates":             1,
		"include_candidates":         true,
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("handleSearchAccommodations: %v", err)
	}
	resp := structured.(accommodationSearchResponse)
	if resp.Count != 1 || len(resp.Candidates) != 1 {
		t.Fatalf("response = count %d candidates %#v, want one matched apartment candidate", resp.Count, resp.Candidates)
	}
	if resp.Candidates[0].Name != "Actual Apartment" || resp.Offers[0].PropertyName != "Actual Apartment" {
		t.Fatalf("selected candidate/offer = %#v / %#v, want Actual Apartment", resp.Candidates, resp.Offers)
	}
}

func TestHandleSearchAccommodationsBoundsSlowRoomLookup(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	origSearchHotels := searchHotelsFunc
	origRooms := getRoomAvailabilityWithOptsFunc
	origTimeout := accommodationRoomLookupTimeout
	t.Cleanup(func() {
		searchHotelsFunc = origSearchHotels
		getRoomAvailabilityWithOptsFunc = origRooms
		accommodationRoomLookupTimeout = origTimeout
	})
	accommodationRoomLookupTimeout = 10 * time.Millisecond

	searchHotelsFunc = func(context.Context, string, hotels.HotelSearchOptions) (*models.HotelSearchResult, error) {
		return &models.HotelSearchResult{
			Success: true,
			Count:   1,
			Hotels: []models.HotelResult{{
				Name:            "Slow Detail Hotel",
				HotelID:         "slow-detail-hotel",
				Price:           120,
				Currency:        "EUR",
				PriceBasis:      models.PriceBasisLeadIn,
				PriceConfidence: models.PriceConfidenceUnverified,
			}},
		}, nil
	}
	getRoomAvailabilityWithOptsFunc = func(ctx context.Context, _ hotels.RoomSearchOptions) (*hotels.RoomAvailability, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	start := time.Now()
	_, structured, err := handleSearchAccommodations(context.Background(), map[string]any{
		"location":           "Paris",
		"check_in":           "2026-07-10",
		"check_out":          "2026-07-12",
		"currency":           "eur",
		"max_candidates":     1,
		"include_candidates": true,
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("handleSearchAccommodations: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("elapsed = %s, want bounded room lookup", elapsed)
	}
	resp := structured.(accommodationSearchResponse)
	if resp.Count != 0 || len(resp.Candidates) != 1 {
		t.Fatalf("response = count %d candidates %#v, want no offers and one candidate", resp.Count, resp.Candidates)
	}
	if len(resp.Candidates[0].DetailErrors) != 1 || resp.Candidates[0].DetailErrors[0].Code != "rooms_fetch_failed" {
		t.Fatalf("detail errors = %#v, want rooms_fetch_failed", resp.Candidates[0].DetailErrors)
	}
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
