package ground

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/testutil"
)

// TestLiveProbe_EuropeanSleeper searches Amsterdam->Prague on the booking API,
// scanning forward from a week out until a departure carries a live price.
// The line runs three nights a week and the weekday pattern shifts with the
// timetable, so a two-week window holds several departures; an unpriced
// (unavailable) route is not success.
//
// Opt-in via TRVL_TEST_LIVE_PROBES=1.
func TestLiveProbe_EuropeanSleeper(t *testing.T) {
	testutil.RequireLiveProbe(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	for offset := 7; offset < 21; offset++ {
		date := time.Now().AddDate(0, 0, offset).Format("2006-01-02")
		routes, err := SearchEuropeanSleeper(ctx, "Amsterdam", "Prague", date, "EUR")
		testutil.SkipIfTransient(t, err)
		if err != nil {
			t.Fatalf("SearchEuropeanSleeper(%s): %v", date, err)
		}
		for _, r := range routes {
			t.Logf("%s %s -> %s, %.2f %s (%s), %s", date, r.Departure.Time, r.Arrival.Time, r.Price, r.Currency, r.PriceProvenance, r.BookingURL)
			if !strings.HasPrefix(r.Departure.Time, date) {
				t.Errorf("departure %s is not on the requested boarding date %s", r.Departure.Time, date)
			}
			if !strings.HasPrefix(r.BookingURL, "https://booking.europeansleeper.eu/en?") {
				t.Errorf("BookingURL = %q, want the pre-filled booking search", r.BookingURL)
			}
			if r.PriceProvenance == models.PriceLive {
				if r.Price <= 0 || r.Currency != "EUR" {
					t.Errorf("live price %.2f %s, want > 0 EUR", r.Price, r.Currency)
				}
				return
			}
		}
	}
	t.Fatal("no Amsterdam->Prague departure with a live price in a two-week window")
}
