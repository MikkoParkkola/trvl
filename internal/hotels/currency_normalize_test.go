package hotels

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestEnsureComparableInTargetCurrency_ResyncsAfterPriceRefresh is the §12
// regression: FinalizeHotelPriceTrust can bump a target-currency hotel's Price
// (EUR80 lead-in -> EUR200 verified room total) without touching ComparablePrice.
// The invariant re-assert must re-sync ComparablePrice to the fresh Price, or the
// stale value makes ranking and the headline crown the wrong hotel.
func TestEnsureComparableInTargetCurrency_ResyncsAfterPriceRefresh(t *testing.T) {
	hotels := []models.HotelResult{
		{Name: "Refreshed", Price: 200, Currency: "EUR", ComparablePrice: 80}, // stale 80
		{Name: "Cheaper", Price: 100, Currency: "EUR", ComparablePrice: 100},
	}
	ensureComparableInTargetCurrency(hotels, "EUR")
	if hotels[0].ComparablePrice != 200 {
		t.Fatalf("stale ComparablePrice not re-synced: got %v, want 200", hotels[0].ComparablePrice)
	}
	if hotels[1].ComparablePrice != 100 {
		t.Fatalf("in-sync ComparablePrice disturbed: got %v, want 100", hotels[1].ComparablePrice)
	}
}

// TestEnsureComparableInTargetCurrency_LeavesForeignTail confirms a hotel whose
// currency differs from the target keeps whatever ComparablePrice normalization
// gave it (0 for an FX failure) rather than copying a foreign raw price in.
func TestEnsureComparableInTargetCurrency_LeavesForeignTail(t *testing.T) {
	hotels := []models.HotelResult{
		{Name: "Yen", Price: 50, Currency: "JPY", ComparablePrice: 0},
	}
	ensureComparableInTargetCurrency(hotels, "EUR")
	if hotels[0].ComparablePrice != 0 {
		t.Fatalf("foreign tail must stay incomparable: got %v, want 0", hotels[0].ComparablePrice)
	}
}

// TestEnsureComparableInTargetCurrency_ZerosStaleForeignComparable is the
// finalize-flip regression: a hotel normalized to the target (EUR90,
// ComparablePrice=90) can be flipped by FinalizeHotelPriceTrust back to a verified
// foreign source (USD200), leaving a stale target-currency ComparablePrice. Since
// its authoritative price is now foreign, the stale comparable must be zeroed so it
// cannot win a cross-currency "cheapest" headline while displaying USD200.
func TestEnsureComparableInTargetCurrency_ZerosStaleForeignComparable(t *testing.T) {
	hotels := []models.HotelResult{
		{Name: "Flipped", Price: 200, Currency: "USD", ComparablePrice: 90}, // stale EUR comparable
		{Name: "Native", Price: 100, Currency: "EUR", ComparablePrice: 100},
	}
	ensureComparableInTargetCurrency(hotels, "EUR")
	if hotels[0].ComparablePrice != 0 {
		t.Fatalf("stale foreign comparable must be zeroed: got %v, want 0", hotels[0].ComparablePrice)
	}
	if hotels[1].ComparablePrice != 100 {
		t.Fatalf("target-currency comparable disturbed: got %v, want 100", hotels[1].ComparablePrice)
	}
}
