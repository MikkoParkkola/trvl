package mcp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// goldenRefusal is one of the real by-name refusal sentences, read from the
// golden file the hotels package checks against SearchHotelByName's actual
// output, and it keeps the rate-limit sentinel as that error does (#813).
type goldenRefusal string

func (g goldenRefusal) Error() string { return string(g) }
func (g goldenRefusal) Unwrap() error { return models.ErrRateLimited }

// byNameRefusals loads the cooldown and blocked sentences; the CLI and MCP
// tests read the same file, so both are held to the same real text.
func byNameRefusals(t *testing.T) map[string]error {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "internal", "hotels", "testdata", "byname_refusals.golden"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]error{}
	for _, line := range strings.Split(strings.TrimSpace(strings.ReplaceAll(string(raw), "\r", "")), "\n") {
		name, sentence, ok := strings.Cut(line, ": ")
		if !ok {
			t.Fatalf("malformed golden line %q", line)
		}
		out[name] = goldenRefusal(sentence)
	}
	return out
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
	summary := ""
	for _, b := range blocks {
		text.WriteString(b.Text)
		if strings.Contains(b.Text, "No individual room types found") {
			summary = b.Text
		}
	}
	if strings.Contains(text.String(), "may not expose") || !strings.Contains(text.String(), notice) {
		t.Fatalf("reply = %q\nwant the notice as the reason, not the no-room-data claim", text.String())
	}
	// The notice travels in the reply once; the summary points to it instead
	// of repeating it (muse, review on 738ac158).
	if summary == "" || strings.Contains(summary, notice) || !strings.Contains(summary, "notice") {
		t.Fatalf("summary = %q\nwant a short reason that points to the notice", summary)
	}
}

// TestHandleHotelRooms_RefusalSentencesMatchCLI: hotel_rooms returns the
// lookup's refusal sentence unchanged and keeps the rate-limit sentinel.
func TestHandleHotelRooms_RefusalSentencesMatchCLI(t *testing.T) {
	orig := searchHotelByNameFunc
	t.Cleanup(func() { searchHotelByNameFunc = orig })
	for name, refusal := range byNameRefusals(t) {
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
