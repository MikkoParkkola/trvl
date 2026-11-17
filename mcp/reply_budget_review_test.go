package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/pricefeed"
	"github.com/MikkoParkkola/trvl/internal/trip"
)

// Review round 2 (#822): after a cut, every derived field describes the rows
// shown, or says it describes the full result, and never names a cut row.

func stubRoomList(t *testing.T, rooms []hotels.RoomType) {
	t.Helper()
	orig := getRoomAvailabilityWithOptsFunc
	t.Cleanup(func() { getRoomAvailabilityWithOptsFunc = orig })
	getRoomAvailabilityWithOptsFunc = func(_ context.Context, opts hotels.RoomSearchOptions) (*hotels.RoomAvailability, error) {
		return &hotels.RoomAvailability{Success: true, HotelID: opts.HotelID, Name: "Hotel X",
			CheckIn: opts.CheckIn, CheckOut: opts.CheckOut, Rooms: slices.Clone(rooms)}, nil
	}
}

func decodeRooms(t *testing.T, list any) []hotels.RoomType {
	t.Helper()
	raw, _ := json.Marshal(list)
	var rooms []hotels.RoomType
	if err := json.Unmarshal(raw, &rooms); err != nil {
		t.Fatal(err)
	}
	return rooms
}

// The only ready room is the priciest, so the cut removes it and the verdict
// must follow the rooms that remain.
func TestReplyBudgetHotelRoomsReadinessFromShownRooms(t *testing.T) {
	rooms := budgetRooms(80, 1000)
	for i := range rooms {
		rooms[i].Readiness = hotels.ReadinessCaution
	}
	rooms[0].Readiness = hotels.ReadinessReady
	stubRoomList(t, rooms)
	got := callGroundTool(t, "hotel_rooms", budgetRoomArgs)
	if got.isError {
		t.Fatalf("isError: %s", got.errText)
	}
	shown := decodeRooms(t, got.structured["rooms"])
	want := pricefeed.RoomsReadiness(&hotels.RoomAvailability{HotelID: "x", Rooms: shown})
	if r, _ := got.structured["booking_readiness"].(string); r != string(want.Readiness) {
		t.Errorf("booking_readiness = %q for %d shown rooms, want %q from the rooms in the reply", r, len(shown), want.Readiness)
	}
}

// Prices on different bases and currencies: the cheapest stay is a 3-night
// total (EUR 150, EUR 50 a night) and a USD room worth EUR 40 a night, both of
// which a raw-Price comparison ranks as dear.
func TestReplyBudgetHotelRoomsComparesLikeWithLike(t *testing.T) {
	origRate := convertRateFunc
	t.Cleanup(func() { convertRateFunc = origRate })
	convertRateFunc = func(from, to string) (float64, bool) {
		if from == "USD" && to == "EUR" {
			return 0.4, true
		}
		return 0, false
	}
	rooms := budgetRooms(80, 1000)
	for i := range rooms {
		rooms[i].NightlyPrice = rooms[i].Price
	}
	rooms = append(rooms,
		hotels.RoomType{Name: "Stay Total", Price: 150, TotalPrice: 150, Currency: "EUR", Provider: "google", MaxGuests: 2},
		hotels.RoomType{Name: "USD Room", Price: 100, NightlyPrice: 100, Currency: "USD", Provider: "agoda", MaxGuests: 2},
	)
	stubRoomList(t, rooms)
	args := mergeArgs(budgetRoomArgs, map[string]any{"check_out": "2027-03-13"})
	got := callGroundTool(t, "hotel_rooms", args)
	if got.isError {
		t.Fatalf("isError: %s", got.errText)
	}
	names := rowNames(got.structured, "rooms", "name")
	for _, want := range []string{"Stay Total", "USD Room"} {
		if !slices.Contains(names, want) {
			t.Errorf("%s (cheaper per night once compared like with like) was cut; kept %d rooms", want, len(names))
		}
	}
}

// The verified rate's room is the cheapest by comparable price but the
// priciest by raw Price; after any cut verified_rate must name a room still
// listed.
func TestReplyBudgetDetailsVerifiedRateIsShown(t *testing.T) {
	stubHotelSearch(t, budgetHotelFixture(5, 300))
	stubRoomsAndAmenities(t, 80, 300)
	orig := getRoomAvailabilityWithOptsFunc
	getRoomAvailabilityWithOptsFunc = func(ctx context.Context, opts hotels.RoomSearchOptions) (*hotels.RoomAvailability, error) {
		a, err := orig(ctx, opts)
		a.Rooms[0].TotalPrice = 1
		return a, err
	}
	got := callGroundTool(t, "search_hotels_with_details", budgetDetailArgs)
	if got.isError {
		t.Fatalf("isError: %s", got.errText)
	}
	rows, _ := got.structured["hotels"].([]any)
	for i, r := range rows {
		h, _ := r.(map[string]any)
		vr, _ := h["verified_rate"].(map[string]any)
		name, _ := vr["room_name"].(string)
		if rooms := rowNames(h, "room_types", "name"); name != "" && !slices.Contains(rooms, name) {
			t.Errorf("hotel %d verified_rate names %q, which the cut removed", i, name)
		}
	}
}

