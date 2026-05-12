package mcp

import (
	"context"
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
				{Name: "Deluxe Room", Price: 160, Currency: "EUR", Provider: "Booking.com"},
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
}
