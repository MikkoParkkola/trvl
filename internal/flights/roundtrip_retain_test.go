package flights

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

func nativeRT(price float64, outDep, inDep string) models.FlightResult {
	return nativeRTCur(price, "EUR", outDep, inDep)
}

func nativeRTCur(price float64, currency, outDep, inDep string) models.FlightResult {
	return models.FlightResult{
		Price:    price,
		Currency: currency,
		FareType: models.FareRoundTrip,
		Legs: []models.FlightLeg{
			{Direction: "outbound", DepartureTime: outDep},
			{Direction: "inbound", DepartureTime: inDep},
		},
	}
}

func composedRT(price float64, stops int) models.FlightResult {
	return composedRTCur(price, "EUR", stops)
}

func composedRTCur(price float64, currency string, stops int) models.FlightResult {
	return models.FlightResult{
		Price:    price,
		Currency: currency,
		Stops:    stops,
		FareType: models.FareSplitTickets,
		Legs:     []models.FlightLeg{{Direction: "outbound", DepartureTime: "2026-07-21T08:00"}},
	}
}

// assertPriceSorted fails the test if results is not cheapest-first per
// compareFlightPrices/PriceForRanking — the invariant every caller of
// retainCompliantNativeRoundTrip relies on, and the one #472's fixtures used
// to violate silently (masking the order-corruption bug the sort now fixes).
func assertPriceSorted(t *testing.T, label string, results []models.FlightResult) {
	t.Helper()
	for i := 1; i < len(results); i++ {
		if compareFlightPrices(results[i-1].PriceForRanking(), results[i].PriceForRanking()) > 0 {
			t.Errorf("%s: not price-sorted at index %d: %.0f before %.0f", label, i, results[i-1].PriceForRanking(), results[i].PriceForRanking())
		}
	}
}

// #472: the cheapest window-compliant native round-trip is retained through
// truncation even when it is priced beyond the cutoff, displacing the most
// expensive kept slot — so it survives for the later window filter. The
// displaced-in fare must not corrupt the cheapest-first order of the result.
func TestRetainCompliantNativeRoundTrip_RetainsBeyondCutoff(t *testing.T) {
	merged := []models.FlightResult{
		nativeRT(263, "2026-07-21T11:00", "2026-07-25T07:00"), // cheapest, return 07:00 -> non-compliant
		nativeRT(274, "2026-07-21T09:45", "2026-07-25T07:00"), // non-compliant
		composedRT(300, 2),
		composedRT(310, 1),
		nativeRT(340, "2026-07-21T10:15", "2026-07-25T13:55"), // compliant, priciest -> beyond cutoff
	}
	assertPriceSorted(t, "fixture", merged)

	got := retainCompliantNativeRoundTrip(merged, "10:00", "", 4)
	assertPriceSorted(t, "result", got)
	if len(got) != 4 {
		t.Fatalf("expected 4, got %d", len(got))
	}
	// The compliant €340 fare must be present despite being the 5th cheapest.
	found := false
	for _, f := range got {
		if f.Price == 340 {
			found = true
		}
	}
	if !found {
		t.Errorf("compliant native RT (340) was truncated away: %+v", got)
	}
	// It should occupy the displaced last slot; the €310 composed leaves.
	if got[3].Price != 340 {
		t.Errorf("expected 340 in last slot, got %.0f", got[3].Price)
	}
}

// #472: when a compliant native round-trip already survives the cut, the list is
// a plain cheapest-max truncation (no displacement).
func TestRetainCompliantNativeRoundTrip_AlreadyPresent(t *testing.T) {
	merged := []models.FlightResult{
		nativeRT(263, "2026-07-21T11:00", "2026-07-25T12:00"), // compliant, cheapest
		composedRT(300, 2),
		composedRT(310, 1),
	}
	assertPriceSorted(t, "fixture", merged)

	got := retainCompliantNativeRoundTrip(merged, "10:00", "", 2)
	assertPriceSorted(t, "result", got)
	if len(got) != 2 || got[0].Price != 263 || got[1].Price != 300 {
		t.Errorf("expected plain truncation [263,300], got %+v", got)
	}
}

// #472: with no departure-time window, retention is a plain cheapest-max cut.
func TestRetainCompliantNativeRoundTrip_NoWindow(t *testing.T) {
	merged := []models.FlightResult{
		nativeRT(290, "2026-07-21T05:00", "2026-07-25T05:00"),
		composedRT(300, 2),
		composedRT(310, 1),
	}
	assertPriceSorted(t, "fixture", merged)

	got := retainCompliantNativeRoundTrip(merged, "", "", 2)
	assertPriceSorted(t, "result", got)
	if len(got) != 2 || got[0].Price != 290 || got[1].Price != 300 {
		t.Errorf("no window: expected [290,300], got %+v", got)
	}
}

