package profile

import "testing"

// TestDetectAlliance_SKisSkyTeam guards the allianceMap correction moving SAS
// (SK) from Star Alliance to SkyTeam (SAS joined SkyTeam in 2024). A profile
// whose flights are all on SK must resolve to SkyTeam, not Star Alliance.
func TestDetectAlliance_SKisSkyTeam(t *testing.T) {
	got := detectAlliance([]AirlineStats{{Code: "SK", Flights: 5}})
	if got != "SkyTeam" {
		t.Fatalf("detectAlliance(SK) = %q, want SkyTeam", got)
	}
}

// TestDetectAlliance_unchangedAnchors guards the rest of the map so the SK move
// did not disturb the other memberships.
func TestDetectAlliance_unchangedAnchors(t *testing.T) {
	cases := map[string]string{
		"LH": "Star Alliance",
		"AF": "SkyTeam",
		"KL": "SkyTeam",
		"BA": "Oneworld",
	}
	for code, want := range cases {
		if got := detectAlliance([]AirlineStats{{Code: code, Flights: 3}}); got != want {
			t.Errorf("detectAlliance(%s) = %q, want %q", code, got, want)
		}
	}
}