// Only the last hotel's room lookup fails, and the cut drops that hotel: the
// summary must still disclose the failure.
func TestReplyBudgetDetailsKeepsTrimmedHotelErrors(t *testing.T) {
	stubHotelSearch(t, budgetHotelFixture(5, 9_000))
	stubRoomsAndAmenities(t, 2, 100)
	orig := getRoomAvailabilityWithOptsFunc
	getRoomAvailabilityWithOptsFunc = func(ctx context.Context, opts hotels.RoomSearchOptions) (*hotels.RoomAvailability, error) {
		if opts.HotelID == "h004" {
			return nil, errors.New("rooms unavailable")
		}
		return orig(ctx, opts)
	}
	got := callGroundTool(t, "search_hotels_with_details", budgetDetailArgs)
	if got.isError {
		t.Fatalf("isError: %s", got.errText)
	}
	if rows, _ := got.structured["hotels"].([]any); len(rows) >= 5 {
		t.Fatalf("fixture should force hotels out; kept %d", len(rows))
	}
	if !strings.Contains(got.userText, "partial failure") {
		t.Errorf("summary dropped the cut hotel's failed lookup:\n%s", got.userText)
	}
}

// One hotel with 16 rooms slightly over budget keeps every room that fits,
// not a blanket ten.
func TestReplyBudgetDetailsNoBlanketFirstCut(t *testing.T) {
	stubHotelSearch(t, budgetHotelFixture(1, 300))
	stubRoomsAndAmenities(t, 16, 200)
	got := callGroundTool(t, "search_hotels_with_details", mergeArgs(budgetDetailArgs, map[string]any{"max_hotels": 1}))
	if got.isError {
		t.Fatalf("isError: %s", got.errText)
	}
	assertUnderPromise(t, got)
	rows, _ := got.structured["hotels"].([]any)
	h, _ := rows[0].(map[string]any)
	if n := len(rowNames(h, "room_types", "name")); n <= detailsRoomsFirstCutForTest || n >= 16 {
		t.Errorf("kept %d of 16 rooms; want more than %d (the reply was only slightly over) and fewer than 16", n, detailsRoomsFirstCutForTest)
	}
}

const detailsRoomsFirstCutForTest = 10

// The cheapest and fastest transfer routes sit at the end of the city
// matches; the cut must keep them, and the match counts must describe the
// routes listed.
func TestReplyBudgetTransfersMatchCountsAndLabels(t *testing.T) {
	orig := searchAirportTransfersFunc
	t.Cleanup(func() { searchAirportTransfersFunc = orig })
	searchAirportTransfersFunc = func(context.Context, trip.AirportTransferInput) (*trip.AirportTransferResult, error) {
		routes := make([]models.GroundRoute, 60)
		for i := range routes {
			routes[i] = models.GroundRoute{Provider: "flixbus", Type: "bus", Price: float64(20 + i), Currency: "EUR", Duration: 40 + i,
				Departure: models.GroundStop{City: "Vienna Airport"}, Arrival: models.GroundStop{City: "Vienna"},
				BookingURL: budgetTransferURL(i) + "?pad=" + strings.Repeat("u", 1500)}
		}
		routes[59].Price = 2
		routes[58].Duration = 5
		return &trip.AirportTransferResult{Success: true, AirportCode: "VIE", Airport: "Vienna International", AirportCity: "Vienna",
			Destination: "Vienna", Date: "2027-03-10", Count: 60, ExactMatches: 30, CityMatches: 30, Routes: routes}, nil
	}
	got := callGroundTool(t, "search_airport_transfers", budgetTransferArgs)
	if got.isError {
		t.Fatalf("isError: %s", got.errText)
	}
	keys := transferKeys(got.structured)
	for _, want := range []string{budgetTransferURL(59), budgetTransferURL(58)} {
		if !slices.Contains(keys, want) {
			t.Errorf("%s (the cheapest or fastest route) was cut", want)
		}
	}
	exact, city := bodyInt(got.structured, "exact_matches"), bodyInt(got.structured, "city_matches")
	if exact+city != len(keys) {
		t.Errorf("exact_matches %d + city_matches %d != %d routes listed", exact, city, len(keys))
	}
}
