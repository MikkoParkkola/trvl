package flights

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestProbeKiwiRoundTrip hits live Kiwi with a round-trip request (returnDate
// set) and dumps the shape of each returned itinerary so we can decide whether
// Kiwi's MCP surface materializes the return leg or flattens A->B->A into a
// single bogus layover chain. Opt-in:
//
//	TRVL_TEST_LIVE_PROBES=1 go test ./internal/flights -run TestProbeKiwiRoundTrip -v
func TestProbeKiwiRoundTrip(t *testing.T) {
	if os.Getenv("TRVL_TEST_LIVE_PROBES") != "1" {
		t.Skip("set TRVL_TEST_LIVE_PROBES=1 to run live Kiwi round-trip probe")
	}

	origin, destination := "HEL", "BCN"
	dep := time.Now().AddDate(0, 1, 0).Format("2006-01-02")
	ret := time.Now().AddDate(0, 1, 7).Format("2006-01-02")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	opts := SearchOptions{ReturnDate: ret, Adults: 1}
	opts.defaults()
	opts.ReturnDate = ret

	results, err := SearchKiwiFlights(ctx, origin, destination, dep, "EUR", opts)
	t.Logf("SearchKiwiFlights(returnDate=%s) -> %d results, err=%v", ret, len(results), err)
	if err != nil {
		t.Fatalf("kiwi round-trip request failed: %v", err)
	}
	for i, f := range results {
		if i >= 6 {
			break
		}
		t.Logf("  [%d] %.2f %s legs=%d stops=%d self=%v", i, f.Price, f.Currency, len(f.Legs), f.Stops, f.SelfConnect)
		for j, leg := range f.Legs {
			t.Logf("      leg[%d] %s -> %s dep=%s arr=%s dir=%q", j,
				leg.DepartureAirport.Code, leg.ArrivalAirport.Code,
				leg.DepartureTime, leg.ArrivalTime, leg.Direction)
		}
	}
}
