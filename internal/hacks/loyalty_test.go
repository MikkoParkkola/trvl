package hacks

import (
	"context"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/preferences"
)

// withMileageRuns temporarily replaces the curated mileage-run table for the
// duration of a test and restores it afterwards, so loyalty filtering can be
// exercised against a controlled multi-alliance fixture.
func withMileageRuns(t *testing.T, runs []mileageRunRoute) {
	t.Helper()
	orig := cheapMileageRuns
	cheapMileageRuns = runs
	t.Cleanup(func() { cheapMileageRuns = orig })
}

// fixtureRuns is a controlled set of routes all reachable from the same origin
// (XXX) but spread across all three alliances, so a loyalty filter has
// something to discriminate on.
var fixtureRuns = []mileageRunRoute{
	{From: "XXX", To: "AAA", Airline: "Star Co (SC)", Alliance: "star_alliance", CostEUR: 30, MilesEarned: 400, CostPerMile: 0.08},
	{From: "XXX", To: "BBB", Airline: "Sky Co (KC)", Alliance: "skyteam", CostEUR: 40, MilesEarned: 300, CostPerMile: 0.13},
	{From: "XXX", To: "CCC", Airline: "One Co (OC)", Alliance: "oneworld", CostEUR: 60, MilesEarned: 400, CostPerMile: 0.15},
}

func TestDetectMileageRun_loyaltyFiltersToAlliance(t *testing.T) {
	withMileageRuns(t, fixtureRuns)

	tests := []struct {
		name          string
		loyalty       LoyaltyProfile
		wantAlliances []string // alliances expected in the returned hacks
	}{
		{
			name:          "no loyalty surfaces every alliance",
			loyalty:       LoyaltyProfile{},
			wantAlliances: []string{"star_alliance", "skyteam", "oneworld"},
		},
		{
			name:          "single alliance narrows to that alliance",
			loyalty:       LoyaltyProfile{Alliances: []string{"skyteam"}},
			wantAlliances: []string{"skyteam"},
		},
		{
			name:          "two alliances keep both, drop the third",
			loyalty:       LoyaltyProfile{Alliances: []string{"star_alliance", "oneworld"}},
			wantAlliances: []string{"star_alliance", "oneworld"},
		},
		{
			name:          "alliance match is case-insensitive",
			loyalty:       LoyaltyProfile{Alliances: []string{"SkyTeam"}},
			wantAlliances: []string{"skyteam"},
		},
		{
			name:          "no matching alliance falls back to every run",
			loyalty:       LoyaltyProfile{Alliances: []string{"no_such_alliance"}},
			wantAlliances: []string{"star_alliance", "skyteam", "oneworld"},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := detectMileageRun(context.Background(), DetectorInput{
				Origin:  "XXX",
				Loyalty: tc.loyalty,
			})
			gotAlliances := alliancesFromHacks(t, got)
			assertSameSet(t, gotAlliances, tc.wantAlliances)
		})
	}
}

func TestDetectMileageRun_loyaltyPrefersCheapestFirst(t *testing.T) {
	withMileageRuns(t, fixtureRuns)

	// With all three alliances collected, every run survives the filter and the
	// detector must order them cheapest-per-mile first.
	got := detectMileageRun(context.Background(), DetectorInput{
		Origin:  "XXX",
		Loyalty: LoyaltyProfile{Alliances: []string{"star_alliance", "skyteam", "oneworld"}},
	})
	if len(got) != 3 {
		t.Fatalf("expected 3 hacks, got %d", len(got))
	}
	// Cheapest-per-mile is star_alliance (0.08), then skyteam (0.13), then
	// oneworld (0.15). The Title carries the airline display name, so assert
	// the order via the alliance-derived airline tokens.
	wantOrder := []string{"SC", "KC", "OC"}
	for i, want := range wantOrder {
		if !strings.Contains(got[i].Title, want) {
			t.Errorf("hack[%d] title %q does not contain expected airline %q (ordering broken)", i, got[i].Title, want)
		}
	}
}

