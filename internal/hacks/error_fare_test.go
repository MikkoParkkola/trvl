package hacks

import (
	"context"
	"fmt"
	"math"
	"strings"
	"testing"
)

func TestDetectErrorFare_below_threshold(t *testing.T) {
	// HEL→BCN is ~2900 km (long-haul). Floor for one-way long-haul is €60.
	// Error threshold is 50% of floor = €30. Price of €20 should trigger.
	in := DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		NaivePrice:  20,
	}
	hacks := detectErrorFare(context.Background(), in)
	if len(hacks) == 0 {
		t.Fatal("expected error_fare hack for €20 HEL→BCN one-way")
	}
	if hacks[0].Type != "error_fare" {
		t.Errorf("type: got %q, want error_fare", hacks[0].Type)
	}
	if hacks[0].Savings <= 0 {
		t.Error("expected positive savings")
	}
}

func TestDetectErrorFare_flash_sale(t *testing.T) {
	// HEL→BCN one-way long-haul. Floor = €60, error threshold = €30.
	// Price of €45 is below floor but above error threshold = flash sale.
	in := DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		NaivePrice:  45,
	}
	hacks := detectErrorFare(context.Background(), in)
	if len(hacks) == 0 {
		t.Fatal("expected flash_sale hack for €45 HEL→BCN one-way")
	}
	if hacks[0].Type != "flash_sale" {
		t.Errorf("type: got %q, want flash_sale", hacks[0].Type)
	}
}

func TestDetectErrorFare_normal_price(t *testing.T) {
	// €150 is above the long-haul floor of €60 — no hack should fire.
	in := DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		NaivePrice:  150,
	}
	hacks := detectErrorFare(context.Background(), in)
	if len(hacks) != 0 {
		t.Errorf("expected no hack for normal price €150, got %d", len(hacks))
	}
}

func TestDetectErrorFare_roundtrip(t *testing.T) {
	// Round-trip HEL→BCN long-haul. RT floor = €100, error threshold = €50.
	// Price of €40 should trigger error_fare.
	in := DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		ReturnDate:  "2026-07-01",
		NaivePrice:  40,
	}
	hacks := detectErrorFare(context.Background(), in)
	if len(hacks) == 0 {
		t.Fatal("expected error_fare for €40 RT HEL→BCN")
	}
	if hacks[0].Type != "error_fare" {
		t.Errorf("type: got %q, want error_fare", hacks[0].Type)
	}
}

func TestDetectErrorFare_short_haul(t *testing.T) {
	// AMS→LHR is ~370 km (short-haul). OW floor = €15, error = €7.50.
	// €5 should trigger error_fare.
	in := DetectorInput{
		Origin:      "AMS",
		Destination: "LHR",
		NaivePrice:  5,
	}
	hacks := detectErrorFare(context.Background(), in)
	if len(hacks) == 0 {
		t.Fatal("expected error_fare for €5 AMS→LHR")
	}
	if hacks[0].Type != "error_fare" {
		t.Errorf("type: got %q, want error_fare", hacks[0].Type)
	}
}

func TestDetectErrorFare_empty_input(t *testing.T) {
	hacks := detectErrorFare(context.Background(), DetectorInput{})
	if len(hacks) != 0 {
		t.Error("expected no hack for empty input")
	}
}

func TestDetectErrorFare_no_price(t *testing.T) {
	in := DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		NaivePrice:  0,
	}
	hacks := detectErrorFare(context.Background(), in)
	if len(hacks) != 0 {
		t.Error("expected no hack when NaivePrice is 0")
	}
}

func TestDetectErrorFare_unknown_airports(t *testing.T) {
	in := DetectorInput{
		Origin:      "XYZ",
		Destination: "QQQ",
		NaivePrice:  5,
	}
	hacks := detectErrorFare(context.Background(), in)
	if len(hacks) != 0 {
		t.Error("expected no hack for unknown airports")
	}
}

