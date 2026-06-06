package main

import "testing"

// TestRailFlyFlagWiring verifies the --rail-fly opt-in flag is registered and
// that the hub allowlist is keyed by ORIGIN airports (the detector substitutes
// rail-reachable origins). Guards the gate-direction bug fix. MIK-3079.
func TestRailFlyFlagWiring(t *testing.T) {
	cmd := flightsCmd()
	if cmd.Flags().Lookup("rail-fly") == nil {
		t.Error("--rail-fly flag not registered on flights command")
	}
	// Origin hubs (departure airports with rail-connected alternatives).
	for _, origin := range []string{"AMS", "CDG", "FRA", "ZRH"} {
		if !railFlyHubs[origin] {
			t.Errorf("railFlyHubs should contain origin hub %q", origin)
		}
	}
	// A destination-only airport must NOT be a hub (regression guard for the
	// origin-vs-dest gate bug).
	if railFlyHubs["BCN"] {
		t.Error("BCN is a destination, not a rail-fly origin hub")
	}
}
