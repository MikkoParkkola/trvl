package los

// MIK-3087 (partial): tests for the length-of-stay rate-flip scanner.

import (
	"strings"
	"testing"
)

func TestScanLengthOfStay_DetectsExtendForSavings(t *testing.T) {
	// Classic weekly-rate cliff: 7 nights for 700, 8 nights for 680.
	got := ScanLengthOfStay(7, []LOSQuote{
		{Nights: 6, TotalPrice: 660, Refundable: true},
		{Nights: 7, TotalPrice: 700, Refundable: true},
		{Nights: 8, TotalPrice: 680, Refundable: true},
	})
	var found *Flip
	for i := range got {
		if got[i].AlternativeNights == 8 {
			found = &got[i]
			break
		}
	}
	if found == nil {
		t.Fatal("expected an extend_for_savings flip at 8 nights")
	}
	if found.Kind != FlipExtendForSavings {
		t.Errorf("Kind=%q, want %q", found.Kind, FlipExtendForSavings)
	}
	if found.TotalDelta != -20 {
		t.Errorf("TotalDelta=%.2f, want -20", found.TotalDelta)
	}
	if !strings.Contains(found.Reason, "save 20") {
		t.Errorf("Reason=%q should mention savings of 20", found.Reason)
	}
}

func TestScanLengthOfStay_DetectsShortenSafe(t *testing.T) {
	// Hotel did not lock a weekly rate — 6 nights @ 100/n, 7 @ 110/n.
	got := ScanLengthOfStay(7, []LOSQuote{
		{Nights: 6, TotalPrice: 600, Refundable: true},
		{Nights: 7, TotalPrice: 770, Refundable: true},
	})
	var found *Flip
	for i := range got {
		if got[i].AlternativeNights == 6 {
			found = &got[i]
			break
		}
	}
	if found == nil {
		t.Fatal("expected shorten_safe flip at 6 nights")
	}
	if found.Kind != FlipShortenSafe {
		t.Errorf("Kind=%q, want %q", found.Kind, FlipShortenSafe)
	}
}

func TestScanLengthOfStay_ExtendBetterRateOnlyWhenMaterial(t *testing.T) {
	// 7 nights @ 100/n, 8 nights @ 99/n -> only 1%% better, suppressed.
	suppressed := ScanLengthOfStay(7, []LOSQuote{
		{Nights: 7, TotalPrice: 700},
		{Nights: 8, TotalPrice: 792},
	})
	for _, f := range suppressed {
		if f.Kind == FlipExtendBetterRate {
			t.Errorf("1%% per-night gap should not surface; got %+v", f)
		}
	}
	// 7 nights @ 100/n, 8 nights @ 90/n -> 10%% better, surfaced.
	got := ScanLengthOfStay(7, []LOSQuote{
		{Nights: 7, TotalPrice: 700},
		{Nights: 8, TotalPrice: 720},
	})
	var found *Flip
	for i := range got {
		if got[i].AlternativeNights == 8 {
			found = &got[i]
			break
		}
	}
	if found == nil {
		t.Fatal("expected extend_better_rate at material gap")
	}
	if found.Kind != FlipExtendBetterRate {
		t.Errorf("Kind=%q, want %q", found.Kind, FlipExtendBetterRate)
	}
}

func TestScanLengthOfStay_NilOnMissingBaseline(t *testing.T) {
	got := ScanLengthOfStay(7, []LOSQuote{
		{Nights: 5, TotalPrice: 500},
		{Nights: 8, TotalPrice: 720},
	})
	if got != nil {
		t.Errorf("missing baseline should yield nil, got %v", got)
	}
}

func TestScanLengthOfStay_NilOnZeroBaseline(t *testing.T) {
	if got := ScanLengthOfStay(0, []LOSQuote{{Nights: 7, TotalPrice: 700}}); got != nil {
		t.Errorf("zero baseline should yield nil, got %v", got)
	}
}

func TestScanLengthOfStay_PrefersTotalPriceOverPerNight(t *testing.T) {
	// PerNight set but TotalPrice also set — TotalPrice wins to prevent
	// double-counting if caller populated both.
	got := ScanLengthOfStay(7, []LOSQuote{
		{Nights: 7, TotalPrice: 700, PricePerNight: 999}, // baseline 700
		{Nights: 8, TotalPrice: 680, PricePerNight: 999}, // alt 680
	})
	if len(got) == 0 {
		t.Fatal("expected at least one flip")
	}
	if got[0].BaselineTotal != 700 || got[0].AlternativeTotal != 680 {
		t.Errorf("BaselineTotal=%.0f Alt=%.0f, want 700/680", got[0].BaselineTotal, got[0].AlternativeTotal)
	}
}

func TestScanLengthOfStay_FallsBackToPerNightWhenTotalMissing(t *testing.T) {
	got := ScanLengthOfStay(7, []LOSQuote{
		{Nights: 7, PricePerNight: 100},
		{Nights: 8, PricePerNight: 90},
	})
	if len(got) == 0 {
		t.Fatal("per-night fallback should still produce flips")
	}
}

func TestScanLengthOfStay_SkipsZeroPricedQuotes(t *testing.T) {
	got := ScanLengthOfStay(7, []LOSQuote{
		{Nights: 7, TotalPrice: 700},
		{Nights: 8, TotalPrice: 0},    // ignored
		{Nights: 9, PricePerNight: 0}, // ignored
	})
	for _, f := range got {
		if f.AlternativeNights == 8 || f.AlternativeNights == 9 {
			t.Errorf("zero-priced quote should not produce a flip; got %+v", f)
		}
	}
}

func TestScanLengthOfStay_SortsBestSavingsFirst(t *testing.T) {
	got := ScanLengthOfStay(7, []LOSQuote{
		{Nights: 7, TotalPrice: 700},
		{Nights: 8, TotalPrice: 690}, // -10 savings
		{Nights: 9, TotalPrice: 660}, // -40 savings
	})
	if len(got) < 2 {
		t.Fatalf("expected at least 2 flips, got %d", len(got))
	}
	if got[0].TotalDelta > got[1].TotalDelta {
		t.Errorf("first flip should have largest savings; got deltas %.0f then %.0f", got[0].TotalDelta, got[1].TotalDelta)
	}
}

func TestScanLengthOfStay_RefundableFlagPropagated(t *testing.T) {
	got := ScanLengthOfStay(7, []LOSQuote{
		{Nights: 7, TotalPrice: 700, Refundable: true},
		{Nights: 8, TotalPrice: 680, Refundable: false},
	})
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	if got[0].Refundable {
		t.Errorf("Refundable=true on alt, want false")
	}
}

func TestScanLengthOfStay_NoFlipsWhenAllAlternativesWorse(t *testing.T) {
	got := ScanLengthOfStay(7, []LOSQuote{
		{Nights: 7, TotalPrice: 700},
		{Nights: 8, TotalPrice: 800}, // strictly worse total + nightly
		{Nights: 9, TotalPrice: 950}, // ditto
	})
	if len(got) != 0 {
		t.Errorf("no actionable flips expected, got %v", got)
	}
}