func TestCheckErrorFare_error_fare(t *testing.T) {
	// HEL→BCN one-way long-haul. Floor = €60, error threshold = €30.
	// €20 should return "error_fare".
	hackType, ok := CheckErrorFare("HEL", "BCN", 20, false)
	if !ok {
		t.Fatal("expected ok=true for €20 HEL→BCN one-way")
	}
	if hackType != "error_fare" {
		t.Errorf("hackType: got %q, want error_fare", hackType)
	}
}

func TestCheckErrorFare_flash_sale(t *testing.T) {
	// €45 one-way HEL→BCN is below floor (€60) but above error threshold (€30).
	hackType, ok := CheckErrorFare("HEL", "BCN", 45, false)
	if !ok {
		t.Fatal("expected ok=true for €45 HEL→BCN one-way")
	}
	if hackType != "flash_sale" {
		t.Errorf("hackType: got %q, want flash_sale", hackType)
	}
}

func TestCheckErrorFare_normal_price(t *testing.T) {
	// €150 is above the floor — should return ok=false.
	hackType, ok := CheckErrorFare("HEL", "BCN", 150, false)
	if ok {
		t.Errorf("expected ok=false for normal price, got hackType=%q", hackType)
	}
}

func TestCheckErrorFare_roundtrip(t *testing.T) {
	// RT HEL→BCN long-haul. RT floor = €100, error threshold = €50.
	// €40 should trigger error_fare.
	hackType, ok := CheckErrorFare("HEL", "BCN", 40, true)
	if !ok {
		t.Fatal("expected ok=true for €40 RT HEL→BCN")
	}
	if hackType != "error_fare" {
		t.Errorf("hackType: got %q, want error_fare", hackType)
	}
}

func TestCheckErrorFare_unknown_airports(t *testing.T) {
	_, ok := CheckErrorFare("XYZ", "QQQ", 5, false)
	if ok {
		t.Error("expected ok=false for unknown airports")
	}
}

func TestCheckErrorFare_empty_input(t *testing.T) {
	_, ok := CheckErrorFare("", "", 0, false)
	if ok {
		t.Error("expected ok=false for empty input")
	}
}

// TestDetectErrorFare_nonEURTarget_suppressedWhenInconvertible proves case
// (b): a non-EUR target currency that can't be honestly converted (XXX is
// the ISO 4217 "no currency" placeholder — it will never appear in a real
// exchange-rate table) suppresses the hack rather than labeling EUR-priced
// math with the wrong currency. Deterministic — no live network required:
// the fake seam below always reports "can't convert" without ever dialing
// out (the prior version of this test relied on the live default seam
// despite its doc comment's claim otherwise).
func TestDetectErrorFare_nonEURTarget_suppressedWhenInconvertible(t *testing.T) {
	orig := convertCurrency
	convertCurrency = func(_ context.Context, amount float64, from, to string) (float64, string) {
		if from == to {
			return amount, to
		}
		return amount, from // can't convert — same contract as the real function
	}
	t.Cleanup(func() { convertCurrency = orig })

	in := DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		NaivePrice:  20,
		Currency:    "XXX",
	}
	hacks := detectErrorFare(context.Background(), in)
	if len(hacks) != 0 {
		t.Errorf("expected no hacks for inconvertible target currency, got %d", len(hacks))
	}
}

// TestDetectErrorFare_eurTarget_labelsEURAndConverts proves cases (a) and
// (c): EUR passes through untouched (no network — ConvertCurrency
// short-circuits on from==to) and the surfaced hack's Currency matches the
// target.
func TestDetectErrorFare_eurTarget_labelsEURAndConverts(t *testing.T) {
	in := DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		NaivePrice:  20,
		Currency:    "EUR",
	}
	hacks := detectErrorFare(context.Background(), in)
	if len(hacks) == 0 {
		t.Fatal("expected error_fare hack for €20 HEL→BCN one-way")
	}
	if hacks[0].Currency != "EUR" {
		t.Errorf("Currency = %q, want EUR", hacks[0].Currency)
	}
	if hacks[0].Savings <= 0 {
		t.Error("expected positive savings")
	}
}

