package mcp

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/hotels"
)

var budgetDetailArgs = mergeArgs(budgetHotelArgs, map[string]any{"max_hotels": 5, "currency": "EUR"})

func budgetRoomName(i int) string { return fmt.Sprintf("Room %02d", i) }

// budgetRooms returns n rooms listed priciest first, so the cheapest room is
// the last one a naive tail cut would drop.
func budgetRooms(n, pad int) []hotels.RoomType {
	rooms := make([]hotels.RoomType, n)
	for i := range rooms {
		rooms[i] = hotels.RoomType{
			Name:        budgetRoomName(i),
			Price:       float64(60 + n - 1 - i),
			Currency:    "EUR",
			Provider:    "booking",
			ProviderURL: "https://www.booking.com/hotel/at/x.html",
			MaxGuests:   2,
			Description: strings.Repeat("r", pad),
		}
	}
	return rooms
}

func stubRoomsAndAmenities(t *testing.T, n, pad int) {
	t.Helper()
	origRooms, origAmenities := getRoomAvailabilityWithOptsFunc, fetchHotelAmenitiesFunc
	t.Cleanup(func() { getRoomAvailabilityWithOptsFunc, fetchHotelAmenitiesFunc = origRooms, origAmenities })
	getRoomAvailabilityWithOptsFunc = func(_ context.Context, opts hotels.RoomSearchOptions) (*hotels.RoomAvailability, error) {
		return &hotels.RoomAvailability{Success: true, HotelID: opts.HotelID, Name: "Hotel " + opts.HotelID,
			CheckIn: opts.CheckIn, CheckOut: opts.CheckOut, Rooms: budgetRooms(n, pad)}, nil
	}
	fetchHotelAmenitiesFunc = func(context.Context, string) ([]string, error) { return []string{"wifi"}, nil }
}

func TestReplyBudgetHotelDetails(t *testing.T) {
	stubHotelSearch(t, budgetHotelFixture(5, 300))
	stubRoomsAndAmenities(t, 80, 300)
	got := callGroundTool(t, "search_hotels_with_details", budgetDetailArgs)
	if got.isError {
		t.Fatalf("isError: %s", got.errText)
	}
	assertUnderPromise(t, got)
	rows, _ := got.structured["hotels"].([]any)
	if len(rows) == 0 {
		t.Fatal("no hotels shown")
	}
	if o := bodyInt(got.structured, "budget_omitted"); o != 5-len(rows) {
		t.Errorf("budget_omitted = %d, want %d", o, 5-len(rows))
	}
	cheapest := budgetRoomName(79)
	for i, r := range rows {
		h, _ := r.(map[string]any)
		rooms := rowNames(h, "room_types", "name")
		if !slices.Contains(rooms, cheapest) {
			t.Errorf("hotel %d lost its cheapest room %s", i, cheapest)
		}
		if o := bodyInt(h, "rooms_omitted"); o != 80-len(rooms) {
			t.Errorf("hotel %d rooms_omitted = %d, want %d", i, o, 80-len(rooms))
		}
		for _, offer := range rowNames(h, "accommodation_offers", "room_name") {
			if !slices.Contains(rooms, offer) {
				t.Errorf("hotel %d offer %q names a room the cut removed from room_types", i, offer)
			}
		}
	}
	if !strings.Contains(got.userText, "50 KB") {
		t.Errorf("summary should say the 50 KB budget cut rooms:\n%s", got.userText)
	}
}

// TestReplyBudgetHotelDetailsFloor: one room alone takes most of the budget,
// so the floor (one hotel, its cheapest room) is what ships.
func TestReplyBudgetHotelDetailsFloor(t *testing.T) {
	stubHotelSearch(t, budgetHotelFixture(5, 300))
	stubRoomsAndAmenities(t, 10, 12_000)
	got := callGroundTool(t, "search_hotels_with_details", budgetDetailArgs)
	if got.isError {
		t.Fatalf("isError: %s", got.errText)
	}
	assertUnderPromise(t, got)
	rows, _ := got.structured["hotels"].([]any)
	if len(rows) != 1 {
		t.Fatalf("floor kept %d hotels, want 1", len(rows))
	}
	h, _ := rows[0].(map[string]any)
	if rooms := rowNames(h, "room_types", "name"); len(rooms) != 1 || rooms[0] != budgetRoomName(9) {
		t.Errorf("floor rooms = %v, want only the cheapest %s", rooms, budgetRoomName(9))
	}
}

func TestReplyBudgetHotelDetailsFloorOverBudget(t *testing.T) {
	stubHotelSearch(t, budgetHotelFixture(5, 30_000))
	stubRoomsAndAmenities(t, 3, 100)
	assertBoundedFloorError(t, callGroundTool(t, "search_hotels_with_details", budgetDetailArgs))
}

var budgetRoomArgs = map[string]any{"hotel_id": "0x476d07b021f90d3f:0x557f8390f490f2f4", "check_in": "2027-03-10", "check_out": "2027-03-11", "currency": "EUR"}

func TestReplyBudgetHotelRooms(t *testing.T) {
	stubRoomsAndAmenities(t, 80, 1000)
	got := callGroundTool(t, "hotel_rooms", budgetRoomArgs)
	if got.isError {
		t.Fatalf("isError: %s", got.errText)
	}
	assertUnderPromise(t, got)
	rooms := rowNames(got.structured, "rooms", "name")
	if len(rooms) == 0 || len(rooms) >= 80 {
		t.Fatalf("rooms shown = %d, want a cut between 1 and 79", len(rooms))
	}
	if !slices.Contains(rooms, budgetRoomName(79)) {
		t.Errorf("the cheapest room (named by the summary) was cut")
	}
	if o := bodyInt(got.structured, "budget_omitted"); o != 80-len(rooms) {
		t.Errorf("budget_omitted = %d, want %d", o, 80-len(rooms))
	}
	if !strings.Contains(got.userText, fmt.Sprintf("Showing %d of 80 rooms", len(rooms))) {
		t.Errorf("summary should say Showing %d of 80 rooms:\n%s", len(rooms), got.userText)
	}
	assertTextCopyMatches(t, got, "rooms", "name")
}

func TestReplyBudgetHotelRoomsFloor(t *testing.T) {
	stubRoomsAndAmenities(t, 10, 15_000)
	got := callGroundTool(t, "hotel_rooms", budgetRoomArgs)
	if got.isError {
		t.Fatalf("isError: %s", got.errText)
	}
	assertUnderPromise(t, got)
	if rooms := rowNames(got.structured, "rooms", "name"); len(rooms) != 1 || rooms[0] != budgetRoomName(9) {
		t.Errorf("floor rooms = %v, want only the cheapest", rooms)
	}
}

func TestReplyBudgetHotelRoomsFloorOverBudget(t *testing.T) {
	stubRoomsAndAmenities(t, 3, 30_000)
	assertBoundedFloorError(t, callGroundTool(t, "hotel_rooms", budgetRoomArgs))
}
