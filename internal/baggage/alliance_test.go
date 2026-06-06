package baggage

import (
	"strings"
	"testing"
)

// --- IsAllianceMember ---

func TestIsAllianceMember(t *testing.T) {
	tests := []struct {
		airline  string
		alliance string
		want     bool
	}{
		{"KL", "skyteam", true},
		{"KL", "SkyTeam", true}, // case-insensitive alliance
		{"kl", "skyteam", true}, // case-insensitive airline
		{"KL", "oneworld", false},
		{"KL", "star_alliance", false},
		{"BA", "oneworld", true},
		{"BA", "skyteam", false},
		{"LH", "star_alliance", true},
		{"LH", "oneworld", false},
		{"FR", "skyteam", false}, // LCC, not in any alliance
		{"FR", "oneworld", false},
		{"FR", "star_alliance", false},
		{"W6", "skyteam", false}, // LCC
		{"", "", false},          // empty inputs
		{"KL", "", false},        // empty alliance
		{"", "skyteam", false},   // empty airline
		{"XX", "skyteam", false}, // unknown airline
		{"KL", "unknown", false}, // unknown alliance
	}
	for _, tt := range tests {
		t.Run(tt.airline+"_"+tt.alliance, func(t *testing.T) {
			got := IsAllianceMember(tt.airline, tt.alliance)
			if got != tt.want {
				t.Errorf("IsAllianceMember(%q, %q) = %v, want %v", tt.airline, tt.alliance, got, tt.want)
			}
		})
	}
}

// --- AllianceForAirline ---

func TestAllianceForAirline(t *testing.T) {
	tests := []struct {
		airline string
		want    string
	}{
		{"KL", "skyteam"},
		{"AF", "skyteam"},
		{"DL", "skyteam"},
		{"BA", "oneworld"},
		{"AA", "oneworld"},
		{"QF", "oneworld"},
		{"LH", "star_alliance"},
		{"UA", "star_alliance"},
		{"SQ", "star_alliance"},
		{"FR", ""},        // LCC
		{"W6", ""},        // LCC
		{"U2", ""},        // LCC
		{"EK", ""},        // Emirates not in any alliance
		{"", ""},          // empty
		{"XX", ""},        // unknown
		{"kl", "skyteam"}, // case-insensitive
	}
	for _, tt := range tests {
		t.Run(tt.airline, func(t *testing.T) {
			got := AllianceForAirline(tt.airline)
			if got != tt.want {
				t.Errorf("AllianceForAirline(%q) = %q, want %q", tt.airline, got, tt.want)
			}
		})
	}
}

// --- ResolveBagBenefit ---

func TestResolveBagBenefit(t *testing.T) {
	tests := []struct {
		alliance string
		tier     string
		wantBags int
		wantKg   float64
		lounge   bool
	}{
		// SkyTeam tiers
		{"skyteam", "elite", 1, 23, false},
		{"skyteam", "elite_plus", 1, 32, true},
		{"skyteam", "gold", 1, 32, true},
		{"skyteam", "silver", 1, 23, false},
		{"skyteam", "platinum", 1, 32, true},
		// Oneworld tiers
		{"oneworld", "ruby", 0, 0, false},
		{"oneworld", "sapphire", 1, 23, true},
		{"oneworld", "emerald", 1, 32, true},
		// Star Alliance tiers
		{"star_alliance", "silver", 1, 23, false},
		{"star_alliance", "gold", 1, 32, true},
		// Unknown combinations
		{"unknown", "gold", 0, 0, false},
		{"skyteam", "unknown", 0, 0, false},
		{"", "", 0, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.alliance+"_"+tt.tier, func(t *testing.T) {
			b := ResolveBagBenefit(tt.alliance, tt.tier)
			if b.ExtraCheckedBags != tt.wantBags {
				t.Errorf("ExtraCheckedBags = %d, want %d", b.ExtraCheckedBags, tt.wantBags)
			}
			if b.CheckedWeightKg != tt.wantKg {
				t.Errorf("CheckedWeightKg = %v, want %v", b.CheckedWeightKg, tt.wantKg)
			}
			if b.LoungeAccess != tt.lounge {
				t.Errorf("LoungeAccess = %v, want %v", b.LoungeAccess, tt.lounge)
			}
		})
	}
}