// TestDetectErrorFare_nonEURTarget_noDoubleConversion is a deterministic
// regression test for the double-conversion bug: in.NaivePrice is already
// denominated in the caller's requested currency (per the
// DetectorInput.NaivePrice/Currency contract), so it must be displayed
// as-is — NOT converted again from EUR. Only the fixed EUR classification
// constant (typicalEUR) needs conversion for display.
//
// Uses a fake convertCurrency injected via the seam in currency.go at a
// fixed, known rate (EUR->GBP @ 0.85) so the assertion is computed
// independently of the live destinations.ConvertCurrency — no network
// required, no t.Parallel (seam var is shared package state, set/restored
// sequentially like railGroundSearcher).
func TestDetectErrorFare_nonEURTarget_noDoubleConversion(t *testing.T) {
	const fakeRate = 0.85
	orig := convertCurrency
	convertCurrency = func(_ context.Context, amount float64, from, to string) (float64, string) {
		if from == to {
			return amount, to
		}
		if from == "EUR" && to == "GBP" {
			return amount * fakeRate, "GBP"
		}
		return amount, from
	}
	t.Cleanup(func() { convertCurrency = orig })

	// HEL->BCN is ~2900 km (long-haul). One-way floor = EUR 60, typical =
	// EUR 250, error threshold = EUR 30. NaivePrice 20 (already in GBP, per
	// contract) is below the error threshold, so error_fare fires.
	const naivePriceGBP = 20.0
	in := DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		NaivePrice:  naivePriceGBP,
		Currency:    "GBP",
	}
	hacks := detectErrorFare(context.Background(), in)
	if len(hacks) == 0 {
		t.Fatal("expected error_fare hack for GBP 20 HEL->BCN one-way")
	}
	h := hacks[0]

	if h.Currency != "GBP" {
		t.Errorf("Currency = %q, want GBP", h.Currency)
	}

	displayedPrice := fmt.Sprintf("GBP %.0f", naivePriceGBP)
	if !strings.Contains(h.Title, displayedPrice) {
		t.Errorf("Title = %q, want it to contain %q (raw NaivePrice, not re-converted)", h.Title, displayedPrice)
	}
	if !strings.Contains(h.Description, displayedPrice) {
		t.Errorf("Description = %q, want it to contain %q (raw NaivePrice, not re-converted)", h.Description, displayedPrice)
	}

	// typicalEUR for long-haul one-way is 250 (routePriceRanges in
	// error_fare.go); converted at the fixed fake rate, independent of the
	// seam under test.
	const typicalEUR = 250.0
	wantSavings := roundSavings(typicalEUR*fakeRate - naivePriceGBP)
	if h.Savings != wantSavings {
		t.Errorf("Savings = %.2f, want %.2f (typicalEUR converted to GBP minus raw NaivePrice)", h.Savings, wantSavings)
	}
}

