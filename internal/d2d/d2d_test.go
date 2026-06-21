package d2d_test

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/d2d"
)

// --- helpers ---

func confirmedLeg(from, to string, price float64, currency, source string) d2d.Leg {
	return d2d.Leg{
		Mode: "train", From: from, To: to,
		Price: price, Currency: currency, Source: source,
		Verification: d2d.Confirmed,
	}
}

func indicativeLeg(from, to string, price float64, currency, source string) d2d.Leg {
	return d2d.Leg{
		Mode: "bus", From: from, To: to,
		Price: price, Currency: currency, Source: source,
		Verification: d2d.Indicative,
	}
}

func unverifiedLeg(from, to string, price float64, currency string) d2d.Leg {
	return d2d.Leg{
		Mode: "taxi", From: from, To: to,
		Price: price, Currency: currency,
		Verification: d2d.Unverified,
	}
}

// sourceLeg builds a leg with no explicit Verification — relies on DefaultVerification.
func sourceLeg(from, to string, price float64, currency, source string) d2d.Leg {
	return d2d.Leg{
		Mode: "bus", From: from, To: to,
		Price: price, Currency: currency, Source: source,
	}
}

// --- TRVL.D2D core tests ---

// TRVL.D2D.1 — mixed trip: confirmed leg + rome2rio/indicative leg.
// The indicative leg must NOT appear in ConfirmedTotal and MUST be in IndicativeLegs.
func TestCompute_mixedTrip_indicativeLegExcludedFromConfirmedTotal(t *testing.T) {
	// GIVEN: one confirmed flight + one indicative rome2rio bus
	legs := []d2d.Leg{
		confirmedLeg("HEL", "LHR", 120.0, "EUR", "google_flights"),
		{Mode: "bus", From: "London", To: "Paris", Price: 25.0, Currency: "EUR", Source: "rome2rio"},
	}

	// WHEN
	got := d2d.Compute(legs)

	// THEN: confirmed total = 120 only; indicative leg surfaced separately
	if got.ConfirmedTotal != 120.0 {
		t.Errorf("ConfirmedTotal = %.2f, want 120.00 (rome2rio leg must not be included)", got.ConfirmedTotal)
	}
	if len(got.IndicativeLegs) != 1 {
		t.Errorf("IndicativeLegs len = %d, want 1", len(got.IndicativeLegs))
	}
	if got.IndicativeLegs[0].Source != "rome2rio" {
		t.Errorf("IndicativeLegs[0].Source = %q, want \"rome2rio\"", got.IndicativeLegs[0].Source)
	}
	if len(got.UnverifiedLegs) != 0 {
		t.Errorf("UnverifiedLegs len = %d, want 0", len(got.UnverifiedLegs))
	}
	if got.Band != d2d.BandWide {
		t.Errorf("Band = %q, want %q (indicative leg present)", got.Band, d2d.BandWide)
	}
	if got.Counts.Confirmed != 1 || got.Counts.Indicative != 1 || got.Counts.Unverified != 0 {
		t.Errorf("Counts = %+v, want {Confirmed:1 Indicative:1 Unverified:0}", got.Counts)
	}
}

// TRVL.D2D.2 — all-confirmed trip: tight band, ConfirmedTotal = sum.
func TestCompute_allConfirmed_tightBandAndExactSum(t *testing.T) {
	// GIVEN: three confirmed legs, same currency
	legs := []d2d.Leg{
		confirmedLeg("HEL", "LHR", 120.0, "EUR", "google_flights"),
		confirmedLeg("London St Pancras", "Paris Gare du Nord", 85.50, "EUR", "eurostar"),
		confirmedLeg("Paris CDG", "Nice", 49.0, "EUR", "ouigo"),
	}

	// WHEN
	got := d2d.Compute(legs)

	// THEN
	want := 120.0 + 85.50 + 49.0 // = 254.50
	if got.ConfirmedTotal != want {
		t.Errorf("ConfirmedTotal = %.2f, want %.2f", got.ConfirmedTotal, want)
	}
	if got.Band != d2d.BandExact {
		t.Errorf("Band = %q, want %q", got.Band, d2d.BandExact)
	}
	if got.BandLow != got.ConfirmedTotal || got.BandHigh != got.ConfirmedTotal {
		t.Errorf("Band[%f,%f] should equal ConfirmedTotal %f for exact band", got.BandLow, got.BandHigh, got.ConfirmedTotal)
	}
	if got.MixedCurrency {
		t.Error("MixedCurrency should be false for single-currency trip")
	}
	if len(got.IndicativeLegs) != 0 || len(got.UnverifiedLegs) != 0 {
		t.Errorf("want no indicative/unverified legs, got ind=%d unv=%d", len(got.IndicativeLegs), len(got.UnverifiedLegs))
	}
	if got.Counts.Confirmed != 3 {
		t.Errorf("Counts.Confirmed = %d, want 3", got.Counts.Confirmed)
	}
}

