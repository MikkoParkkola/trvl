package hacks

import (
	"context"
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
// math with the wrong currency. Deterministic — no live network required.
func TestDetectErrorFare_nonEURTarget_suppressedWhenInconvertible(t *testing.T) {
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
