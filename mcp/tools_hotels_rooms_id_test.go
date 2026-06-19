package mcp

import (
	"context"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// Regression tests for GH #187 / #188 Bug 3(a): hotel_rooms must honor a
// supplied hotel_id directly and NOT re-run the fuzzy hotel-name search, which
// can resolve to a different property and fetch room data for the wrong hotel.

func TestHandleHotelRooms_HonorsSuppliedHotelID(t *testing.T) {
	origSearch := searchHotelByNameFunc
	origRooms := getRoomAvailabilityWithOptsFunc
	defer func() { searchHotelByNameFunc = origSearch; getRoomAvailabilityWithOptsFunc = origRooms }()

	searchCalled := false
	searchHotelByNameFunc = func(_ context.Context, _, _, _, _ string) (*models.HotelResult, error) {
		searchCalled = true
		return &models.HotelResult{Name: "WRONG Hotel", HotelID: "wrong-id"}, nil
	}
	var gotID string
	getRoomAvailabilityWithOptsFunc = func(_ context.Context, opts hotels.RoomSearchOptions) (*hotels.RoomAvailability, error) {
		gotID = opts.HotelID
		return &hotels.RoomAvailability{Success: true, HotelID: opts.HotelID, Name: "Right Hotel"}, nil
	}

	_, _, err := handleHotelRooms(context.Background(), map[string]any{
		"hotel_id":  "/g/right-id",
		"check_in":  "2099-06-18",
		"check_out": "2099-06-20",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("handleHotelRooms: %v", err)
	}
	if searchCalled {
		t.Error("must NOT call SearchHotelByName when hotel_id is supplied")
	}
	if gotID != "/g/right-id" {
		t.Errorf("room fetch HotelID = %q, want /g/right-id", gotID)
	}
}

func TestHandleHotelRooms_FallsBackToNameSearch(t *testing.T) {
	origSearch := searchHotelByNameFunc
	origRooms := getRoomAvailabilityWithOptsFunc
	defer func() { searchHotelByNameFunc = origSearch; getRoomAvailabilityWithOptsFunc = origRooms }()

	searchCalled := false
	searchHotelByNameFunc = func(_ context.Context, name, _, _, _ string) (*models.HotelResult, error) {
		searchCalled = true
		if name != "Hotel Lutetia, Paris" {
			t.Errorf("name search query = %q", name)
		}
		return &models.HotelResult{Name: "Hotel Lutetia", HotelID: "/g/lutetia", BookingURL: "https://booking.example/lutetia"}, nil
	}
	var gotID, gotBookingURL string
	getRoomAvailabilityWithOptsFunc = func(_ context.Context, opts hotels.RoomSearchOptions) (*hotels.RoomAvailability, error) {
		gotID = opts.HotelID
		gotBookingURL = opts.BookingURL
		return &hotels.RoomAvailability{Success: true, HotelID: opts.HotelID, Name: "Hotel Lutetia"}, nil
	}

	_, _, err := handleHotelRooms(context.Background(), map[string]any{
		"hotel_name": "Hotel Lutetia, Paris",
		"check_in":   "2099-06-18",
		"check_out":  "2099-06-20",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("handleHotelRooms: %v", err)
	}
	if !searchCalled {
		t.Error("expected SearchHotelByName to run when only hotel_name is supplied")
	}
	if gotID != "/g/lutetia" {
		t.Errorf("room fetch HotelID = %q, want /g/lutetia", gotID)
	}
	// Booking URL from the search result should propagate when caller omits it.
	if gotBookingURL != "https://booking.example/lutetia" {
		t.Errorf("BookingURL = %q, want propagated search-result URL", gotBookingURL)
	}
}

func TestHandleHotelRooms_RequiresIDOrName(t *testing.T) {
	_, _, err := handleHotelRooms(context.Background(), map[string]any{
		"check_in":  "2099-06-18",
		"check_out": "2099-06-20",
	}, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error when neither hotel_id nor hotel_name is supplied")
	}
}
