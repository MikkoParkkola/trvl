package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// runRoomsWithLookupErr runs `trvl rooms "Hotel Sacher Wien"` with the name
// lookup failing with lookupErr and returns the command's error.
func runRoomsWithLookupErr(t *testing.T, lookupErr error) error {
	t.Helper()
	orig := searchHotelByName
	t.Cleanup(func() { searchHotelByName = orig })
	searchHotelByName = func(context.Context, string, string, string, string) (*models.HotelResult, error) {
		return nil, lookupErr
	}
	cmd := roomsCmd()
	cmd.SetArgs([]string{"Hotel Sacher Wien", "--checkin", "2030-06-15", "--checkout", "2030-06-18"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	return cmd.Execute()
}

// TestRunRooms_RateLimitIsNotBlamedOnTheIndex: a Google refusal is reported
// as itself, without the hint that the hotel is missing from Google's index.
func TestRunRooms_RateLimitIsNotBlamedOnTheIndex(t *testing.T) {
	err := runRoomsWithLookupErr(t, fmt.Errorf("found Hotel Sacher Vienna, but Google Hotels refused requests: %w", models.ErrRateLimited))
	if err == nil {
		t.Fatal("want an error")
	}
	if strings.Contains(err.Error(), "Smaller or island properties") {
		t.Fatalf("err = %v\nwant no index hint for a rate limit", err)
	}
	if !errors.Is(err, models.ErrRateLimited) {
		t.Fatalf("err = %v, want it to keep ErrRateLimited", err)
	}
}

// TestRunRooms_NoMatchKeepsTheIndexHint (preservation): a real miss still
// points at the paths that work for properties Google does not index.
func TestRunRooms_NoMatchKeepsTheIndexHint(t *testing.T) {
	err := runRoomsWithLookupErr(t, fmt.Errorf("%w: %q", hotels.ErrNoNameMatch, "Hotel Sacher Wien"))
	if err == nil || !strings.Contains(err.Error(), "Smaller or island properties") {
		t.Fatalf("err = %v, want the index hint", err)
	}
}

// TestResolveRoomAvailability_LocationFlagReachesTheRoomHint (#812): the city
// the user passes with --location must reach the room lookup as a place, even
// when the hotel's name already contains it.
func TestResolveRoomAvailability_LocationFlagReachesTheRoomHint(t *testing.T) {
	fetched := stubRoomsLookup(t, models.HotelResult{Name: "Hotel Sacher Vienna", HotelID: "ChIJsacher"})
	if _, err := resolveRoomAvailability(context.Background(), "Hotel Sacher Wien", "2030-06-15", "2030-06-18", "EUR", "Wien"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	got := *fetched
	if got == nil {
		t.Fatal("room fetch never ran")
	}
	if !strings.HasSuffix(got.Location, ", Wien") || got.LocationKind != hotels.LocationHotelQuery {
		t.Fatalf("room hint = %q (kind %v), want the hotel query ending in \", Wien\"", got.Location, got.LocationKind)
	}
}