// TRVL.D2D.3 — trip with an unverified leg: excluded from confirmed total, band reflects uncertainty.
func TestCompute_unverifiedLeg_excludedAndBandUnknown(t *testing.T) {
	// GIVEN: confirmed flight + unverified last-mile
	legs := []d2d.Leg{
		confirmedLeg("HEL", "TLL", 60.0, "EUR", "ryanair"),
		unverifiedLeg("Tallinn Airport", "Hotel", 15.0, "EUR"),
	}

	// WHEN
	got := d2d.Compute(legs)

	// THEN: confirmed total = 60 only
	if got.ConfirmedTotal != 60.0 {
		t.Errorf("ConfirmedTotal = %.2f, want 60.00", got.ConfirmedTotal)
	}
	if len(got.UnverifiedLegs) != 1 {
		t.Errorf("UnverifiedLegs len = %d, want 1", len(got.UnverifiedLegs))
	}
	if got.Band != d2d.BandUnknown {
		t.Errorf("Band = %q, want %q", got.Band, d2d.BandUnknown)
	}
	// BandHigh should include the unverified leg's price
	if got.BandHigh < got.ConfirmedTotal+15.0 {
		t.Errorf("BandHigh = %.2f, want >= %.2f (confirmed + unverified)", got.BandHigh, got.ConfirmedTotal+15.0)
	}
	if got.Counts.Unverified != 1 || got.Counts.Confirmed != 1 {
		t.Errorf("Counts = %+v, want {Confirmed:1 Unverified:1}", got.Counts)
	}
}

// TRVL.D2D.4 — source → verification mapping: rome2rio always maps to Indicative.
func TestDefaultVerification_rome2rioIsIndicative(t *testing.T) {
	cases := []struct {
		source string
		want   d2d.Verification
	}{
		{"rome2rio", d2d.Indicative},
		{"Rome2Rio", d2d.Indicative}, // case-insensitive
		{"ROME2RIO", d2d.Indicative},
		{"google_flights", d2d.Confirmed},
		{"ryanair", d2d.Confirmed},
		{"booking.com", d2d.Confirmed},
		{"", d2d.Unverified},
		{"unknown_provider", d2d.Confirmed}, // unknown transactional providers default Confirmed
	}
	for _, tc := range cases {
		got := d2d.DefaultVerification(tc.source)
		if got != tc.want {
			t.Errorf("DefaultVerification(%q) = %q, want %q", tc.source, got, tc.want)
		}
	}
}

// TRVL.D2D.5 — source-derived verification via Leg (no explicit Verification field).
func TestCompute_sourceDerivesVerification_rome2rioIndicative(t *testing.T) {
	// GIVEN: leg with no Verification set — source is "rome2rio"
	legs := []d2d.Leg{
		confirmedLeg("A", "B", 100.0, "EUR", "google_flights"),
		sourceLeg("B", "C", 30.0, "EUR", "rome2rio"), // should be treated as Indicative
	}

	// WHEN
	got := d2d.Compute(legs)

	// THEN: rome2rio leg derived as indicative, not in ConfirmedTotal
	if got.ConfirmedTotal != 100.0 {
		t.Errorf("ConfirmedTotal = %.2f, want 100.00", got.ConfirmedTotal)
	}
	if len(got.IndicativeLegs) != 1 {
		t.Errorf("IndicativeLegs len = %d, want 1", len(got.IndicativeLegs))
	}
}