func TestResolveBagBenefit_CaseInsensitive(t *testing.T) {
	b := ResolveBagBenefit("SkyTeam", "Gold")
	if b.ExtraCheckedBags != 1 {
		t.Errorf("ResolveBagBenefit case-insensitive: ExtraCheckedBags = %d, want 1", b.ExtraCheckedBags)
	}
	if !b.LoungeAccess {
		t.Error("ResolveBagBenefit case-insensitive: expected lounge access for Gold")
	}
}

func TestResolveBagBenefit_AllTiersHaveFreeCarryOn(t *testing.T) {
	// Every tier with ExtraCheckedBags > 0 should also grant FreeCarryOn.
	for alliance, tiers := range allianceTierBenefits {
		for tier, benefit := range tiers {
			if benefit.ExtraCheckedBags > 0 && !benefit.FreeCarryOn {
				t.Errorf("%s/%s has ExtraCheckedBags=%d but FreeCarryOn=false", alliance, tier, benefit.ExtraCheckedBags)
			}
		}
	}
}

// --- AllInCost ---

func TestAllInCost_LCCNoBenefit(t *testing.T) {
	// Ryanair (FR) with no FF status: should add checked bag fee.
	cost, note := AllInCost(100, "FR", true, false, nil)
	if cost != 135 { // 100 + 35 (FR checked fee)
		t.Errorf("AllInCost(FR, checked, no FF) = %v, want 135", cost)
	}
	if !strings.Contains(note, "checked bag") {
		t.Errorf("note = %q, expected mention of checked bag", note)
	}
}

func TestAllInCost_LCCCarryOnFee(t *testing.T) {
	// Ryanair (FR) with carry-on needed and OverheadOnly=true.
	cost, note := AllInCost(100, "FR", false, true, nil)
	if cost != 115 { // 100 + 15 carry-on
		t.Errorf("AllInCost(FR, carry-on, no FF) = %v, want 115", cost)
	}
	if !strings.Contains(note, "carry-on") {
		t.Errorf("note = %q, expected carry-on mention", note)
	}
}

func TestAllInCost_LCCBothFees(t *testing.T) {
	// Ryanair (FR) needing both carry-on and checked.
	cost, note := AllInCost(100, "FR", true, true, nil)
	if cost != 150 { // 100 + 15 carry-on + 35 checked
		t.Errorf("AllInCost(FR, both, no FF) = %v, want 150", cost)
	}
	if !strings.Contains(note, "carry-on") || !strings.Contains(note, "checked") {
		t.Errorf("note = %q, expected both fees mentioned", note)
	}
}

func TestAllInCost_LCCWrongAlliance(t *testing.T) {
	// Ryanair with Oneworld Sapphire: wrong alliance, fees should still apply.
	statuses := []FFStatus{{Alliance: "oneworld", Tier: "sapphire"}}
	cost, _ := AllInCost(100, "FR", true, true, statuses)
	if cost != 150 { // FR is not in any alliance, so no FF benefit
		t.Errorf("AllInCost(FR, both, oneworld sapphire) = %v, want 150", cost)
	}
}

func TestAllInCost_FullServiceWithFFStatus(t *testing.T) {
	// KLM (KL) with SkyTeam Gold: bags included + FF extra bag.
	statuses := []FFStatus{{Alliance: "skyteam", Tier: "gold"}}
	cost, note := AllInCost(300, "KL", true, false, statuses)
	if cost != 300 { // No additional fees: CheckedIncluded=1 + ExtraCheckedBags=1
		t.Errorf("AllInCost(KL, checked, skyteam gold) = %v, want 300", cost)
	}
	if !strings.Contains(note, "FF") {
		t.Errorf("note = %q, expected FF benefit mention", note)
	}
}

func TestAllInCost_FullServiceNoStatus(t *testing.T) {
	// KLM (KL) with no FF status: bags included in ticket.
	cost, note := AllInCost(300, "KL", true, false, nil)
	if cost != 300 { // CheckedIncluded=1, so no extra fee
		t.Errorf("AllInCost(KL, checked, no FF) = %v, want 300", cost)
	}
	if note != "bags included" {
		t.Errorf("note = %q, want 'bags included'", note)
	}
}

