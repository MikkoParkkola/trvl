package hotels

import (
	"context"
	"errors"
	"testing"
)

// A refused destination and a hotel with no rooms used to look identical from
// the outside: the refusal was logged at debug level and the lookup returned a
// successful, empty result. A caller (an MCP client, the CLI) could not tell
// "the URL you handed me can never work" from "this hotel has nothing", so it
// would keep re-sending the same unusable URL and report an empty room list to
// the user.
//
// Both cases run with an already-cancelled context so no provider does any
// network I/O: the destination pin runs before the first request, and every
// other path fails fast at the rate limiter.
func TestGetRoomAvailabilityWithOpts_SurfacesRefusedBookingURL(t *testing.T) {
	// NOTE: deliberately no allowLocalBookingHost here. This must exercise the
	// production destination pin.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	base := RoomSearchOptions{
		HotelID:  "ChIJtest",
		CheckIn:  "2026-08-01",
		CheckOut: "2026-08-03",
		Currency: "EUR",
	}

	t.Run("refused destination is an error, not an empty room list", func(t *testing.T) {
		opts := base
		opts.BookingURL = "http://169.254.169.254/latest/meta-data/"

		av, err := GetRoomAvailabilityWithOpts(ctx, opts)
		if !errors.Is(err, ErrNotBookingURL) {
			t.Fatalf("a refused destination must not degrade to an empty room list: "+
				"want an error wrapping ErrNotBookingURL, got err=%v", err)
		}
		if av != nil {
			t.Errorf("a refused lookup must not also return a result to read: got %+v", av)
		}
	})

	t.Run("a booking fetch that merely failed still degrades quietly", func(t *testing.T) {
		// The control that makes the assertion above mean something. This URL
		// gets past the destination pin, so the booking fetch runs and fails on
		// the cancelled context -- an ordinary, retryable failure. That must
		// still degrade to a result with no booking rooms, exactly as before.
		// Without this case, a fix that turned every booking error into a hard
		// failure would satisfy the subtest above.
		opts := base
		opts.BookingURL = "https://www.booking.com/hotel/es/beverly-hills-heights.html"

		av, err := GetRoomAvailabilityWithOpts(ctx, opts)
		if err != nil {
			t.Fatalf("an ordinary booking fetch failure must not become a hard error, "+
				"only a refused destination may: got %v", err)
		}
		if av == nil {
			t.Fatal("a non-refused lookup must return a result")
		}
		if len(av.Rooms) != 0 {
			t.Errorf("no provider should have produced rooms under a cancelled context: got %d", len(av.Rooms))
		}
	})
}

// The refusal test in booking_rooms_origin_test.go proves the pin rejects
// foreign hosts, and every parser test swaps the pin out via
// allowLocalBookingHost. Nothing proved the shipped pin still ADMITS
// Booking.com: a guard hard-wired to refuse everything would satisfy the whole
// suite while silently killing the feature in production.
//
// This runs the production function against the production pin -- no
// reassignment -- and asserts the URL gets past the pin. The context is
// cancelled, so the request dies at the rate limiter and no packet leaves.
func TestFetchBookingRooms_AdmitsBookingDotComThroughProductionPin(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := defaultFetchBookingRooms(ctx, "https://www.booking.com/hotel/es/beverly-hills-heights.html",
		"2026-08-01", "2026-08-03", "EUR")
	if err == nil {
		t.Fatal("expected the cancelled context to stop the fetch; " +
			"if this ever passes, the test is doing real network I/O")
	}
	if errors.Is(err, ErrNotBookingURL) {
		t.Fatalf("the shipped destination pin refused a real https booking.com URL, "+
			"so room lookups are dead in production while the refusal tests still pass: %v", err)
	}
}
