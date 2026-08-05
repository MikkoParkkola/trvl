package mcp

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestAccommodationCandidateLimitBounds pins the rate-limit guard that decides
// how many properties get a per-hotel room-level lookup. Each candidate costs
// one network round-trip to the provider, so this clamp is the ceiling on
// upstream calls per search (issue #290, MIK-277G.2).
func TestAccommodationCandidateLimitBounds(t *testing.T) {
	cases := []struct {
		name      string
		requested int
		available int
		want      int
	}{
		{"default when unset", 0, 20, 5},
		{"negative coerced to default", -3, 20, 5},
		{"honours explicit request", 3, 20, 3},
		{"hard cap at 8", 50, 20, 8},
		{"cap applies even at the boundary", 9, 20, 8},
		{"clamped to available", 8, 4, 4},
		{"no hotels means no lookups", 5, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := accommodationCandidateLimit(tc.requested, tc.available); got != tc.want {
				t.Fatalf("accommodationCandidateLimit(%d, %d) = %d, want %d", tc.requested, tc.available, got, tc.want)
			}
		})
	}
}

// TestHandleSearchAccommodationsCapsRoomLookupCallCount proves the clamp holds
// end to end: regardless of how many properties the discovery search returns or
// how high the caller sets max_candidates, the handler never issues more than
// the capped number of room-level lookups. This is the rate-limit-safety
// guarantee in #290's title.
func TestHandleSearchAccommodationsCapsRoomLookupCallCount(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)

	origSearchHotels := searchHotelsFunc
	origRooms := getRoomAvailabilityWithOptsFunc
	t.Cleanup(func() {
		searchHotelsFunc = origSearchHotels
		getRoomAvailabilityWithOptsFunc = origRooms
	})

	// 20 Google properties, each carrying a hotel_id and no provider room
	// inventory, so every selected candidate must do a live room lookup.
	const available = 20
	searchHotelsFunc = func(_ context.Context, _ string, _ hotels.HotelSearchOptions) (*models.HotelSearchResult, error) {
		out := make([]models.HotelResult, available)
		for i := range out {
			out[i] = models.HotelResult{
				Name:         fmt.Sprintf("Hotel %02d", i),
				HotelID:      fmt.Sprintf("/g/11test%02d", i),
				Price:        100 + float64(i),
				Currency:     "EUR",
				PropertyType: "hotel",
			}
		}
		return &models.HotelSearchResult{Success: true, Count: available, Hotels: out}, nil
	}

	run := func(t *testing.T, maxCandidates int, wantCap int) {
		var calls atomic.Int64
		getRoomAvailabilityWithOptsFunc = func(_ context.Context, _ hotels.RoomSearchOptions) (*hotels.RoomAvailability, error) {
			calls.Add(1)
			return &hotels.RoomAvailability{}, nil
		}
		args := map[string]any{
			"location":  "Paris",
			"check_in":  "2026-07-10",
			"check_out": "2026-07-12",
			"adults":    2,
			"currency":  "eur",
		}
		if maxCandidates > 0 {
			args["max_candidates"] = maxCandidates
		}
		if _, _, err := handleSearchAccommodations(context.Background(), args, nil, nil, nil); err != nil {
			t.Fatalf("handleSearchAccommodations: %v", err)
		}
		if got := int(calls.Load()); got != wantCap {
			t.Fatalf("room lookups = %d, want %d (cap must bound calls)", got, wantCap)
		}
	}

	t.Run("oversized request is capped at 8", func(t *testing.T) { run(t, 50, 8) })
	t.Run("default request is capped at 5", func(t *testing.T) { run(t, 0, 5) })
}
