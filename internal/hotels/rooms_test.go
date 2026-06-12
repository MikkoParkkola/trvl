package hotels

import (
	"context"
	"testing"
)

func TestGetRoomAvailability_ParallelBookingFetch(t *testing.T) {
	origFetch := FetchBookingRooms
	FetchBookingRooms = func(ctx context.Context, url, checkIn, checkOut, currency string) ([]RoomType, error) {
		return []RoomType{{
			Name:     "Booking Deluxe Room",
			Price:    120,
			Currency: "EUR",
			Provider: "Booking.com",
		}}, nil
	}
	defer func() { FetchBookingRooms = origFetch }()

	opts := RoomSearchOptions{
		HotelID:    "test-hotel-id",
		CheckIn:    "2026-08-10",
		CheckOut:   "2026-08-17",
		Currency:   "EUR",
		BookingURL: "https://www.booking.com/hotel/test",
	}

	result, err := GetRoomAvailabilityWithOpts(context.Background(), opts)
	if err != nil {
		t.Fatalf("GetRoomAvailabilityWithOpts failed: %v", err)
	}

	foundBooking := false
	for _, r := range result.Rooms {
		if r.Provider == "Booking.com" {
			foundBooking = true
			break
		}
	}
	if !foundBooking {
		t.Error("expected Booking.com room in results")
	}
}

func TestGetRoomAvailability_SkipsBookingWhenNoURL(t *testing.T) {
	origFetch := FetchBookingRooms
	callCount := 0
	FetchBookingRooms = func(ctx context.Context, url, checkIn, checkOut, currency string) ([]RoomType, error) {
		callCount++
		return nil, nil
	}
	defer func() { FetchBookingRooms = origFetch }()

	opts := RoomSearchOptions{
		HotelID:  "test-hotel-id",
		CheckIn:  "2026-08-10",
		CheckOut: "2026-08-17",
		Currency: "EUR",
	}

	_, err := GetRoomAvailabilityWithOpts(context.Background(), opts)
	if err != nil {
		t.Fatalf("GetRoomAvailabilityWithOpts failed: %v", err)
	}

	if callCount > 0 {
		t.Error("expected FetchBookingRooms NOT to be called when no BookingURL")
	}
}

func TestRoomFallbackSearchOptionsUsesRequestedGuests(t *testing.T) {
	opts := RoomSearchOptions{
		HotelID:      "test-hotel-id",
		CheckIn:      "2026-08-10",
		CheckOut:     "2026-08-17",
		Currency:     "EUR",
		Guests:       4,
		ChildrenAges: []int{7, 10},
		Rooms:        2,
	}

	got := roomFallbackSearchOptions(opts)
	if got.Guests != 4 {
		t.Fatalf("fallback Guests = %d, want requested guests 4", got.Guests)
	}
	if len(got.ChildrenAges) != 2 || got.ChildrenAges[0] != 7 || got.ChildrenAges[1] != 10 {
		t.Fatalf("fallback ChildrenAges = %v, want [7 10]", got.ChildrenAges)
	}
	if got.Rooms != 2 {
		t.Fatalf("fallback Rooms = %d, want requested rooms 2", got.Rooms)
	}
	if got.CheckIn != opts.CheckIn || got.CheckOut != opts.CheckOut || got.Currency != opts.Currency {
		t.Fatalf("fallback dates/currency = %s/%s/%s, want %s/%s/%s",
			got.CheckIn, got.CheckOut, got.Currency, opts.CheckIn, opts.CheckOut, opts.Currency)
	}
}

func TestRoomFallbackSearchOptionsDefaultsGuests(t *testing.T) {
	got := roomFallbackSearchOptions(RoomSearchOptions{
		HotelID:  "test-hotel-id",
		CheckIn:  "2026-08-10",
		CheckOut: "2026-08-17",
		Currency: "EUR",
	})
	if got.Guests != 2 {
		t.Fatalf("fallback Guests = %d, want default guests 2", got.Guests)
	}
}
