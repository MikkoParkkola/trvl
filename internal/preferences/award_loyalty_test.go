package preferences

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/awards"
)

// TestAwardLoyaltyInput proves the saved loyalty profile is projected
// into the dependency-free shape the awards package seeds from:
// frequent-flyer programs carry their carrier code + balance, loyalty
// airlines carry bare codes, and a nil receiver is safe.
func TestAwardLoyaltyInput(t *testing.T) {
	cases := []struct {
		name      string
		prefs     *Preferences
		wantFF    []awards.ProfileProgram
		wantAirls []string
	}{
		{
			name: "frequent-flyer programs map to code+balance",
			prefs: &Preferences{
				FrequentFlyerPrograms: []FrequentFlyerStatus{
					{Alliance: "skyteam", Tier: "elite", AirlineCode: "AY", MilesBalance: 40000},
				},
				LoyaltyAirlines: []string{"KL"},
			},
			wantFF:    []awards.ProfileProgram{{Program: "AY", Balance: 40000}},
			wantAirls: []string{"KL"},
		},
		{
			name: "frequent-flyer entry without airline code is skipped",
			prefs: &Preferences{
				FrequentFlyerPrograms: []FrequentFlyerStatus{
					{Alliance: "oneworld", Tier: "sapphire", MilesBalance: 5000}, // no AirlineCode
					{Alliance: "skyteam", Tier: "elite", AirlineCode: "VS", MilesBalance: 10000},
				},
			},
			wantFF:    []awards.ProfileProgram{{Program: "VS", Balance: 10000}},
			wantAirls: nil,
		},
		{
			name:      "nil receiver returns zero value",
			prefs:     nil,
			wantFF:    nil,
			wantAirls: nil,
		},
		{
			name:      "empty profile yields empty input",
			prefs:     &Preferences{},
			wantFF:    nil,
			wantAirls: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.prefs.AwardLoyaltyInput()
			if len(got.FrequentFlyer) != len(tc.wantFF) {
				t.Fatalf("FrequentFlyer len = %d, want %d (%+v)", len(got.FrequentFlyer), len(tc.wantFF), got.FrequentFlyer)
			}
			for i := range got.FrequentFlyer {
				if got.FrequentFlyer[i] != tc.wantFF[i] {
					t.Errorf("FrequentFlyer[%d] = %+v, want %+v", i, got.FrequentFlyer[i], tc.wantFF[i])
				}
			}
			if len(got.Airlines) != len(tc.wantAirls) {
				t.Fatalf("Airlines = %v, want %v", got.Airlines, tc.wantAirls)
			}
			for i := range got.Airlines {
				if got.Airlines[i] != tc.wantAirls[i] {
					t.Errorf("Airlines[%d] = %q, want %q", i, got.Airlines[i], tc.wantAirls[i])
				}
			}
		})
	}
}

// TestAwardLoyaltyInput_SeedsEndToEnd proves the profile flows through
// the awards seeding helpers into a usable balance set without the user
// specifying anything.
func TestAwardLoyaltyInput_SeedsEndToEnd(t *testing.T) {
	prefs := &Preferences{
		FrequentFlyerPrograms: []FrequentFlyerStatus{
			{AirlineCode: "AY", MilesBalance: 40000},
		},
		LoyaltyAirlines: []string{"KL"},
	}
	seeded := awards.SeedBalancesFromProfile(nil, awards.ProfileProgramsFrom(prefs.AwardLoyaltyInput()))
	want := []awards.PointBalance{
		{Program: "AY", Balance: 40000},
		{Program: "KL", Balance: 0},
	}
	if len(seeded) != len(want) {
		t.Fatalf("seeded = %+v, want %+v", seeded, want)
	}
	for i := range seeded {
		if seeded[i] != want[i] {
			t.Errorf("seeded[%d] = %+v, want %+v", i, seeded[i], want[i])
		}
	}
}
