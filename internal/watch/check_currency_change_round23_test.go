package watch

import (
	"context"
	"testing"
)

// TestCheckRoomSetsAlertsClearedByCurrencyChange proves round 23's fix to
// checkRoomWithWebhookContext: when a currency mismatch clears a live
// BelowPrice/AlertDropAbs threshold on a ROOM watch, CheckResult.
// AlertsClearedByCurrencyChange must be set -- mirroring the flight path's
// existing behavior (TestCheckOneSetsAlertsClearedByCurrencyChangeAndNotifies).
// Round 22 set this flag unconditionally on `currencyChanged` (any repeat
// mismatch), regardless of whether a threshold actually existed to lose, and
// left a real threshold-clearing case in the `firstQuoteMismatch` branch
// (currencyMismatch without currencyChanged) unflagged entirely. Found by GPT
// and Grok second-opinion review, 2026-07-30 (round 23).
func TestCheckRoomSetsAlertsClearedByCurrencyChange(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	_, _, err := store.Add(Watch{
		Type:         "room",
		HotelName:    "Grand Hotel",
		RoomKeywords: []string{"deluxe"},
		Origin:       "HEL",
		Destination:  "BCN",
		DepartDate:   "2026-07-01",
		ReturnDate:   "2026-07-05",
		Currency:     "USD",
		BelowPrice:   300,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	checker := &sequencedRoomChecker{steps: [][]RoomMatch{
		{{Name: "Deluxe", Price: 450, Currency: "EUR"}}, // mismatch clears BelowPrice.
	}}

	results := CheckAllWithRooms(context.Background(), store, nil, checker)
	if len(results) != 1 || results[0].Error != nil {
		t.Fatalf("CheckAllWithRooms: results=%+v", results)
	}
	if !results[0].AlertsClearedByCurrencyChange {
		t.Fatal("AlertsClearedByCurrencyChange = false, want true (BelowPrice was live and got cleared)")
	}
}

// TestCheckRoomNoCurrencyClearFlagWhenNoThresholdWasSet is the negative case:
// a room-watch currency mismatch with no BelowPrice/AlertDropAbs set has
// nothing to clear, so the flag must stay false. This also guards against the
// leftover round-22 regression where the flag was set unconditionally inside
// the `currencyChanged` branch even when nothing had actually been cleared.
func TestCheckRoomNoCurrencyClearFlagWhenNoThresholdWasSet(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	_, _, err := store.Add(Watch{
		Type:         "room",
		HotelName:    "Grand Hotel",
		RoomKeywords: []string{"deluxe"},
		Origin:       "HEL",
		Destination:  "BCN",
		DepartDate:   "2026-07-01",
		ReturnDate:   "2026-07-05",
		Currency:     "USD",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	checker := &sequencedRoomChecker{steps: [][]RoomMatch{
		{{Name: "Deluxe", Price: 450, Currency: "EUR"}},
	}}

	results := CheckAllWithRooms(context.Background(), store, nil, checker)
	if len(results) != 1 || results[0].Error != nil {
		t.Fatalf("CheckAllWithRooms: results=%+v", results)
	}
	if results[0].AlertsClearedByCurrencyChange {
		t.Fatal("AlertsClearedByCurrencyChange = true, want false: no threshold was set to clear")
	}
}
