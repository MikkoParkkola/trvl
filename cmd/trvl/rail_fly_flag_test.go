package main

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/hacks"
)

// TestRailFlyFlagWiring verifies the --rail-fly flag is registered and that
// rail+fly is now driven purely by RailFlyStationsForHub (no hardcoded hub
// allowlist). Per #469 the presence of stations for an origin enables it.
func TestRailFlyFlagWiring(t *testing.T) {
	cmd := flightsCmd()
	if cmd.Flags().Lookup("rail-fly") == nil {
		t.Error("--rail-fly flag not registered on flights command")
	}
	// Origins with rail stations must be recognized (via the data, not allowlist).
	for _, origin := range []string{"AMS", "CDG", "FRA", "ZRH"} {
		if len(hacks.RailFlyStationsForHub(origin)) == 0 {
			t.Errorf("RailFlyStationsForHub(%q) should return stations", origin)
		}
	}
	// Non-hub destination must not have rail-fly stations.
	if len(hacks.RailFlyStationsForHub("BCN")) != 0 {
		t.Error("BCN should have no rail-fly stations (not an origin hub)")
	}
}
