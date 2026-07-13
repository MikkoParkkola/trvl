package hacks

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/destinations"
)

func TestDetectDepartureTax_emptyInput(t *testing.T) {
	hacks := detectDepartureTax(context.Background(), DetectorInput{})
	if len(hacks) != 0 {
		t.Errorf("expected no hacks for empty input, got %d", len(hacks))
	}
}

func TestDetectDepartureTax_missingOrigin(t *testing.T) {
	hacks := detectDepartureTax(context.Background(), DetectorInput{
		Destination: "BCN",
	})
	if len(hacks) != 0 {
		t.Errorf("expected no hacks for missing origin, got %d", len(hacks))
	}
}

func TestDetectDepartureTax_missingDestination(t *testing.T) {
	hacks := detectDepartureTax(context.Background(), DetectorInput{
		Origin: "AMS",
	})
	if len(hacks) != 0 {
		t.Errorf("expected no hacks for missing destination, got %d", len(hacks))
	}
}

func TestDetectDepartureTax_unknownAirport(t *testing.T) {
	hacks := detectDepartureTax(context.Background(), DetectorInput{
		Origin:      "XYZ",
		Destination: "ABC",
	})
	if len(hacks) != 0 {
		t.Errorf("expected no hacks for unknown airports, got %d", len(hacks))
	}
}

func TestDetectDepartureTax_zeroTaxOrigin(t *testing.T) {
	// Helsinki (FI) has zero aviation tax — should return nil.
	hacks := detectDepartureTax(context.Background(), DetectorInput{
		Origin:      "HEL",
		Destination: "BCN",
	})
	if len(hacks) != 0 {
		t.Errorf("expected no hacks for zero-tax origin (FI), got %d", len(hacks))
	}
}

func TestDetectDepartureTax_zeroTaxOriginPrague(t *testing.T) {
	// Prague (CZ) has zero aviation tax — should return nil.
	hacks := detectDepartureTax(context.Background(), DetectorInput{
		Origin:      "PRG",
		Destination: "BCN",
	})
	if len(hacks) != 0 {
		t.Errorf("expected no hacks for zero-tax origin (CZ), got %d", len(hacks))
	}
}

func TestDetectDepartureTax_highTaxNoAlternative(t *testing.T) {
	// MAD (ES) — Spain is not in departureTaxEUR so should return nil.
	hacks := detectDepartureTax(context.Background(), DetectorInput{
		Origin:      "MAD",
		Destination: "HEL",
	})
	if len(hacks) != 0 {
		t.Errorf("expected no hacks for origin country not in tax map, got %d", len(hacks))
	}
}

// TestDetectDepartureTax_cphToHel proves the net-of-ground-cost fix: CPH
// (DK) has only EUR 5 departure tax, and its cheapest zero-tax alternative
// (MMX/Malmo) costs EUR 10 in ground transport to reach. Once Savings is
// honestly netted against the transport cost (rather than reporting the
// gross tax as if transport were free), that is not a real saving, so no
// hack should surface.
func TestDetectDepartureTax_cphToHel(t *testing.T) {
	hacks := detectDepartureTax(context.Background(), DetectorInput{
		Origin:      "CPH",
		Destination: "BCN",
	})
	if len(hacks) != 0 {
		t.Errorf("expected no hack once ground cost (EUR 10) is netted against the EUR 5 tax, got %d", len(hacks))
	}
}

// syntheticTaxOrigin/syntheticTaxAlt are injected into the package-level
// fixture tables for the duration of a test so the net-of-ground-cost
// arithmetic and non-EUR currency conversion can be proven deterministically.
// No route in the real fixture data has a ground cost cheaper than the tax
// saved (see TestDetectDepartureTax_cphToHel), so a synthetic route is
// required to exercise the surfaced-hack path at all.
const (
	syntheticTaxOrigin = "ZZ1"
	syntheticTaxAlt    = "ZZ2"
)