func TestDetectMileageRun_emptyLoyaltyBaselineUnchanged(t *testing.T) {
	// A real origin (HEL → HEL-ARN, oneworld) with the zero loyalty profile must
	// behave exactly like the legacy detector: at least one hack, advisory only.
	baseline := detectMileageRun(context.Background(), DetectorInput{Origin: "HEL"})
	withLoyalty := detectMileageRun(context.Background(), DetectorInput{
		Origin:  "HEL",
		Loyalty: LoyaltyProfile{}, // explicit zero value
	})
	if len(baseline) == 0 {
		t.Fatal("expected baseline hacks from HEL")
	}
	if len(baseline) != len(withLoyalty) {
		t.Fatalf("zero loyalty changed result count: baseline=%d withLoyalty=%d", len(baseline), len(withLoyalty))
	}
	for i := range baseline {
		if baseline[i].Title != withLoyalty[i].Title {
			t.Errorf("zero loyalty changed hack[%d] title: %q vs %q", i, baseline[i].Title, withLoyalty[i].Title)
		}
	}
}

func alliancesFromHacks(t *testing.T, hacks []Hack) []string {
	t.Helper()
	// Map the fixture airline tokens back to alliances so the assertion does not
	// depend on hack Title formatting beyond the airline code.
	tokenAlliance := map[string]string{"SC": "star_alliance", "KC": "skyteam", "OC": "oneworld"}
	var out []string
	for _, h := range hacks {
		for token, alliance := range tokenAlliance {
			if strings.Contains(h.Title, "("+token+")") {
				out = append(out, alliance)
			}
		}
	}
	return out
}

func assertSameSet(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("set size mismatch: got %v, want %v", got, want)
	}
	seen := make(map[string]int)
	for _, g := range got {
		seen[g]++
	}
	for _, w := range want {
		if seen[w] == 0 {
			t.Fatalf("missing %q: got %v, want %v", w, got, want)
		}
		seen[w]--
	}
}

func TestLoyaltyFromPreferences(t *testing.T) {
	tests := []struct {
		name           string
		prefs          *preferences.Preferences
		wantAlliances  []string
		wantAirlines   []string
		wantNearStatus bool
		wantHasLoyalty bool
	}{
		{
			name:           "nil preferences yield zero profile",
			prefs:          nil,
			wantHasLoyalty: false,
		},
		{
			name:           "empty preferences yield zero profile",
			prefs:          &preferences.Preferences{},
			wantHasLoyalty: false,
		},
		{
			name: "alliances and airlines mapped and deduplicated",
			prefs: &preferences.Preferences{
				LoyaltyAirlines: []string{"KL", "kl", "AY"},
				FrequentFlyerPrograms: []preferences.FrequentFlyerStatus{
					{Alliance: "SkyTeam", AirlineCode: "KL"},
					{Alliance: "skyteam", AirlineCode: "AF"},
					{Alliance: "oneworld", AirlineCode: "AY"},
				},
			},
			wantAlliances:  []string{"skyteam", "oneworld"},
			wantAirlines:   []string{"KL", "AY", "AF"},
			wantHasLoyalty: true,
		},
		{
			name: "near-status set from outstanding qualifying segments",
			prefs: &preferences.Preferences{
				FrequentFlyerPrograms: []preferences.FrequentFlyerStatus{{Alliance: "oneworld"}},
				LoyaltyBalances: []preferences.LoyaltyBalance{
					{Program: "Finnair Plus", QualSegmentsNeeded: 2},
				},
			},
			wantAlliances:  []string{"oneworld"},
			wantNearStatus: true,
			wantHasLoyalty: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := LoyaltyFromPreferences(tc.prefs)
			if got.HasLoyalty() != tc.wantHasLoyalty {
				t.Errorf("HasLoyalty()=%v, want %v", got.HasLoyalty(), tc.wantHasLoyalty)
			}
			if got.NearStatus != tc.wantNearStatus {
				t.Errorf("NearStatus=%v, want %v", got.NearStatus, tc.wantNearStatus)
			}
			if tc.wantAlliances != nil {
				assertSameSet(t, got.Alliances, tc.wantAlliances)
			}
			if tc.wantAirlines != nil {
				assertSameSet(t, got.Airlines, tc.wantAirlines)
			}
		})
	}
}

func TestLoyaltyProfile_hasAirline(t *testing.T) {
	p := LoyaltyProfile{Airlines: []string{"KL", "AY"}}
	if !p.hasAirline("kl") {
		t.Error("expected case-insensitive airline match for 'kl'")
	}
	if p.hasAirline("BA") {
		t.Error("did not expect a match for unlisted airline 'BA'")
	}
	if p.hasAirline("") {
		t.Error("empty code must never match")
	}
}
