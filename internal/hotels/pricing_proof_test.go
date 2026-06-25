//go:build proof

package hotels

import (
	"context"
	"testing"
	"time"
)

// TestGoogleRoomPricingLiveCapture hits the real Google hotel room-pricing path
// and captures whether it returns at least one bookable, priced room.
//
// This is on-demand instrumentation for #290 AC.3, not a CI gate: it is excluded
// from default builds via the `proof` build tag and only runs under
// `go test -tags proof`. Live anti-bot walls are expected in sandboxed
// environments, so a total transport failure skips rather than fails.
func TestGoogleRoomPricingLiveCapture(t *testing.T) {
	now := time.Now()
	checkIn := now.AddDate(0, 0, 30).Format("2006-01-02")
	checkOut := now.AddDate(0, 0, 33).Format("2006-01-02")
	t.Logf("search window: check-in=%s check-out=%s", checkIn, checkOut)

	hotelIDs := []string{
		"/g/11b6d4_v_4",               // Hotel Kamp Helsinki
		"ChIJy7MSZP0LkkYRZw2dDekQP78", // Helsinki hotel
	}

	var (
		bookingReadyCount   int
		anySuccessWithRooms bool
	)

	for _, hotelID := range hotelIDs {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)

		result, err := GetRoomAvailabilityWithOpts(ctx, RoomSearchOptions{
			HotelID:  hotelID,
			CheckIn:  checkIn,
			CheckOut: checkOut,
			Currency: "EUR",
			Guests:   2,
		})
		cancel()

		if err != nil {
			t.Logf("[%s] transport error: %v", hotelID, err)
			continue
		}

		t.Logf("[%s] success=%v rooms=%d notice=%q error=%q",
			hotelID, result.Success, len(result.Rooms), result.Notice, result.Error)

		if len(result.Rooms) > 0 {
			anySuccessWithRooms = true
		}

		for i, room := range result.Rooms {
			t.Logf("  room %d: name=%q price=%.2f currency=%s provider=%q provider_url=%q",
				i+1, room.Name, room.Price, room.Currency, room.Provider, room.ProviderURL)
			if room.Price > 0 && room.ProviderURL != "" {
				bookingReadyCount++
			}
		}
	}

	// Anti-bot / unreachable source is instrumentation noise, not a regression:
	// skip when no hotel call yielded any rooms.
	if !anySuccessWithRooms {
		t.Skipf("live Google room-pricing source returned no rooms for any hotel " +
			"(likely unreachable or bot-walled in this environment) — skipping")
	}

	// Real signal: the pricing path returned rooms but none were bookable. This is
	// exactly the #290 AC.3 regression this harness exists to catch.
	if bookingReadyCount == 0 {
		t.Fatalf("pricing path returned rooms but none were bookable " +
			"(no room had both price > 0 and a provider URL) — #290 AC.3 regression")
	}

	t.Logf("booking_ready_count=%d", bookingReadyCount)
}
