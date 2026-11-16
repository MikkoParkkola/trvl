package mcp

import (
	"context"
	"sync"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// stubAccommodationRooms records the BookingURL each room fetch received, by
// hotel ID, and answers every search with found.
func stubAccommodationRooms(t *testing.T, found []models.HotelResult) map[string]string {
	t.Helper()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)
	origSearch, origRooms := searchHotelsFunc, getRoomAvailabilityWithOptsFunc
	t.Cleanup(func() { searchHotelsFunc, getRoomAvailabilityWithOptsFunc = origSearch, origRooms })
	searchHotelsFunc = func(context.Context, string, hotels.HotelSearchOptions) (*models.HotelSearchResult, error) {
		return &models.HotelSearchResult{Success: true, Count: len(found), Hotels: found}, nil
	}
	var mu sync.Mutex
	got := map[string]string{}
	getRoomAvailabilityWithOptsFunc = func(_ context.Context, opts hotels.RoomSearchOptions) (*hotels.RoomAvailability, error) {
		mu.Lock()
		got[opts.HotelID] = opts.BookingURL
		mu.Unlock()
		return &hotels.RoomAvailability{Success: true, HotelID: opts.HotelID}, nil
	}
	return got
}

// TestSearchAccommodations_RoomFetchGetsBookingSourcePageOnly: the room fetch
// refuses anything but an https booking.com page, so the search path passes
// only the booking source's page: never an http one, never a display link.
func TestSearchAccommodations_RoomFetchGetsBookingSourcePageOnly(t *testing.T) {
	got := stubAccommodationRooms(t, []models.HotelResult{
		{Name: "Hotel Central", HotelID: "ChIJcentral", Price: 200, Currency: "EUR", Sources: []models.PriceSource{
			{Provider: "booking", BookingURL: "http://www.booking.com/hotel/fr/central.html", Price: 200, Currency: "EUR"},
		}},
		{Name: "Hotel Display", HotelID: "ChIJdisplay", Price: 180, Currency: "EUR",
			BookingURL: "https://www.booking.com/searchresults.html?ss=Paris"},
	})
	if _, _, err := handleSearchAccommodations(context.Background(), map[string]any{
		"location": "Paris", "check_in": futureDateAfter(30), "check_out": futureDateAfter(32),
		"adults": 2, "currency": "eur",
	}, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"ChIJcentral", "ChIJdisplay"} {
		if url, ran := got[id]; !ran || url != "" {
			t.Errorf("room fetch for %s ran=%v with BookingURL %q, want a run without one", id, ran, url)
		}
	}
}

// TestReverifyAccommodation_KeepsOnlyABookingCallerURL: in reverify the
// caller's booking_url is the hotel's page and stays when it is an https
// booking.com URL; any other is dropped so the Google fetch still runs.
func TestReverifyAccommodation_KeepsOnlyABookingCallerURL(t *testing.T) {
	cases := map[string]string{
		"https://www.booking.com/hotel/fr/central.html": "https://www.booking.com/hotel/fr/central.html",
		"https://evil.example/hotel":                    "",
	}
	for callerURL, want := range cases {
		got := stubAccommodationRooms(t, nil)
		if _, _, err := handleSearchAccommodations(context.Background(), map[string]any{
			"location": "Paris", "check_in": futureDateAfter(30), "check_out": futureDateAfter(32),
			"adults": 2, "currency": "eur", "reverify_only": true, "hotel_id": "ChIJcentral", "booking_url": callerURL,
		}, nil, nil, nil); err != nil {
			t.Fatalf("%s: %v", callerURL, err)
		}
		if url, ran := got["ChIJcentral"]; !ran || url != want {
			t.Errorf("%s: room fetch ran=%v with BookingURL %q, want %q", callerURL, ran, url, want)
		}
	}
}

// TestAccommodationHotelIDIsGooglePlaceID_SharesTheStrictRule: the
// accommodation check accepts exactly models.IsGoogleHotelID's shapes.
func TestAccommodationHotelIDIsGooglePlaceID_SharesTheStrictRule(t *testing.T) {
	for id, want := range map[string]bool{
		"0x476d079e5136ca9f:0xfdc2e58a51a25b46": true,
		"ChIJI85wbpwHbUcRiJL_xm_zLE4":           true,
		"agoda:123":                             false,
		"/g/":                                   false,
	} {
		if got := accommodationHotelIDIsGooglePlaceID(models.HotelResult{HotelID: id}); got != want {
			t.Errorf("accommodationHotelIDIsGooglePlaceID(%q) = %v, want %v", id, got, want)
		}
	}
}
