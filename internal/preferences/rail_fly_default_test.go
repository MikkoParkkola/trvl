package preferences

// MIK-3079: rail+fly origin expansion.
//
// ZYR / ZWE / BRU should appear in the AMS nearby-airports default so
// --home-fan searches include them without waiting for the affinity
// side-effect on RecordWinningOrigin to seed them after a winning
// search. This guards against a regression where someone trims the
// default list.

import "testing"

func TestDefaultNearbyAirports_AMSIncludesRailFlyOrigins(t *testing.T) {
	want := []string{"BRU", "ZWE", "ZYR"}
	got := defaultNearbyAirports()["AMS"]
	have := map[string]bool{}
	for _, c := range got {
		have[c] = true
	}
	for _, w := range want {
		if !have[w] {
			t.Errorf("defaultNearbyAirports()[\"AMS\"] missing %q (got %v)", w, got)
		}
	}
}

func TestNearbyAirportsFor_AMSExpandsToRailFly(t *testing.T) {
	p := &Preferences{NearbyAirports: defaultNearbyAirports()}
	got := p.NearbyAirportsFor("AMS")
	have := map[string]bool{}
	for _, c := range got {
		have[c] = true
	}
	for _, w := range []string{"AMS", "BRU", "ZWE", "ZYR", "EIN"} {
		if !have[w] {
			t.Errorf("NearbyAirportsFor(\"AMS\") missing %q (got %v)", w, got)
		}
	}
}
