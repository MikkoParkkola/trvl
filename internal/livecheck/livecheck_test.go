package livecheck

import (
	"context"
	"os"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/watch"
)

// TestChecker_UnsupportedType is deterministic and hits no network: an
// unsupported watch type must return an error rather than a fabricated price.
func TestChecker_UnsupportedType(t *testing.T) {
	t.Parallel()
	price, _, _, err := Checker{}.CheckPrice(context.Background(), watch.Watch{Type: "bus"})
	if err == nil {
		t.Fatal("expected error for unsupported watch type")
	}
	if price != 0 {
		t.Errorf("price = %f, want 0 on error", price)
	}
}

// TestChecker_LiveFlight is a probe-gated test that performs a real flight
// search. It is skipped unless TRVL_TEST_LIVE_PROBES=1 so the default suite
// stays deterministic and offline (per CLAUDE.md).
func TestChecker_LiveFlight(t *testing.T) {
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
	if err != nil {
		t.Fatalf("live flight check failed: %v", err)
	}
	// A real result must carry a positive price and a currency. This is the
	// assertion the original stub could never satisfy.
	if price <= 0 {
		t.Errorf("expected positive live price, got %f", price)
	}
	if currency == "" {
		t.Error("expected non-empty currency on a live result")
	}
}