// newSyntheticTaxRoute registers a high-tax origin (EUR 20) with a single
// zero-tax alternative reachable for EUR 6 ground transport — a genuine net
// saving of EUR 14 — and removes the fixture entries when the test ends.
func newSyntheticTaxRoute(t *testing.T) {
	t.Helper()
	iataToCountry[syntheticTaxOrigin] = "Z1"
	iataToCountry[syntheticTaxAlt] = "Z2"
	departureTaxEUR["Z1"] = 20
	departureTaxEUR["Z2"] = 0
	nearbyAirports[syntheticTaxOrigin] = []nearbyEntry{
		{Code: syntheticTaxAlt, City: "Zed City", GroundCost: 6, GroundMins: 30, Description: "Bus"},
	}
	t.Cleanup(func() {
		delete(iataToCountry, syntheticTaxOrigin)
		delete(iataToCountry, syntheticTaxAlt)
		delete(departureTaxEUR, "Z1")
		delete(departureTaxEUR, "Z2")
		delete(nearbyAirports, syntheticTaxOrigin)
	})
}

// TestDetectDepartureTax_fieldsPopulated proves the surfaced hack carries a
// full set of user-facing fields, using the synthetic route so a genuine net
// saving actually exists to surface.
func TestDetectDepartureTax_fieldsPopulated(t *testing.T) {
	newSyntheticTaxRoute(t)
	hacks := detectDepartureTax(context.Background(), DetectorInput{
		Origin:      syntheticTaxOrigin,
		Destination: "BCN",
	})
	if len(hacks) == 0 {
		t.Fatal("expected a hack for the synthetic high-tax route")
	}
	h := hacks[0]
	if h.Type != "departure_tax" {
		t.Errorf("type = %q, want departure_tax", h.Type)
	}
	if h.Savings <= 0 {
		t.Errorf("savings should be > 0, got %.0f", h.Savings)
	}
	if h.Title == "" {
		t.Error("title is empty")
	}
	if h.Description == "" {
		t.Error("description is empty")
	}
	if len(h.Steps) == 0 {
		t.Error("steps are empty")
	}
	if len(h.Risks) == 0 {
		t.Error("risks are empty")
	}
}

func TestDetectDepartureTax_caseInsensitive(t *testing.T) {
	newSyntheticTaxRoute(t)
	// Lowercase input should work.
	hacks := detectDepartureTax(context.Background(), DetectorInput{
		Origin:      strings.ToLower(syntheticTaxOrigin),
		Destination: "bcn",
	})
	if len(hacks) == 0 {
		t.Fatal("expected hack for lowercase origin")
	}
}

func TestDetectDepartureTax_currencyDefault(t *testing.T) {
	newSyntheticTaxRoute(t)
	hacks := detectDepartureTax(context.Background(), DetectorInput{
		Origin:      syntheticTaxOrigin,
		Destination: "BCN",
	})
	if len(hacks) == 0 {
		t.Fatal("expected at least one hack")
	}
	if hacks[0].Currency != "EUR" {
		t.Errorf("currency = %q, want EUR", hacks[0].Currency)
	}
	if hacks[0].Savings <= 0 {
		t.Errorf("savings should be > 0, got %.0f", hacks[0].Savings)
	}
}

// TestDetectDepartureTax_eurTarget_labelsEURAndConverts proves cases (a)
// and (c) explicitly: EUR passes through untouched (no network —
// ConvertCurrency short-circuits on from==to), the surfaced hack's Currency
// matches the target, and Savings is net of the EUR 6 ground cost (finding
// #6): 20 - 6 = 14.
func TestDetectDepartureTax_eurTarget_labelsEURAndConverts(t *testing.T) {
	newSyntheticTaxRoute(t)
	hacks := detectDepartureTax(context.Background(), DetectorInput{
		Origin:      syntheticTaxOrigin,
		Destination: "BCN",
		Currency:    "EUR",
	})
	if len(hacks) == 0 {
		t.Fatal("expected at least one hack")
	}
	if hacks[0].Currency != "EUR" {
		t.Errorf("Currency = %q, want EUR", hacks[0].Currency)
	}
	if hacks[0].Savings != 14 {
		t.Errorf("Savings = %.2f, want 14 (net of the EUR 6 ground cost)", hacks[0].Savings)
	}
}