func TestAllInCost_UnknownAirline(t *testing.T) {
	// Unknown airline: passthrough, no extra fees.
	cost, note := AllInCost(200, "XX", true, true, nil)
	if cost != 200 {
		t.Errorf("AllInCost(XX) = %v, want 200 (passthrough)", cost)
	}
	if note != "" {
		t.Errorf("note = %q, want empty for unknown airline", note)
	}
}

func TestAllInCost_ZeroBaseFare(t *testing.T) {
	cost, note := AllInCost(0, "KL", true, true, nil)
	if cost != 0 {
		t.Errorf("AllInCost(0 fare) = %v, want 0", cost)
	}
	if note != "" {
		t.Errorf("note = %q, want empty for zero fare", note)
	}
}

func TestAllInCost_NegativeBaseFare(t *testing.T) {
	cost, note := AllInCost(-50, "KL", true, true, nil)
	if cost != 0 {
		t.Errorf("AllInCost(-50 fare) = %v, want 0", cost)
	}
	if note != "" {
		t.Errorf("note = %q, want empty for negative fare", note)
	}
}

func TestAllInCost_MultipleFFStatuses(t *testing.T) {
	// User holds SkyTeam Gold + Oneworld Sapphire.
	statuses := []FFStatus{
		{Alliance: "skyteam", Tier: "gold"},
		{Alliance: "oneworld", Tier: "sapphire"},
	}

	// KLM (SkyTeam): should use SkyTeam Gold benefit.
	cost, note := AllInCost(300, "KL", true, false, statuses)
	if cost != 300 {
		t.Errorf("AllInCost(KL, multi-FF) = %v, want 300", cost)
	}
	if !strings.Contains(note, "FF") {
		t.Errorf("KL note = %q, expected FF mention", note)
	}

	// BA (Oneworld): should use Oneworld Sapphire benefit.
	cost2, note2 := AllInCost(400, "BA", true, false, statuses)
	if cost2 != 400 {
		t.Errorf("AllInCost(BA, multi-FF) = %v, want 400", cost2)
	}
	if !strings.Contains(note2, "FF") {
		t.Errorf("BA note = %q, expected FF mention", note2)
	}
}

func TestAllInCost_NoBagsNeeded(t *testing.T) {
	// No bags needed: should just return base fare.
	cost, note := AllInCost(100, "FR", false, false, nil)
	if cost != 100 {
		t.Errorf("AllInCost(FR, no bags) = %v, want 100", cost)
	}
	if note != "bags included" {
		t.Errorf("note = %q, want 'bags included'", note)
	}
}

func TestAllInCost_FFStatusNoBagsNeeded(t *testing.T) {
	// Full-service airline with FF status but no bags needed: should report
	// "bags included + FF extra bag" since benefit exists even if unused.
	statuses := []FFStatus{{Alliance: "skyteam", Tier: "gold"}}
	cost, note := AllInCost(300, "KL", false, false, statuses)
	if cost != 300 {
		t.Errorf("AllInCost(KL, no bags, FF gold) = %v, want 300", cost)
	}
	if note != "bags included + FF extra bag" {
		t.Errorf("note = %q, want 'bags included + FF extra bag'", note)
	}
}

// --- bestBenefitForAirline ---

func TestBestBenefitForAirline_EmptyStatuses(t *testing.T) {
	b := bestBenefitForAirline("KL", nil)
	if b.ExtraCheckedBags != 0 {
		t.Errorf("expected zero benefit for nil statuses, got %d extra bags", b.ExtraCheckedBags)
	}
}

func TestBestBenefitForAirline_WrongAlliance(t *testing.T) {
	statuses := []FFStatus{{Alliance: "oneworld", Tier: "emerald"}}
	b := bestBenefitForAirline("KL", statuses) // KL is SkyTeam
	if b.ExtraCheckedBags != 0 {
		t.Errorf("expected zero benefit for wrong alliance, got %d extra bags", b.ExtraCheckedBags)
	}
}

func TestBestBenefitForAirline_NonAllianceAirline(t *testing.T) {
	statuses := []FFStatus{{Alliance: "skyteam", Tier: "gold"}}
	b := bestBenefitForAirline("FR", statuses) // FR is not in any alliance
	if b.ExtraCheckedBags != 0 {
		t.Errorf("expected zero benefit for non-alliance airline, got %d extra bags", b.ExtraCheckedBags)
	}
}

