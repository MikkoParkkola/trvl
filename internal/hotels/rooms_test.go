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
