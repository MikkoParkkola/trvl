package weather

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLiveProbe_OpenMeteoMarine exercises the keyless Open-Meteo Marine sea-state
// path against the live API. Gated by TRVL_TEST_LIVE_PROBES=1 (and skipped by
// -short), it hits a real ferry route's coordinates (Helsinki harbour) and
// asserts a non-negative wave height comes back. No API key required.
func TestLiveProbe_OpenMeteoMarine(t *testing.T) {
	if testing.Short() {
		t.Skip("live probe skipped under -short")
	}
	if os.Getenv("TRVL_TEST_LIVE_PROBES") != "1" {
		t.Skip("live probes disabled (set TRVL_TEST_LIVE_PROBES=1)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Helsinki South Harbour — a real Tallink/Viking ferry departure point.
	state, err := GetSeaState(ctx, 60.16, 24.94)
	if err != nil {
		t.Fatalf("Open-Meteo Marine probe failed: %v", err)
	}
	if state.WaveHeight < 0 {
		t.Fatalf("got negative wave height: %.2f", state.WaveHeight)
	}
	if state.Label == "" {
		t.Error("expected a non-empty sea-state label")
	}

	t.Logf("Open-Meteo Marine @ Helsinki (60.16,24.94): wave_height=%.2fm swell=%.2fm label=%q",
		state.WaveHeight, state.SwellHeight, state.Label)
}
