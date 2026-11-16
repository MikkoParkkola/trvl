package mcp

import (
	"context"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// stubNameThenRooms answers the name lookup with hotel and records the room
// fetch options; the returned pointer stays nil when no fetch ran.
func stubNameThenRooms(t *testing.T, hotel models.HotelResult) **hotels.RoomSearchOptions {
	t.Helper()
	origSearch, origRooms := searchHotelByNameFunc, getRoomAvailabilityWithOptsFunc
	t.Cleanup(func() { searchHotelByNameFunc, getRoomAvailabilityWithOptsFunc = origSearch, origRooms })
	searchHotelByNameFunc = func(context.Context, string, string, string, string) (*models.HotelResult, error) {
		return &hotel, nil
	}
	var fetched *hotels.RoomSearchOptions
	getRoomAvailabilityWithOptsFunc = func(_ context.Context, opts hotels.RoomSearchOptions) (*hotels.RoomAvailability, error) {
		fetched = &opts
		return &hotels.RoomAvailability{Success: true, HotelID: opts.HotelID, Name: hotel.Name}, nil
	}
	return &fetched
}

func roomsByName(name string) map[string]any {
	return map[string]any{"hotel_name": name, "check_in": "2099-11-18", "check_out": "2099-11-20"}
}

// TestHandleHotelRooms_RefusesBookingID: a Booking.com id from the name
// lookup is not a Google place ID and must not reach the room fetch as one.
func TestHandleHotelRooms_RefusesBookingID(t *testing.T) {
	fetched := stubNameThenRooms(t, models.HotelResult{Name: "Hotel Sacher Wien", HotelID: "4711"})
	if _, _, err := handleHotelRooms(context.Background(), roomsByName("Hotel Sacher Wien"), nil, nil, nil); err == nil {
		t.Fatal("hotel_rooms resolved a Booking-only match, want an error naming the missing Google ID")
	}
	if *fetched != nil {
		t.Fatalf("room fetch ran with HotelID %q, want no fetch", (*fetched).HotelID)
	}
}

// TestResolveRoomAvailabilityForWatch_RefusesBookingID is the watch checker's
// half of the same rule.
func TestResolveRoomAvailabilityForWatch_RefusesBookingID(t *testing.T) {
	fetched := stubNameThenRooms(t, models.HotelResult{Name: "Hotel Sacher Wien", HotelID: "4711"})
	if _, err := resolveRoomAvailabilityForWatch(context.Background(), "Hotel Sacher Wien", "2099-11-18", "2099-11-20", "EUR"); err == nil {
		t.Fatal("watch resolved a Booking-only match, want an error naming the missing Google ID")
	}
	if *fetched != nil {
		t.Fatalf("room fetch ran with HotelID %q, want no fetch", (*fetched).HotelID)
	}
}

// TestHandleHotelRooms_BookingURLFromBookingSourceOnly: the display link on
// HotelResult.BookingURL is never a room-fetch target, even on booking.com.
func TestHandleHotelRooms_BookingURLFromBookingSourceOnly(t *testing.T) {
	fetched := stubNameThenRooms(t, models.HotelResult{
		Name: "Hotel Sacher Vienna", HotelID: "ChIJsacher",
		BookingURL: "https://www.booking.com/searchresults.html?ss=Wien",
	})
	if _, _, err := handleHotelRooms(context.Background(), roomsByName("Hotel Sacher Wien"), nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if *fetched == nil || (*fetched).BookingURL != "" {
		t.Fatalf("room fetch options = %+v, want no BookingURL", *fetched)
	}
}
