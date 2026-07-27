package afklm

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestProbeAFKLMRoundTrip hits the live AF-KLM Offers API with a real
// round-trip request and asserts the provider returns genuine both-bound
// return-ticket data: FareType=FareRoundTrip plus outbound/inbound Direction
// tags on the legs. Credential is resolved through the provider's normal chain
// (AFKLM_KEY env, macOS Keychain, then 1Password via AFKLM_OP_REF).
//
// Opt-in (default suite stays deterministic + offline):
//
//	TRVL_TEST_LIVE_PROBES=1 go test ./internal/flights/afklm -run TestProbeAFKLMRoundTrip -v
func TestProbeAFKLMRoundTrip(t *testing.T) {
	if os.Getenv("TRVL_TEST_LIVE_PROBES") != "1" {
		t.Skip("set TRVL_TEST_LIVE_PROBES=1 to run live AF-KLM round-trip probe")
	}

	p, err := NewProvider(context.Background(), PolicyExternal)
	if err == ErrNoCredential {
		t.Skip("no AF-KLM credential configured (set AFKLM_KEY or sign in to 1Password)")
	}
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	origin, dest := "AMS", "BCN"
	dep := time.Now().AddDate(0, 1, 0).Format("2006-01-02")
	ret := time.Now().AddDate(0, 1, 7).Format("2006-01-02")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	opts := models.FlightSearchOptions{
		ReturnDate: ret,
		CabinClass: models.Economy,
		Adults:     1,
		Currency:   "EUR",
	}
	res, err := p.SearchFlights(ctx, origin, dest, dep, opts)
	if err != nil {
		t.Fatalf("SearchFlights: %v", err)
	}
	if !res.Success {
		t.Fatalf("search not successful: %s", res.Error)
	}
	t.Logf("AFKLM %s->%s round-trip: %d results", origin, dest, len(res.Flights))
	if len(res.Flights) == 0 {
		t.Fatal("expected at least one round-trip result")
	}

	var rtCount, outboundLegs, inboundLegs int
	for i, f := range res.Flights {
		if f.FareType == models.FareRoundTrip {
			rtCount++
		}
		for _, leg := range f.Legs {
			switch leg.Direction {
			case "outbound":
				outboundLegs++
			case "inbound":
				inboundLegs++
			}
		}
		if i < 3 {
			t.Logf("  [%d] %.2f %s fare=%q legs=%d", i, f.Price, f.Currency, f.FareType, len(f.Legs))
		}
	}
	t.Logf("FareRoundTrip results=%d outboundLegs=%d inboundLegs=%d", rtCount, outboundLegs, inboundLegs)

	if rtCount == 0 {
		t.Errorf("expected at least one FareRoundTrip result from a live round-trip request")
	}
	if outboundLegs == 0 || inboundLegs == 0 {
		t.Errorf("expected both outbound and inbound legs to be Direction-tagged (got out=%d in=%d)", outboundLegs, inboundLegs)
	}
}
