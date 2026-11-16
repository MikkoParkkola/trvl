package main

import (
	"context"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// stubRoomsLookup answers the name lookup with hotel and records the room
// fetch options; fetched stays nil when the room fetch never ran.
func stubRoomsLookup(t *testing.T, hotel models.HotelResult) **hotels.RoomSearchOptions {
	t.Helper()
	origSearch, origRooms := searchHotelByName, getRoomAvailabilityWithOpts
	t.Cleanup(func() { searchHotelByName, getRoomAvailabilityWithOpts = origSearch, origRooms })
	searchHotelByName = func(context.Context, string, string, string, string) (*models.HotelResult, error) {
		return &hotel, nil
	}
	var fetched *hotels.RoomSearchOptions
	getRoomAvailabilityWithOpts = func(_ context.Context, opts hotels.RoomSearchOptions) (*hotels.RoomAvailability, error) {
		fetched = &opts
		return &hotels.RoomAvailability{Success: true, HotelID: opts.HotelID}, nil
	}
	return &fetched
}

// TestResolveRoomAvailability_RefusesBookingID: a Booking.com id is not a
// Google place ID, so the room fetch must not be sent it as one.
func TestResolveRoomAvailability_RefusesBookingID(t *testing.T) {
	fetched := stubRoomsLookup(t, models.HotelResult{Name: "Hotel Sacher Wien", HotelID: "4711"})

	if _, err := resolveRoomAvailability(context.Background(), "Hotel Sacher Wien", "2026-11-18", "2026-11-20", "EUR", ""); err == nil {
		t.Fatal("resolved rooms for a Booking-only match, want an error naming the missing Google ID")
	}
	if *fetched != nil {
		t.Fatalf("room fetch ran with HotelID %q, want no fetch", (*fetched).HotelID)
	}
}

// TestResolveRoomAvailability_BookingURLFromBookingSourceOnly: the room fetch
// gets the Booking.com page from the booking source, never the display link
// on HotelResult.BookingURL, and nothing when the source is not booking.com.
func TestResolveRoomAvailability_BookingURLFromBookingSourceOnly(t *testing.T) {
	const page = "https://www.booking.com/hotel/at/sacher.html"
	cases := []struct {
		name    string
		hotel   models.HotelResult
		wantURL string
	}{
		{"booking source", models.HotelResult{
			BookingURL: "https://www.google.com/travel/hotels/Wien",
			Sources: []models.PriceSource{
				{Provider: "google_hotels", BookingURL: "https://www.google.com/travel/hotels/Wien"},
				{Provider: "booking", BookingURL: page},
			},
		}, page},
		{"display link only", models.HotelResult{BookingURL: page}, ""},
		{"booking source off booking.com", models.HotelResult{
			Sources: []models.PriceSource{{Provider: "booking", BookingURL: "https://booking.example/sacher"}},
		}, ""},
	}
	for _, tc := range cases {
		tc.hotel.Name, tc.hotel.HotelID = "Hotel Sacher Vienna", "ChIJsacher"
		fetched := stubRoomsLookup(t, tc.hotel)
		if _, err := resolveRoomAvailability(context.Background(), "Hotel Sacher Wien", "2026-11-18", "2026-11-20", "EUR", ""); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if *fetched == nil || (*fetched).BookingURL != tc.wantURL {
			t.Errorf("%s: room fetch options = %+v, want BookingURL %q", tc.name, *fetched, tc.wantURL)
		}
	}
}

// TestResolveRoomAvailability_ProviderColonIDIsNotGoogle: "flatio:98765"
// typed in is a name to look up, not a Google ID, and "agoda:123" from the
// name lookup is refused like any other non-Google id.
func TestResolveRoomAvailability_ProviderColonIDIsNotGoogle(t *testing.T) {
	fetched := stubRoomsLookup(t, models.HotelResult{Name: "Hotel Sacher Vienna", HotelID: "ChIJsacher"})
	if _, err := resolveRoomAvailability(context.Background(), "flatio:98765", "2026-11-18", "2026-11-20", "EUR", ""); err != nil {
		t.Fatal(err)
	}
	if *fetched == nil || (*fetched).HotelID != "ChIJsacher" {
		t.Errorf("room fetch = %+v, want the name lookup's ChIJsacher, not flatio:98765 as a Google ID", *fetched)
	}

	fetched = stubRoomsLookup(t, models.HotelResult{Name: "Hotel Sacher Wien", HotelID: "agoda:123"})
	if _, err := resolveRoomAvailability(context.Background(), "Hotel Sacher Wien", "2026-11-18", "2026-11-20", "EUR", ""); err == nil {
		t.Error("resolved rooms for an agoda:123 match, want an error naming the missing Google ID")
	}
	if *fetched != nil {
		t.Errorf("room fetch ran with HotelID %q, want no fetch", (*fetched).HotelID)
	}
}
