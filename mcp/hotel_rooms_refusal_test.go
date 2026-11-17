package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// byNameRefusals mirrors cmd/trvl/rooms_refusal_test.go: the same two by-name
// refusal sentences, so the CLI and MCP are held to the same text (#813).
var byNameRefusals = map[string]error{
	"cooldown": fmt.Errorf("found %q, but Google Hotels refused requests, so its Google place ID is unknown; retry once Google's cooldown ends: %w", "Hotel Sacher Vienna", models.ErrRateLimited),
	"blocked":  fmt.Errorf("found %q, but Google Hotels blocked the request, so its Google place ID is unknown; retry later: %w", "Hotel Sacher Vienna", models.ErrRateLimited),
}

// TestHandleHotelRooms_EmptyRoomsSummaryFollowsTheNotice: with no rooms and a
// Google refusal notice, the summary gives that reason instead of claiming
// Google has no room data for the property (grok, review on 706aff4d).
func TestHandleHotelRooms_EmptyRoomsSummaryFollowsTheNotice(t *testing.T) {
	const notice = "Google Hotels room pricing was temporarily unavailable (rate-limited); a retry may surface more room-level offers."
	origSearch, origRooms := searchHotelByNameFunc, getRoomAvailabilityWithOptsFunc
	t.Cleanup(func() { searchHotelByNameFunc, getRoomAvailabilityWithOptsFunc = origSearch, origRooms })
	searchHotelByNameFunc = func(context.Context, string, string, string, string) (*models.HotelResult, error) {
		return &models.HotelResult{Name: "Hotel Sacher Vienna", HotelID: "ChIJI85wbpwHbUcRiJL_xm_zLE4"}, nil
	}
	getRoomAvailabilityWithOptsFunc = func(_ context.Context, opts hotels.RoomSearchOptions) (*hotels.RoomAvailability, error) {
		return &hotels.RoomAvailability{Success: true, HotelID: opts.HotelID, Name: "Hotel Sacher Vienna", Notice: notice}, nil
	}

	blocks, _, err := handleHotelRooms(context.Background(), map[string]any{
		"hotel_name": "Hotel Sacher Wien", "check_in": "2030-06-15", "check_out": "2030-06-18",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("hotel_rooms: %v", err)
	}
	var text strings.Builder
	for _, b := range blocks {
		text.WriteString(b.Text)
	}
	if got := text.String(); strings.Contains(got, "may not expose") || !strings.Contains(got, notice) {
		t.Fatalf("summary = %q\nwant the notice as the reason, not the no-room-data claim", got)
	}
}

// TestHandleHotelRooms_RefusalSentencesMatchCLI: hotel_rooms returns the
// lookup's refusal sentence unchanged and keeps the rate-limit sentinel.
func TestHandleHotelRooms_RefusalSentencesMatchCLI(t *testing.T) {
	orig := searchHotelByNameFunc
	t.Cleanup(func() { searchHotelByNameFunc = orig })
	for name, refusal := range byNameRefusals {
		t.Run(name, func(t *testing.T) {
			searchHotelByNameFunc = func(context.Context, string, string, string, string) (*models.HotelResult, error) {
				return nil, refusal
			}
			_, _, err := handleHotelRooms(context.Background(), map[string]any{
				"hotel_name": "Hotel Sacher Wien", "check_in": "2030-06-15", "check_out": "2030-06-18",
			}, nil, nil, nil)
			if want := `hotel lookup for "Hotel Sacher Wien": ` + refusal.Error(); err == nil || err.Error() != want {
				t.Fatalf("err = %v\nwant  %s", err, want)
			}
			if !errors.Is(err, models.ErrRateLimited) {
				t.Fatalf("err = %v, want ErrRateLimited kept", err)
			}
		})
	}
}
