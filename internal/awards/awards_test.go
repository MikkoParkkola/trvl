package awards

// MIK-3081: tests for the cross-program award sweet-spot detector.

import (
	"strings"
	"testing"
)

func sampleSeat() AwardSeat {
	return AwardSeat{
		Program: "VS", Origin: "AMS", Destination: "JFK", Date: "2026-06-15",
		Cabin: "business", MilesCost: 50000, CashFees: 320, CashEquivalent: 2400,
		BookableSegments: 1,
	}
}

func TestFindSweetSpots_NilSeatsReturnsNil(t *testing.T) {
	if got := FindSweetSpots(nil, []PointBalance{{Program: "MR", Balance: 100000}}, nil); got != nil {
		t.Errorf("nil seats should return nil, got %v", got)
	}
}

func TestFindSweetSpots_NativeRedemptionWhenBalanceCovers(t *testing.T) {
	got := FindSweetSpots([]AwardSeat{sampleSeat()},
		[]PointBalance{{Program: "VS", Balance: 60000}}, nil)
	if len(got) == 0 {
		t.Fatal("expected at least one sweet spot")
	}
	// Find native VS row.
	var native *SweetSpot
	for i := range got {
		if got[i].SourceProgram == "VS" && got[i].BookingProgram == "VS" {
			native = &got[i]
			break
		}
	}
	if native == nil {
		t.Fatal("expected native VS sweet spot")
	}
	if !native.Affordable {
		t.Errorf("Affordable=false, want true (60k VS >= 50k)")
	}
	if native.MilesSpentSource != 50000 {
		t.Errorf("MilesSpentSource=%d, want 50000", native.MilesSpentSource)
	}
	if !strings.Contains(native.TransferRoute, "native") {
		t.Errorf("TransferRoute=%q should mention native redemption", native.TransferRoute)
	}
}

func TestFindSweetSpots_TransferRouteFromAmexMR(t *testing.T) {
	got := FindSweetSpots([]AwardSeat{sampleSeat()},
		[]PointBalance{{Program: "MR", Balance: 80000}}, nil)
	var fromMR *SweetSpot
	for i := range got {
		if got[i].SourceProgram == "MR" {
			fromMR = &got[i]
			break
		}
	}
	if fromMR == nil {
		t.Fatal("expected MR -> VS sweet spot")
	}
	if fromMR.MilesSpentSource != 50000 {
		t.Errorf("MR @ 1:1 should require 50k, got %d", fromMR.MilesSpentSource)
	}
	if !fromMR.Affordable {
		t.Errorf("80k MR should cover 50k VS need; Affordable=false")
	}
	if !strings.Contains(fromMR.TransferRoute, "MR") || !strings.Contains(fromMR.TransferRoute, "VS") {
		t.Errorf("TransferRoute=%q should describe MR->VS path", fromMR.TransferRoute)
	}
}

func TestFindSweetSpots_PromotionalRatioMakesItCheaper(t *testing.T) {
	// Override default ratios with a 30% promo on MR -> VS so the
	// 50000 mile seat needs only ~38462 MR.
	custom := []TransferRatio{
		{Source: "MR", Target: "VS", Numerator: 1, Denominator: 1.3},
	}
	got := FindSweetSpots([]AwardSeat{sampleSeat()},
		[]PointBalance{{Program: "MR", Balance: 40000}}, custom)
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	want := 38462 // ceil(50000 / 1.3)
	if got[0].MilesSpentSource != want {
		t.Errorf("MilesSpentSource=%d, want %d", got[0].MilesSpentSource, want)
	}
	if !got[0].Affordable {
		t.Errorf("40k MR @ 30%% promo should cover; Affordable=false")
	}
}

func TestFindSweetSpots_AffordableSortsBeforeShort(t *testing.T) {
	got := FindSweetSpots([]AwardSeat{sampleSeat()}, []PointBalance{
		{Program: "MR", Balance: 30000}, // short for VS native at 1:1
		{Program: "VS", Balance: 60000}, // affordable native
	}, nil)
	if len(got) < 2 {
		t.Fatalf("got %d, want >= 2", len(got))
	}
	if !got[0].Affordable {
		t.Errorf("first result must be affordable, got Affordable=%v", got[0].Affordable)
	}
	// All affordable rows must precede non-affordable ones.
	seenShort := false
	for _, s := range got {
		if !s.Affordable {
			seenShort = true
		}
		if seenShort && s.Affordable {
			t.Errorf("affordable row appeared after a short row in sort order")
		}
	}
}