func TestBestBenefitForAirline_PicksBest(t *testing.T) {
	// bestBenefitForAirline compares ExtraCheckedBags first, then FreeCarryOn.
	// Both "elite" and "gold" have ExtraCheckedBags=1, so the one that also
	// has FreeCarryOn (both do) or a higher bag count wins. Since they are
	// equal on ExtraCheckedBags and FreeCarryOn, first match sticks. Use a
	// scenario where there's a clear winner: ruby (0 bags) vs emerald (1 bag).
	statuses := []FFStatus{
		{Alliance: "oneworld", Tier: "ruby"},    // 0 extra bags
		{Alliance: "oneworld", Tier: "emerald"}, // 1 extra bag, 32kg
	}
	b := bestBenefitForAirline("BA", statuses)
	if b.ExtraCheckedBags != 1 {
		t.Errorf("expected 1 extra bag (emerald), got %d", b.ExtraCheckedBags)
	}
	if b.CheckedWeightKg != 32 {
		t.Errorf("expected 32kg (emerald), got %v", b.CheckedWeightKg)
	}
	if !b.LoungeAccess {
		t.Error("expected lounge access from emerald tier")
	}
}

// --- AllianceMembers ---

func TestAllianceMembers_SkyTeam(t *testing.T) {
	members := AllianceMembers("skyteam")
	if len(members) == 0 {
		t.Fatal("expected non-empty SkyTeam members")
	}
	// Verify it contains KLM.
	found := false
	for _, m := range members {
		if m == "KL" {
			found = true
			break
		}
	}
	if !found {
		t.Error("SkyTeam members should contain KL (KLM)")
	}
}

func TestAllianceMembers_ReturnsACopy(t *testing.T) {
	original := AllianceMembers("skyteam")
	if len(original) == 0 {
		t.Fatal("expected non-empty result")
	}
	// Mutate the returned slice.
	original[0] = "MUTATED"
	// Get it again and verify the original data is not mutated.
	fresh := AllianceMembers("skyteam")
	if fresh[0] == "MUTATED" {
		t.Error("AllianceMembers returned a reference, not a copy")
	}
}

func TestAllianceMembers_UnknownAlliance(t *testing.T) {
	members := AllianceMembers("unknown")
	if members != nil {
		t.Errorf("expected nil for unknown alliance, got %v", members)
	}
}

func TestAllianceMembers_CaseInsensitive(t *testing.T) {
	members := AllianceMembers("SKYTEAM")
	if len(members) == 0 {
		t.Error("AllianceMembers(\"SKYTEAM\") should match via case-insensitive lookup")
	}
	members = AllianceMembers("SkyTeam")
	if len(members) == 0 {
		t.Error("AllianceMembers(\"SkyTeam\") should match via case-insensitive lookup")
	}
}

func TestAllianceMembers_AllAlliances(t *testing.T) {
	for _, alliance := range []string{"skyteam", "oneworld", "star_alliance"} {
		members := AllianceMembers(alliance)
		if len(members) == 0 {
			t.Errorf("AllianceMembers(%q) returned empty", alliance)
		}
	}
}

// --- HasFFBenefitForAirline ---

func TestHasFFBenefitForAirline_True(t *testing.T) {
	statuses := []FFStatus{{Alliance: "skyteam", Tier: "gold"}}
	if !HasFFBenefitForAirline("KL", statuses) {
		t.Error("expected true for KL with SkyTeam Gold")
	}
}

func TestHasFFBenefitForAirline_FalseWrongAlliance(t *testing.T) {
	statuses := []FFStatus{{Alliance: "skyteam", Tier: "gold"}}
	if HasFFBenefitForAirline("BA", statuses) {
		t.Error("expected false for BA (Oneworld) with SkyTeam Gold")
	}
}

func TestHasFFBenefitForAirline_FalseNoStatus(t *testing.T) {
	if HasFFBenefitForAirline("KL", nil) {
		t.Error("expected false for KL with nil statuses")
	}
}

