package watch

import (
	"context"
	"testing"
)

// Threshold comparison across a currency change.
//
// Split out of check_currency_change_test.go, which crossed the 800-line
// ceiling. The seam is subject: that file covers WHEN a currency change is
// detected; this one covers what must not be compared once it has been.

// TRVL.CURRENCY.BLIND.1 -- a quote in a different currency must never be
// compared against a threshold denominated in the old one.
//
// The reported failure (trvl#545): a watch with BelowPrice = 500 set while the
// watch was in USD, then a poll returning 480 EUR. At ~1.08 USD/EUR that is
// about 518 USD -- ABOVE the user's goal -- but a bare `480 <= 500` fired
// BelowGoal and told them their target was met.
//
// BelowPrice, AlertDropAbs and LowestPrice are bare float64 with no currency of
// their own; they inherit the watch's, which a poll can change out from under
// them. The fix is not conversion -- no FX rate exists at this layer -- but
// refusing to compare: a currency mismatch skips the threshold checks and
// clears the currency-denominated thresholds, surfacing
// AlertsClearedByCurrencyChange so the user knows to re-set them.
func TestCurrencyMismatchNeverFiresBelowGoal(t *testing.T) {
	s := NewStore(t.TempDir())
	id, _, err := s.Add(Watch{
		Type: "flight", Origin: "HEL", Destination: "BCN",
		Currency: "USD", BelowPrice: 500,
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	// A prior observation in USD, so the next poll is a currency CHANGE rather
	// than a first quote.
	if err := s.RecordPrice(id, 520, "USD"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	w, _ := s.Get(id)
	res := checkOne(context.Background(), s, &sequencedChecker{steps: []struct {
		price    float64
		currency string
	}{{price: 480, currency: "EUR"}}}, w)
	if res.Error != nil {
		t.Fatalf("check: %v", res.Error)
	}

	if res.BelowGoal {
		t.Error("BelowGoal fired on 480 EUR against a 500 USD target: the comparison is " +
			"currency-blind and 480 EUR is roughly 518 USD, above the goal")
	}
	if !res.AlertsClearedByCurrencyChange {
		t.Error("the threshold was dropped without telling the user; " +
			"AlertsClearedByCurrencyChange must surface it")
	}

	got, _ := s.Get(id)
	if got.BelowPrice != 0 {
		t.Errorf("BelowPrice = %v, want 0 -- a USD-denominated target cannot survive into EUR",
			got.BelowPrice)
	}
	if got.Currency != "EUR" {
		t.Errorf("Currency = %q, want EUR -- the new quote is still adopted", got.Currency)
	}
}

// TRVL.LASTMINUTE.1 -- last-minute mode must be rejected on watch types that
// can never run it, not accepted and silently ignored.
//
// Detection requires Type == "hotel". The guard that says so sat AFTER the
// room and opportunity branches of Validate, both of which return early -- so
// it was unreachable for exactly the types it exists to reject. The mode
// validated, persisted, and never ran: the user was told it was on and nothing
// happened. That is the worst shape for a setting, because there is no error to
// notice and no behaviour to miss (trvl#543).
func TestLastMinuteRejectedOnTypesThatCannotRunIt(t *testing.T) {
	cases := []struct {
		name  string
		watch Watch
	}{
		{
			name: "room",
			watch: Watch{
				Type: "room", HotelName: "Lutetia", RoomKeywords: []string{"suite"},
				DepartDate: "2027-03-01", ReturnDate: "2027-03-05",
				LastMinuteMode: true,
			},
		},
		{
			name: "opportunity",
			watch: Watch{
				Type: "opportunity", Favourites: []string{"BCN"},
				WindowFrom: "2027-03-01", WindowTo: "2027-03-31",
				LastMinuteMode: true,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.watch.Validate(); err == nil {
				t.Errorf("a %s watch accepted last-minute mode; it can never run one, so the user "+
					"is told the mode is on and nothing ever happens", tc.name)
			}
		})
	}
}

// TRVL.LASTMINUTE.2 -- and the types that CAN run it are unaffected, so the
// guard cannot be satisfied by rejecting everything.
func TestLastMinuteStillAcceptedOnHotelWatches(t *testing.T) {
	w := Watch{
		Type: "hotel", Destination: "Lisbon",
		DepartDate: "2027-03-01", ReturnDate: "2027-03-05",
		LastMinuteMode: true, LastMinuteDropPct: 30,
	}
	if err := w.Validate(); err != nil {
		t.Errorf("a hotel watch was refused last-minute mode: %v", err)
	}

	// Room and opportunity watches WITHOUT the mode must still validate.
	room := Watch{
		Type: "room", HotelName: "Lutetia", RoomKeywords: []string{"suite"},
		DepartDate: "2027-03-01", ReturnDate: "2027-03-05",
	}
	if err := room.Validate(); err != nil {
		t.Errorf("hoisting the guard broke ordinary room validation: %v", err)
	}
}
