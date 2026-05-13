package cabinarb

// MIK-3090 (partial): tests for the cabin-class arbitrage detector.

import (
	"strings"
	"testing"
)

func TestDetect_PremiumEconomyWithinThresholdSurfaces(t *testing.T) {
	got := Detect([]CabinFare{
		{Cabin: CabinEconomy, Price: 500},
		{Cabin: CabinPremiumEconomy, Price: 560}, // 12% upsell
	})
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	if got[0].Baseline != CabinEconomy || got[0].Target != CabinPremiumEconomy {
		t.Errorf("Baseline=%q Target=%q, want economy -> premium_economy", got[0].Baseline, got[0].Target)
	}
	if got[0].UpsellAbsolute != 60 {
		t.Errorf("UpsellAbsolute=%.2f, want 60", got[0].UpsellAbsolute)
	}
	if !strings.Contains(got[0].Reason, "12.0%") {
		t.Errorf("Reason=%q should mention 12.0%%", got[0].Reason)
	}
}

func TestDetect_AboveThresholdSuppressed(t *testing.T) {
	got := Detect([]CabinFare{
		{Cabin: CabinEconomy, Price: 500},
		{Cabin: CabinPremiumEconomy, Price: 600}, // 20% upsell, above 15%
	})
	if got != nil {
		t.Errorf("20%% upsell should be suppressed, got %v", got)
	}
}

func TestDetect_TargetCheaperThanBaselineSurfacesAsNoBrainer(t *testing.T) {
	got := Detect([]CabinFare{
		{Cabin: CabinEconomy, Price: 500},
		{Cabin: CabinPremiumEconomy, Price: 480}, // strictly cheaper
	})
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	if got[0].UpsellPercent != 0 {
		t.Errorf("UpsellPercent=%.2f, want 0 for strictly-cheaper case", got[0].UpsellPercent)
	}
	if !strings.Contains(got[0].Reason, "strictly upgrade") {
		t.Errorf("Reason=%q should mention strictly upgrade", got[0].Reason)
	}
}

func TestDetect_LongHaulLadderAllSteps(t *testing.T) {
	// Economy 500, Premium 570 (14%), Business 600 (~5.3% over premium),
	// First 660 (10% over business). All three steps should surface.
	got := Detect([]CabinFare{
		{Cabin: CabinEconomy, Price: 500},
		{Cabin: CabinPremiumEconomy, Price: 570},
		{Cabin: CabinBusiness, Price: 600},
		{Cabin: CabinFirst, Price: 660},
	})
	if len(got) != 3 {
		t.Fatalf("got %d, want 3 ladder steps", len(got))
	}
}

func TestDetect_SortsByUpsellPercentAscending(t *testing.T) {
	got := Detect([]CabinFare{
		{Cabin: CabinEconomy, Price: 500},
		{Cabin: CabinPremiumEconomy, Price: 570}, // 14%
		{Cabin: CabinBusiness, Price: 580},       // 1.75% over premium
	})
	if len(got) < 2 {
		t.Fatalf("got %d, want >= 2", len(got))
	}
	if got[0].UpsellPercent > got[1].UpsellPercent {
		t.Errorf("not sorted ascending: %.2f then %.2f", got[0].UpsellPercent, got[1].UpsellPercent)
	}
}

func TestDetect_PicksCheapestPerCabin(t *testing.T) {
	got := Detect([]CabinFare{
		{Cabin: CabinEconomy, Price: 600, Carrier: "AY"},
		{Cabin: CabinEconomy, Price: 500, Carrier: "KL"}, // cheaper economy
		{Cabin: CabinPremiumEconomy, Price: 560},
	})
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	// Should compare against the 500 economy, not 600.
	if got[0].BaselinePrice != 500 {
		t.Errorf("BaselinePrice=%.2f, want 500 (cheapest economy)", got[0].BaselinePrice)
	}
}

func TestDetect_MissingCabinPairProducesNoStep(t *testing.T) {
	got := Detect([]CabinFare{
		{Cabin: CabinEconomy, Price: 500},
		// no premium_economy quote
	})
	if got != nil {
		t.Errorf("missing pair should yield nil, got %v", got)
	}
}

func TestDetect_NonPositivePriceIgnored(t *testing.T) {
	got := Detect([]CabinFare{
		{Cabin: CabinEconomy, Price: 500},
		{Cabin: CabinPremiumEconomy, Price: 0},   // skipped
		{Cabin: CabinPremiumEconomy, Price: 560}, // used
	})
	if len(got) != 1 || got[0].TargetPrice != 560 {
		t.Errorf("expected single recommendation at 560; got %v", got)
	}
}

func TestDetect_CaseInsensitiveCabinLabels(t *testing.T) {
	got := Detect([]CabinFare{
		{Cabin: "ECONOMY", Price: 500},
		{Cabin: "Premium_Economy", Price: 560},
	})
	if len(got) != 1 {
		t.Errorf("case-insensitive cabin labels should still match; got %v", got)
	}
}

func TestDetectWithThreshold_NonPositiveReturnsNil(t *testing.T) {
	got := DetectWithThreshold([]CabinFare{
		{Cabin: CabinEconomy, Price: 500},
		{Cabin: CabinPremiumEconomy, Price: 560},
	}, 0)
	if got != nil {
		t.Errorf("threshold 0 should yield nil, got %v", got)
	}
}

func TestDetectWithThreshold_LooserCapSurfacesMore(t *testing.T) {
	in := []CabinFare{
		{Cabin: CabinEconomy, Price: 500},
		{Cabin: CabinPremiumEconomy, Price: 600}, // 20% upsell
	}
	if got := DetectWithThreshold(in, 15); got != nil {
		t.Errorf("under 15%% threshold should suppress, got %v", got)
	}
	if got := DetectWithThreshold(in, 25); len(got) != 1 {
		t.Errorf("under 25%% threshold should surface, got %v", got)
	}
}

func TestDetect_EmptyInput(t *testing.T) {
	if got := Detect(nil); got != nil {
		t.Errorf("nil input should yield nil, got %v", got)
	}
	if got := Detect([]CabinFare{}); got != nil {
		t.Errorf("empty input should yield nil, got %v", got)
	}
}

func TestDefaultThresholdMatchesAC(t *testing.T) {
	if DefaultUpsellThresholdPct != 15.0 {
		t.Errorf("DefaultUpsellThresholdPct=%v, want 15.0 (AC-mandated)", DefaultUpsellThresholdPct)
	}
}