// TestDetectDepartureTax_nonEURTarget_netsGroundCostAndConverts proves
// finding #6 (Savings must be net of ground cost, not the gross tax) AND
// asserts both the converted numeric amount and the currency label for a
// non-EUR target, per the review's coverage requirement.
func TestDetectDepartureTax_nonEURTarget_netsGroundCostAndConverts(t *testing.T) {
	newSyntheticTaxRoute(t)
	ctx := context.Background()
	hacks := detectDepartureTax(ctx, DetectorInput{
		Origin:      syntheticTaxOrigin,
		Destination: "BCN",
		Currency:    "GBP",
	})
	if len(hacks) == 0 {
		t.Fatal("expected at least one hack")
	}
	h := hacks[0]
	if h.Currency != "GBP" {
		t.Errorf("Currency = %q, want GBP", h.Currency)
	}
	// Net EUR 14 (20 tax - 6 ground cost) converted to GBP.
	wantSavings, cur := destinations.ConvertCurrency(ctx, 14, "EUR", "GBP")
	if cur != "GBP" {
		t.Fatalf("test setup: EUR->GBP should be convertible offline, got currency %q", cur)
	}
	wantSavings = roundSavings(wantSavings)
	if math.Abs(h.Savings-wantSavings) > 1 {
		t.Errorf("Savings = %.2f, want ~%.2f (net EUR 14 converted to GBP)", h.Savings, wantSavings)
	}
	if h.Savings == 20 {
		t.Error("Savings equals the gross tax — ground cost was not netted out")
	}
}

// TestDetectDepartureTax_nonEURTarget_suppressedWhenInconvertible proves
// case (b): a non-EUR target currency that can't be honestly converted (XXX
// is the ISO 4217 "no currency" placeholder — it will never appear in a
// real exchange-rate table) suppresses the hack rather than labeling
// EUR-denominated tax/ground-cost figures with the wrong currency.
// Deterministic — no live network required.
func TestDetectDepartureTax_nonEURTarget_suppressedWhenInconvertible(t *testing.T) {
	hacks := detectDepartureTax(context.Background(), DetectorInput{
		Origin:      "CPH",
		Destination: "BCN",
		Currency:    "XXX",
	})
	if len(hacks) != 0 {
		t.Errorf("expected no hacks for inconvertible target currency, got %d", len(hacks))
	}
}

// --- Static data tests ---

func TestDepartureTaxEUR_populated(t *testing.T) {
	if len(departureTaxEUR) == 0 {
		t.Fatal("departureTaxEUR is empty")
	}
}

func TestDepartureTaxEUR_zeroTaxCountries(t *testing.T) {
	zeroTax := []string{"IE", "PT", "CY", "MT", "FI", "EE", "LV", "LT", "CZ", "PL", "HU", "RO", "BG", "HR", "SE"}
	for _, cc := range zeroTax {
		tax, ok := departureTaxEUR[cc]
		if !ok {
			t.Errorf("missing zero-tax country %s from departureTaxEUR", cc)
			continue
		}
		if tax != 0 {
			t.Errorf("expected zero tax for %s, got %.0f", cc, tax)
		}
	}
}

func TestDepartureTaxEUR_highTaxCountries(t *testing.T) {
	highTax := []string{"GB", "DE", "FR", "NL", "AT", "NO"}
	for _, cc := range highTax {
		tax, ok := departureTaxEUR[cc]
		if !ok {
			t.Errorf("missing high-tax country %s from departureTaxEUR", cc)
			continue
		}
		if tax <= 0 {
			t.Errorf("expected positive tax for %s, got %.0f", cc, tax)
		}
	}
}

func TestCountryName_known(t *testing.T) {
	if got := countryName("NL"); got != "Netherlands" {
		t.Errorf("countryName(NL) = %q, want Netherlands", got)
	}
}

func TestCountryName_unknown(t *testing.T) {
	if got := countryName("XX"); got != "XX" {
		t.Errorf("countryName(XX) = %q, want XX", got)
	}
}