// TRVL.D2D.6 — mixed currency guard: legs in different currencies.
func TestCompute_mixedCurrency_flaggedAndPartialTotal(t *testing.T) {
	// GIVEN: EUR flight + USD hotel (different currencies)
	legs := []d2d.Leg{
		confirmedLeg("HEL", "JFK", 450.0, "EUR", "google_flights"),
		confirmedLeg("Manhattan", "Hotel Bed", 200.0, "USD", "booking.com"),
	}

	// WHEN
	got := d2d.Compute(legs)

	// THEN: MixedCurrency flagged; only EUR leg in ConfirmedTotal
	if !got.MixedCurrency {
		t.Error("MixedCurrency should be true for EUR + USD trip")
	}
	// Headline currency is EUR (first confirmed leg)
	if got.Currency != "EUR" {
		t.Errorf("Currency = %q, want \"EUR\"", got.Currency)
	}
	if got.ConfirmedTotal != 450.0 {
		t.Errorf("ConfirmedTotal = %.2f, want 450.00 (only EUR leg)", got.ConfirmedTotal)
	}
	if len(got.ExcludedCurrencyLegs) != 1 {
		t.Errorf("ExcludedCurrencyLegs len = %d, want 1", len(got.ExcludedCurrencyLegs))
	}
	if got.ExcludedCurrencyLegs[0].Currency != "USD" {
		t.Errorf("ExcludedCurrencyLegs[0].Currency = %q, want \"USD\"", got.ExcludedCurrencyLegs[0].Currency)
	}
	// Band is at least Narrow (currency exclusion)
	if got.Band == d2d.BandExact {
		t.Error("Band should not be Exact when currencies are mixed")
	}
}

// TRVL.D2D.7 — all-indicative trip: ConfirmedTotal = 0, all legs in IndicativeLegs.
func TestCompute_allIndicative_confirmedTotalIsZero(t *testing.T) {
	legs := []d2d.Leg{
		indicativeLeg("A", "B", 50.0, "EUR", "rome2rio"),
		indicativeLeg("B", "C", 30.0, "EUR", "rome2rio"),
	}
	got := d2d.Compute(legs)
	if got.ConfirmedTotal != 0 {
		t.Errorf("ConfirmedTotal = %.2f, want 0.00", got.ConfirmedTotal)
	}
	if len(got.IndicativeLegs) != 2 {
		t.Errorf("IndicativeLegs len = %d, want 2", len(got.IndicativeLegs))
	}
	if got.Band != d2d.BandWide {
		t.Errorf("Band = %q, want %q", got.Band, d2d.BandWide)
	}
}

// TRVL.D2D.8 — empty legs slice: returns zero Total with BandUnknown.
func TestCompute_emptyLegs_zeroTotalBandUnknown(t *testing.T) {
	got := d2d.Compute(nil)
	if got.ConfirmedTotal != 0 {
		t.Errorf("ConfirmedTotal = %.2f, want 0", got.ConfirmedTotal)
	}
	if got.Band != d2d.BandUnknown {
		t.Errorf("Band = %q, want %q", got.Band, d2d.BandUnknown)
	}
}

// TRVL.D2D.9 — band high >= confirmed total in all cases.
func TestCompute_bandHighAlwaysGEConfirmedTotal(t *testing.T) {
	cases := []struct {
		name string
		legs []d2d.Leg
	}{
		{"all confirmed", []d2d.Leg{confirmedLeg("A", "B", 100, "EUR", "src")}},
		{"with indicative", []d2d.Leg{
			confirmedLeg("A", "B", 100, "EUR", "src"),
			indicativeLeg("B", "C", 50, "EUR", "rome2rio"),
		}},
		{"with unverified", []d2d.Leg{
			confirmedLeg("A", "B", 100, "EUR", "src"),
			unverifiedLeg("B", "C", 20, "EUR"),
		}},
		{"mixed currency", []d2d.Leg{
			confirmedLeg("A", "B", 100, "EUR", "src"),
			confirmedLeg("C", "D", 200, "USD", "src2"),
		}},
	}
	for _, tc := range cases {
		got := d2d.Compute(tc.legs)
		if got.BandHigh < got.ConfirmedTotal {
			t.Errorf("[%s] BandHigh=%.2f < ConfirmedTotal=%.2f", tc.name, got.BandHigh, got.ConfirmedTotal)
		}
	}
}

// TRVL.D2D.10 — explicit Verified leg takes precedence over source mapping.
func TestCompute_explicitVerificationOverridesSource(t *testing.T) {
	// GIVEN: leg explicitly marked Confirmed even though source is rome2rio
	legs := []d2d.Leg{
		{
			Mode: "bus", From: "A", To: "B",
			Price: 40.0, Currency: "EUR",
			Source:       "rome2rio",
			Verification: d2d.Confirmed, // caller explicitly confirmed it
		},
	}
	got := d2d.Compute(legs)
	if got.ConfirmedTotal != 40.0 {
		t.Errorf("ConfirmedTotal = %.2f, want 40.00 (explicit Confirmed beats source mapping)", got.ConfirmedTotal)
	}
	if len(got.IndicativeLegs) != 0 {
		t.Error("IndicativeLegs should be empty when leg is explicitly Confirmed")
	}
}
