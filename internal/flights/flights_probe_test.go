package flights

import (
	"context"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/testutil"
)

// TestFlightsProbe searches one-way HEL->BCN 30 days out and validates the
// response shape. Opt-in via TRVL_TEST_LIVE_PROBES=1.
func TestFlightsProbe(t *testing.T) {
	testutil.RequireLiveProbe(t)

	date := time.Now().AddDate(0, 0, 30).Format("2006-01-02")
	t.Logf("searching HEL -> BCN on %s (one-way, economy)", date)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := SearchFlights(ctx, "HEL", "BCN", date, SearchOptions{})
	// Google throttles no-key batchexecute calls from datacenter IPs (HTTP 429,
	// retried with backoff until the context deadline). That is transient noise,
	// not a regression: skip rather than red the nightly. A real format/shape
	// change ("unexpected flight data format") is non-transient and still fails.
	testutil.SkipIfTransient(t, err)
	if err != nil {
		t.Fatalf("SearchFlights: %v", err)
	}
	if !result.Success {
		if testutil.IsTransientMsg(result.Error) {
			t.Skipf("skipping: transient provider noise (not a regression): %s", result.Error)
		}
		t.Fatalf("search unsuccessful: %s", result.Error)
	}
	if result.Count == 0 || len(result.Flights) == 0 {
		t.Fatal("expected at least one flight result")
	}

	t.Logf("got %d flights", result.Count)

	// Find the first flight with a price — Google sometimes returns results
	// without prices (e.g. codeshare listings, unpriced combinations).
	var f models.FlightResult
	for _, candidate := range result.Flights {
		if candidate.Price > 0 {
			f = candidate
			break
		}
	}
	if f.Price <= 0 {
		t.Errorf("no flight with price > 0 found in %d results", result.Count)
	}
	if f.Currency == "" {
		t.Error("flight currency is empty")
	}

	// Must have at least one leg with timing info.
	if len(f.Legs) == 0 {
		t.Fatal("flight has no legs")
	}
	leg := f.Legs[0]
	if leg.DepartureTime == "" {
		t.Error("first leg departure time is empty")
	}
	if leg.ArrivalTime == "" {
		t.Error("first leg arrival time is empty")
	}

	// Find any leg with an airline name for logging.
	airline := "(unknown)"
	for _, l := range f.Legs {
		if l.Airline != "" {
			airline = l.Airline
			break
		}
	}

	t.Logf("sample: %s, %.2f %s, %d stops, %d min, %s -> %s",
		airline, f.Price, f.Currency,
		f.Stops, f.Duration, leg.DepartureTime, leg.ArrivalTime)
}
