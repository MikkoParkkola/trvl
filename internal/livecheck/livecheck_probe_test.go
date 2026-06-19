package livecheck

import (
	"context"
	"os"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/testutil"
	"github.com/MikkoParkkola/trvl/internal/watch"
)

// TestChecker_LiveFlightProbe performs a real flight search end-to-end. It is the
// automation that catches a re-stubbed checker: if CheckPrice ever stops calling
// the live providers (the original bug), this returns 0 and fails. Probe-gated
// via TRVL_TEST_LIVE_PROBES=1 so the default suite stays offline; run nightly in
// CI (see .github/workflows/live-probes.yml).
func TestChecker_LiveFlightProbe(t *testing.T) {
	if os.Getenv("TRVL_TEST_LIVE_PROBES") != "1" {
		t.Skip("hits live flight APIs; set TRVL_TEST_LIVE_PROBES=1 to run")
	}
	price, currency, _, err := Checker{}.CheckPrice(context.Background(), watch.Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "LHR",
		DepartDate:  "2026-09-15",
		Currency:    "EUR",
	})
	// A throttled night (Google 429 from the CI datacenter IP) is transient
	// noise, not the re-stubbed-checker regression this guard exists for: skip
	// rather than red the nightly. A stubbed checker returns price 0 with no
	// error and still fails the assertion below.
	testutil.SkipIfTransient(t, err)
	if err != nil {
		t.Fatalf("live flight check failed: %v", err)
	}
	if price <= 0 {
		t.Errorf("expected positive live price, got %f (a stubbed checker would return 0)", price)
	}
	if currency == "" {
		t.Error("expected non-empty currency on a live result")
	}
}