// #472: nothing to retain when no native fare is window-compliant -> plain cut.
// The non-compliant native is priced above the cutoff (320) so the fixture
// stays genuinely sorted while still exercising the "candidate exists beyond
// the cut but fails the window check" path.
func TestRetainCompliantNativeRoundTrip_NoneCompliant(t *testing.T) {
	merged := []models.FlightResult{
		composedRT(300, 2),
		composedRT(310, 1),
		nativeRT(320, "2026-07-21T11:00", "2026-07-25T07:00"), // return 07:00 -> non-compliant
	}
	assertPriceSorted(t, "fixture", merged)

	got := retainCompliantNativeRoundTrip(merged, "10:00", "", 2)
	assertPriceSorted(t, "result", got)
	if len(got) != 2 || got[0].Price != 300 || got[1].Price != 310 {
		t.Errorf("none compliant: expected plain [300,310], got %+v", got)
	}
}

// #472 defect #4: every fixture above used EUR, so displacement/re-sort logic
// could scramble a Price/Currency pairing (e.g. relabel a swapped-in fare with
// the wrong currency) without any test catching it. This fare represents a
// native round-trip that normalizeFlightCurrencies successfully converted to
// the search's target currency before this function ever sees it — Price and
// Currency must survive the displacement swap and the following re-sort as a
// matched pair.
func TestRetainCompliantNativeRoundTrip_MixedCurrencyConvertedFareKeepsPairing(t *testing.T) {
	merged := []models.FlightResult{
		nativeRT(263, "2026-07-21T11:00", "2026-07-25T07:00"), // non-compliant
		nativeRT(274, "2026-07-21T09:45", "2026-07-25T07:00"), // non-compliant
		composedRT(300, 2),
		composedRT(310, 1),
		// originally quoted ~370 USD; normalizeFlightCurrencies converted it to
		// 340 EUR before merge — represented here as its post-conversion state.
		nativeRTCur(340, "EUR", "2026-07-21T10:15", "2026-07-25T13:55"),
	}
	assertPriceSorted(t, "fixture", merged)

	got := retainCompliantNativeRoundTrip(merged, "10:00", "", 4)
	assertPriceSorted(t, "result", got)
	if len(got) != 4 {
		t.Fatalf("expected 4, got %d", len(got))
	}
	if got[3].Price != 340 || got[3].Currency != "EUR" {
		t.Errorf("converted fare must keep its Price/Currency pairing, got price=%.0f currency=%q", got[3].Price, got[3].Currency)
	}
	for _, f := range got[:3] {
		if f.Currency != "EUR" {
			t.Errorf("unrelated entry (price=%.0f) picked up the wrong currency %q; displacement must not cross-contaminate", f.Price, f.Currency)
		}
	}
}

// #472 defect #4: normalizeFlightCurrencies leaves Price and Currency
// untouched when conversion fails (documented fallback contract). This fare
// simulates that failure — an unrecognised code that never got converted to
// the search's target currency. retainCompliantNativeRoundTrip must carry it
// through honestly: the numeric price must never be silently relabeled to a
// different currency, and it must not bleed its currency onto neighbours.
func TestRetainCompliantNativeRoundTrip_InconvertibleFareStaysHonest(t *testing.T) {
	merged := []models.FlightResult{
		nativeRT(263, "2026-07-21T11:00", "2026-07-25T07:00"), // non-compliant
		nativeRT(274, "2026-07-21T09:45", "2026-07-25T07:00"), // non-compliant
		composedRT(300, 2),
		composedRT(310, 1),
		// conversion to the target currency failed (unsupported/unknown code);
		// Price and Currency were left exactly as quoted, per the documented
		// fallback contract.
		nativeRTCur(450, "XXX", "2026-07-21T10:15", "2026-07-25T13:55"),
	}
	assertPriceSorted(t, "fixture", merged)

	got := retainCompliantNativeRoundTrip(merged, "10:00", "", 4)
	assertPriceSorted(t, "result", got)
	if len(got) != 4 {
		t.Fatalf("expected 4, got %d", len(got))
	}
	if got[3].Price != 450 || got[3].Currency != "XXX" {
		t.Errorf("inconvertible fare must keep its original price+currency untouched, got price=%.0f currency=%q", got[3].Price, got[3].Currency)
	}
	for _, f := range got[:3] {
		if f.Currency != "EUR" {
			t.Errorf("unrelated entry (price=%.0f) picked up the inconvertible currency %q; displacement must not cross-contaminate", f.Price, f.Currency)
		}
	}
}