func TestFindSweetSpots_KeepsShortBalanceForGuidance(t *testing.T) {
	got := FindSweetSpots([]AwardSeat{sampleSeat()},
		[]PointBalance{{Program: "MR", Balance: 30000}}, nil)
	if len(got) == 0 {
		t.Fatal("short balance should still emit a row for guidance")
	}
	if got[0].Affordable {
		t.Errorf("Affordable=true with 30k MR < 50k need")
	}
	if !strings.Contains(got[0].Reason, "short") {
		t.Errorf("Reason=%q should mention shortfall", got[0].Reason)
	}
}

func TestFindSweetSpots_EmptyRatiosDisablesTransfers(t *testing.T) {
	got := FindSweetSpots([]AwardSeat{sampleSeat()},
		[]PointBalance{{Program: "MR", Balance: 100000}, {Program: "VS", Balance: 60000}},
		[]TransferRatio{}, // explicit empty -> no transfers, no identity
	)
	if len(got) != 0 {
		t.Errorf("empty ratios should yield no results, got %d", len(got))
	}
}

func TestFindSweetSpots_CentsPerPointMath(t *testing.T) {
	got := FindSweetSpots([]AwardSeat{sampleSeat()},
		[]PointBalance{{Program: "VS", Balance: 60000}}, nil)
	for _, s := range got {
		if s.SourceProgram != "VS" {
			continue
		}
		// Value = 2400 - 320 = 2080; per 50k points; 2080/50000 = 0.0416 → 4.16 cpp
		want := 4.16
		if diff := s.CentsPerPoint - want; diff > 0.01 || diff < -0.01 {
			t.Errorf("CentsPerPoint=%v, want ~%v", s.CentsPerPoint, want)
		}
	}
}

func TestFindSweetSpots_ZeroMilesSeatSkipped(t *testing.T) {
	bad := sampleSeat()
	bad.MilesCost = 0
	got := FindSweetSpots([]AwardSeat{bad}, []PointBalance{{Program: "VS", Balance: 60000}}, nil)
	if len(got) != 0 {
		t.Errorf("zero-miles seat should be skipped, got %d", len(got))
	}
}

func TestFindSweetSpots_BalanceFolding(t *testing.T) {
	// Two MR balances (e.g. personal + business) collapse into one
	// before the affordability check.
	got := FindSweetSpots([]AwardSeat{sampleSeat()}, []PointBalance{
		{Program: "MR", Balance: 30000},
		{Program: "mr", Balance: 30000}, // case-folded
	}, nil)
	var fromMR *SweetSpot
	for i := range got {
		if got[i].SourceProgram == "MR" {
			fromMR = &got[i]
			break
		}
	}
	if fromMR == nil {
		t.Fatal("expected MR sweet spot")
	}
	if !fromMR.Affordable {
		t.Errorf("two 30k balances should fold to 60k and cover 50k need; got short")
	}
}

func TestPointsAtSource_RoundsUp(t *testing.T) {
	cases := []struct {
		miles int
		ratio float64
		want  int
	}{
		{50000, 1.0, 50000},
		{50000, 1.0 / 1.3, 38462}, // 38461.5 -> 38462
		{1, 1.0 / 3.0, 1},         // 0.333 -> 1 (cannot transfer fractional point)
	}
	for _, tc := range cases {
		got := pointsAtSource(tc.miles, tc.ratio)
		if got != tc.want {
			t.Errorf("pointsAtSource(%d, %v) = %d, want %d", tc.miles, tc.ratio, got, tc.want)
		}
	}
}

func TestRatioLabel_FormatsCleanly(t *testing.T) {
	cases := []struct {
		r    TransferRatio
		want string
	}{
		{TransferRatio{Numerator: 1, Denominator: 1}, "1:1"},
		{TransferRatio{Numerator: 1, Denominator: 1.3}, "1:1.3"},
		{TransferRatio{Numerator: 1.5, Denominator: 1}, "1.5:1"},
		{TransferRatio{Numerator: 2, Denominator: 5}, "2:5"},
	}
	for _, tc := range cases {
		if got := ratioLabel(tc.r); got != tc.want {
			t.Errorf("ratioLabel(%v) = %q, want %q", tc.r, got, tc.want)
		}
	}
}

func TestDefaultTransferRatios_HasMajorChains(t *testing.T) {
	want := []struct{ src, tgt string }{
		{"MR", "VS"}, {"MR", "AY"}, {"MR", "AC"}, {"MR", "BA"}, {"MR", "FB"},
		{"UR", "VS"}, {"UR", "AC"}, {"UR", "FB"},
		{"BILT", "AY"}, {"BILT", "AC"}, {"BILT", "VS"},
	}
	got := DefaultTransferRatios()
	for _, w := range want {
		found := false
		for _, r := range got {
			if r.Source == w.src && r.Target == w.tgt {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("DefaultTransferRatios missing %s -> %s", w.src, w.tgt)
		}
	}
}
