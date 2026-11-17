package mcp

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// byNameRefusals mirrors cmd/trvl/rooms_refusal_test.go: the same two by-name
// refusal sentences, so the CLI and MCP are held to the same text (#813).
var byNameRefusals = map[string]error{
	"cooldown": fmt.Errorf("found %q, but Google Hotels refused requests, so its Google place ID is unknown; retry once Google's cooldown ends: %w", "Hotel Sacher Vienna", models.ErrRateLimited),
	"blocked":  fmt.Errorf("found %q, but Google Hotels blocked the request, so its Google place ID is unknown; retry later: %w", "Hotel Sacher Vienna", models.ErrRateLimited),
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
