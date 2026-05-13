package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
)

func TestHandleSearchHotelsWithDetailsEnrichesTopResults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

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
		"location":          "Paris",
		"check_in":          "2026-07-10",
		"check_out":         "2026-07-12",
		"currency":          "eur",
		"max_hotels":        1,
		"include_rooms":     true,
		"include_amenities": true,
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("handleSearchHotelsWithDetails: %v", err)
	}
	if len(content) == 0 {
		t.Fatal("expected summary content")
	}
	if got := content[0].Text; got != "Enriched 1 of 2 hotels in Paris. Found 1 room type." {
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
	t.Setenv("HOME", t.TempDir())

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
		"check_in":          "2026-07-10",
		"check_out":         "2026-07-12",
		"currency":          "EUR",
		"max_hotels":        2,
		"include_rooms":     true,
		"include_amenities": true,
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("handleSearchHotelsWithDetails: %v", err)
	}
	if got := content[0].Text; got != "Enriched 2 of 2 hotels in Paris. Found 1 room type. 2 detail lookups had partial failures." {
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
