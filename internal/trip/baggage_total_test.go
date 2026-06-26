package trip

import "testing"

// TestComparableOrPrice asserts the plan uses the baggage-inclusive all-in fare
// when present, and falls back to the headline fare when no surcharge applies.
// This is the figure GrandTotal is summed from, so the fallback must be exact
// (not zero) when ComparablePrice is unset — otherwise a plan would quote a
// flight total of 0 for any carrier with no bag surcharge.
func TestComparableOrPrice(t *testing.T) {
	allIn := comparableOrPrice(PlanFlight{Price: 100, ComparablePrice: 135})
	if allIn != 135 {
		t.Errorf("with ComparablePrice set, want 135 (all-in), got %v", allIn)
	}

	headlineOnly := comparableOrPrice(PlanFlight{Price: 100})
	if headlineOnly != 100 {
		t.Errorf("without ComparablePrice, want fallback to headline 100, got %v", headlineOnly)
	}

	// A non-positive ComparablePrice must not be trusted over the headline.
	if got := comparableOrPrice(PlanFlight{Price: 80, ComparablePrice: 0}); got != 80 {
		t.Errorf("zero ComparablePrice should fall back to headline 80, got %v", got)
	}
}
