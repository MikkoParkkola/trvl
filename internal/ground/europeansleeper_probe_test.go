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
// scanning forward from a week out for the first boarding date with a train.
// The line runs three nights a week and the weekday pattern shifts with the
// timetable, so a seven-day window always holds a departure.
//
// Opt-in via TRVL_TEST_LIVE_PROBES=1.
func TestLiveProbe_EuropeanSleeper(t *testing.T) {
	testutil.RequireLiveProbe(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	for offset := 7; offset < 14; offset++ {
		date := time.Now().AddDate(0, 0, offset).Format("2006-01-02")
		routes, err := SearchEuropeanSleeper(ctx, "Amsterdam", "Prague", date, "EUR")
		testutil.SkipIfTransient(t, err)
		if err != nil {
			t.Fatalf("SearchEuropeanSleeper(%s): %v", date, err)
		}
		if len(routes) == 0 {
			continue
		}
		for _, r := range routes {
			t.Logf("%s %s -> %s, %.2f %s (%s), %s", date, r.Departure.Time, r.Arrival.Time, r.Price, r.Currency, r.PriceProvenance, r.BookingURL)
			if !strings.HasPrefix(r.Departure.Time, date) {
				t.Errorf("departure %s is not on the requested boarding date %s", r.Departure.Time, date)
			}
			switch r.PriceProvenance {
			case models.PriceLive:
				if r.Price <= 0 {
					t.Errorf("live price %.2f, want > 0", r.Price)
				}
			case models.PriceUnavailable:
			default:
				t.Errorf("PriceProvenance = %q, want live or unavailable", r.PriceProvenance)
			}
			if !strings.HasPrefix(r.BookingURL, "https://booking.europeansleeper.eu/en?") {
				t.Errorf("BookingURL = %q, want the pre-filled booking search", r.BookingURL)
			}
		}
		return
	}
	t.Fatal("no Amsterdam->Prague departure found in a seven-day window")
}
