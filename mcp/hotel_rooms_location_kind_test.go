package mcp

import (
	"context"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestHandleHotelRooms_LocationKindFollowsTheHint (#812): hotel_rooms marks its
// room-search hint as a hotel query only when that hint is the hotel name; a
// lookup by hotel_id alone leaves the hint empty and its kind unset, so the
// city the entity page supplies is searched as a place.
func TestHandleHotelRooms_LocationKindFollowsTheHint(t *testing.T) {
	const placeID = "ChIJI85wbpwHbUcRiJL_xm_zLE4"
	origSearch, origRooms := searchHotelByNameFunc, getRoomAvailabilityWithOptsFunc
	t.Cleanup(func() { searchHotelByNameFunc, getRoomAvailabilityWithOptsFunc = origSearch, origRooms })
	searchHotelByNameFunc = func(context.Context, string, string, string, string) (*models.HotelResult, error) {
		return &models.HotelResult{Name: "Hotel Sacher Vienna", HotelID: placeID}, nil
	}
	var got hotels.RoomSearchOptions
	getRoomAvailabilityWithOptsFunc = func(_ context.Context, opts hotels.RoomSearchOptions) (*hotels.RoomAvailability, error) {
		got = opts
		return &hotels.RoomAvailability{Success: true, HotelID: opts.HotelID}, nil
	}

	cases := []struct {
		name string
		args map[string]any
		want hotels.LocationKind
	}{
		{"hotel_name", map[string]any{"hotel_name": "Hotel Sacher Wien"}, hotels.LocationHotelQuery},
		{"hotel_id only", map[string]any{"hotel_id": placeID}, hotels.LocationArea},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got = hotels.RoomSearchOptions{}
			tc.args["check_in"], tc.args["check_out"] = "2030-06-15", "2030-06-18"
			if _, _, err := handleHotelRooms(context.Background(), tc.args, nil, nil, nil); err != nil {
				t.Fatalf("hotel_rooms: %v", err)
			}
			if got.LocationKind != tc.want {
				t.Fatalf("LocationKind = %v (Location %q), want %v", got.LocationKind, got.Location, tc.want)
			}
		})
	}
}