func TestHasFFBenefitForAirline_FalseNonAllianceAirline(t *testing.T) {
	statuses := []FFStatus{{Alliance: "skyteam", Tier: "gold"}}
	if HasFFBenefitForAirline("FR", statuses) {
		t.Error("expected false for FR (not in any alliance)")
	}
}

func TestHasFFBenefitForAirline_OneworldRuby(t *testing.T) {
	// Ruby has 0 extra checked bags but FreeCarryOn=true, so should return true.
	statuses := []FFStatus{{Alliance: "oneworld", Tier: "ruby"}}
	if !HasFFBenefitForAirline("BA", statuses) {
		t.Error("expected true for BA with Oneworld Ruby (FreeCarryOn)")
	}
}

// --- FFStatusesFromPrefs ---

func TestFFStatusesFromPrefs(t *testing.T) {
	prefs := []struct{ Alliance, Tier string }{
		{"skyteam", "gold"},
		{"oneworld", "sapphire"},
	}
	result := FFStatusesFromPrefs(prefs)
	if len(result) != 2 {
		t.Fatalf("expected 2 statuses, got %d", len(result))
	}
	if result[0].Alliance != "skyteam" || result[0].Tier != "gold" {
		t.Errorf("result[0] = %+v, want skyteam/gold", result[0])
	}
	if result[1].Alliance != "oneworld" || result[1].Tier != "sapphire" {
		t.Errorf("result[1] = %+v, want oneworld/sapphire", result[1])
	}
}

func TestFFStatusesFromPrefs_Empty(t *testing.T) {
	result := FFStatusesFromPrefs(nil)
	if len(result) != 0 {
		t.Errorf("expected 0 for nil prefs, got %d", len(result))
	}
}

// --- Static data integrity ---

func TestAllianceMembers_AllUppercase2LetterIATA(t *testing.T) {
	for alliance, members := range allianceMembers {
		for _, code := range members {
			if len(code) != 2 {
				t.Errorf("alliance %q member %q is not 2-letter", alliance, code)
			}
			if code != strings.ToUpper(code) {
				t.Errorf("alliance %q member %q is not uppercase", alliance, code)
			}
		}
	}
}

func TestAllianceMembers_NoDuplicatesAcrossAlliances(t *testing.T) {
	seen := make(map[string]string) // code -> alliance
	for alliance, members := range allianceMembers {
		for _, code := range members {
			if prevAlliance, ok := seen[code]; ok {
				t.Errorf("airline %q appears in both %q and %q", code, prevAlliance, alliance)
			}
			seen[code] = alliance
		}
	}
}

func TestAllianceMembers_NoDuplicatesWithinAlliance(t *testing.T) {
	for alliance, members := range allianceMembers {
		seen := make(map[string]bool)
		for _, code := range members {
			if seen[code] {
				t.Errorf("airline %q duplicated within %q", code, alliance)
			}
			seen[code] = true
		}
	}
}

func TestAllianceTierBenefits_ConsistentKeys(t *testing.T) {
	// All alliance keys in allianceTierBenefits should also exist in allianceMembers.
	for alliance := range allianceTierBenefits {
		if _, ok := allianceMembers[alliance]; !ok {
			t.Errorf("alliance %q in tier benefits but not in members", alliance)
		}
	}
}

// TestResolveBagBenefit_PerUserStatuses covers tiers a user may hold: Flying
// Blue Gold (SkyTeam) and a downgraded oneworld "Silver" — both grant a free
// checked bag. These are per-user (from profile), never global defaults.
func TestResolveBagBenefit_PerUserStatuses(t *testing.T) {
	if b := ResolveBagBenefit("skyteam", "gold"); b.ExtraCheckedBags < 1 {
		t.Errorf("Flying Blue Gold should grant a free checked bag, got %+v", b)
	}
	if b := ResolveBagBenefit("oneworld", "silver"); b.ExtraCheckedBags < 1 {
		t.Errorf("oneworld Silver (Sapphire) should grant a free checked bag, got %+v", b)
	}
	// Bronze (Ruby) must NOT grant a free bag — guards against over-crediting.
	if b := ResolveBagBenefit("oneworld", "bronze"); b.ExtraCheckedBags != 0 {
		t.Errorf("oneworld Bronze should not grant a free checked bag, got %+v", b)
	}
}
