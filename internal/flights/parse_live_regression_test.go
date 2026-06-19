package flights

import (
	"os"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
)

// TestParseFlights_LiveOneWayRegression guards against silent drift in the
// Google Flights response format. Unlike the hand-crafted flight_response.json
// golden file, this fixture is a REAL, unmodified one-way HTTP response body
// captured from Google Flights (BER->BCN), including the anti-XSSI prefix and
// the wrb.fr envelope. The test exercises the full decode path
// (StripAntiXSSI -> DecodeFlightResponse -> ExtractFlightData -> parseFlights)
// exactly as searchGoogleFlightsWithClient does in production.
//
// Issue #198 ("unexpected flight data format") was reported against round-trip
// searches; this regression locks the one-way parser/decoder against the
// current live format so a future Google-side format change is caught offline.
func TestParseFlights_LiveOneWayRegression(t *testing.T) {
	body, err := os.ReadFile("testdata/google_flights_live_oneway.json")
	if err != nil {
		t.Fatalf("read live fixture: %v", err)
	}

	inner, err := batchexec.DecodeFlightResponse(body)
	if err != nil {
		t.Fatalf("DecodeFlightResponse on live body: %v", err)
	}

	rawFlights, err := batchexec.ExtractFlightData(inner)
	if err != nil {
		t.Fatalf("ExtractFlightData on live body: %v", err)
	}
	if len(rawFlights) == 0 {
		t.Fatal("ExtractFlightData returned no raw flight entries")
	}

	flights := parseFlights(rawFlights)
	if len(flights) == 0 {
		t.Fatal("parseFlights returned no flights from live fixture")
	}

	// At least one flight must carry a usable price, currency, and per-leg
	// departure/arrival times — the fields the product actually displays.
	var priced, timed int
	for _, f := range flights {
		if f.Price > 0 && f.Currency != "" {
			priced++
		}
		for _, leg := range f.Legs {
			if leg.DepartureTime != "" && leg.ArrivalTime != "" {
				timed++
				break
			}
		}
	}
	if priced == 0 {
		t.Errorf("no flight with Price>0 and non-empty Currency in %d parsed flights", len(flights))
	}
	if timed == 0 {
		t.Errorf("no flight with a leg carrying both departure and arrival times in %d parsed flights", len(flights))
	}

	t.Logf("live one-way fixture: %d raw entries -> %d parsed flights (%d priced, %d with leg times)",
		len(rawFlights), len(flights), priced, timed)
}