// TestDetectErrorFare_nonEURTarget_JPY_classifiesCorrectly is a deterministic
// regression test for the threshold-currency-mismatch bug: in.NaivePrice is
// denominated in the caller's requested currency (target), but the
// classification thresholds (errorThreshold/flashThreshold) started life as
// fixed EUR constants. Before the fix, those EUR thresholds were compared
// directly against a target-currency price with no conversion — for a
// large-nominal currency like JPY, a EUR 15 flashThreshold is tiny next to
// any realistic JPY price, so `price >= flashThreshold` was always true and
// the detector never fired for JPY (or any non-EUR currency).
//
// Uses a fake convertCurrency injected via the seam in currency.go at a
// fixed, known rate (EUR->JPY @ 130) so the assertion is computed
// independently of the live destinations.ConvertCurrency — no network
// required, no t.Parallel (seam var is shared package state, set/restored
// sequentially like railGroundSearcher).
func TestDetectErrorFare_nonEURTarget_JPY_classifiesCorrectly(t *testing.T) {
	const fakeRate = 130.0
	orig := convertCurrency
	convertCurrency = func(_ context.Context, amount float64, from, to string) (float64, string) {
		if from == to {
			return amount, to
		}
		if from == "EUR" && to == "JPY" {
			return amount * fakeRate, "JPY"
		}
		return amount, from
	}
	t.Cleanup(func() { convertCurrency = orig })

	// HEL->BCN is ~2900 km (long-haul). One-way floor = EUR 60, typical =
	// EUR 250. Converted at the fake rate: floor = JPY 7800, typical =
	// JPY 32500, error threshold = JPY 3900 (50% of floor).
	//
	// NaivePrice 2000 (already in JPY, per contract) is below the JPY error
	// threshold (3900) but was NEVER below the buggy raw-EUR flashThreshold
	// comparison (JPY 2000 >= EUR 60 is always true in float terms) — so the
	// pre-fix detector silently returned nil for this input.
	const naivePriceJPY = 2000.0
	in := DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
		NaivePrice:  naivePriceJPY,
		Currency:    "JPY",
	}
	hacks := detectErrorFare(context.Background(), in)
	if len(hacks) == 0 {
		t.Fatal("expected error_fare hack for JPY 2000 HEL->BCN one-way (threshold must be converted to JPY, not compared raw EUR)")
	}
	h := hacks[0]

	if h.Type != "error_fare" {
		t.Errorf("Type = %q, want error_fare", h.Type)
	}
	if h.Currency != "JPY" {
		t.Errorf("Currency = %q, want JPY", h.Currency)
	}

	const typicalEUR = 250.0
	const floorEUR = 60.0
	dispTypical := typicalEUR * fakeRate // JPY 32500
	convFloor := floorEUR * fakeRate     // JPY 7800
	errorThreshold := convFloor * 0.5    // JPY 3900

	if naivePriceJPY > errorThreshold {
		t.Fatalf("test precondition failed: naivePriceJPY (%.0f) must be <= errorThreshold (%.0f)", naivePriceJPY, errorThreshold)
	}

	wantSavings := roundSavings(dispTypical - naivePriceJPY)
	if h.Savings != wantSavings {
		t.Errorf("Savings = %.2f, want %.2f (JPY typical minus JPY price)", h.Savings, wantSavings)
	}

	wantDiscount := math.Round(((dispTypical - naivePriceJPY) / dispTypical) * 100)
	wantDiscountStr := fmt.Sprintf("%.0f%%", wantDiscount)
	if !strings.Contains(h.Description, wantDiscountStr) {
		t.Errorf("Description = %q, want it to contain discount %q computed in JPY (not mixed with raw EUR typical)", h.Description, wantDiscountStr)
	}

	wantPriceStr := fmt.Sprintf("JPY %.0f", naivePriceJPY)
	if !strings.Contains(h.Title, wantPriceStr) {
		t.Errorf("Title = %q, want it to contain %q (raw NaivePrice, not re-converted)", h.Title, wantPriceStr)
	}
}

func TestDetectErrorFare_intercontinental(t *testing.T) {
	// LHR→JFK is ~5500 km. Intercontinental OW floor = €150, error = €75.
	// €50 should trigger error_fare.
	in := DetectorInput{
		Origin:      "LHR",
		Destination: "JFK",
		NaivePrice:  50,
	}
	hacks := detectErrorFare(context.Background(), in)
	if len(hacks) == 0 {
		t.Fatal("expected error_fare for €50 LHR→JFK")
	}
	if hacks[0].Type != "error_fare" {
		t.Errorf("type: got %q, want error_fare", hacks[0].Type)
	}
}
