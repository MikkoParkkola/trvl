package pricefeed

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestCheapestHotel_ExcludesForeignCohort is the load-bearing honesty test:
// the foreign quote is nominally the SMALLEST number (JPY 50), so a naive
// raw-Price scan would crown it. The cohort guard must exclude JPY (minority
// currency) and pick the true dominant-cohort minimum (Beta, 90 EUR). If this
// test ever passes on an unguarded scan, the guard is not doing its job — so
// the foreign price must stay below the cohort minimum.
func TestCheapestHotel_ExcludesForeignCohort(t *testing.T) {
	hotels := []models.HotelResult{
		{Name: "Alpha", Price: 100, Currency: "EUR"},
		{Name: "Beta", Price: 90, Currency: "EUR"},
		{Name: "Gamma", Price: 50, Currency: "JPY"}, // nominally cheapest, dishonest
	}
	got := CheapestHotel(hotels)
	if got.Name != "Beta" || got.Price != 90 || got.Currency != "EUR" {
		t.Fatalf("want Beta 90 EUR (nominally-smaller JPY 50 excluded), got %+v", got)
	}
}

// TestCheapestHotel_MajorityForeignFXFailure is the §12 regression: the foreign
// hotels are BOTH the majority AND nominally cheaper, but their FX conversion
// failed (ComparablePrice == 0). A frequency-based cohort would crown JPY 50;
// the comparability cohort must pick the lone normalized EUR hotel.
func TestCheapestHotel_MajorityForeignFXFailure(t *testing.T) {
	hotels := []models.HotelResult{
		{Name: "Euro", Price: 90, Currency: "EUR", ComparablePrice: 90},
		{Name: "YenA", Price: 50, Currency: "JPY"},
		{Name: "YenB", Price: 60, Currency: "JPY"},
	}
	got := CheapestHotel(hotels)
	if got.Name != "Euro" || got.ComparablePrice != 90 {
		t.Fatalf("want Euro (comparable cohort, not majority JPY), got %+v", got)
	}
}

// TestCheapestHotel_PicksMinComparable: among normalized hotels the pick is the
// lowest ComparablePrice; an FX-failed foreign quote is excluded even if raw
// price is smaller.
func TestCheapestHotel_PicksMinComparable(t *testing.T) {
	hotels := []models.HotelResult{
		{Name: "A", Price: 120, Currency: "EUR", ComparablePrice: 120},
		{Name: "B", Price: 95, Currency: "EUR", ComparablePrice: 95},
		{Name: "C", Price: 40, Currency: "JPY"}, // FX failed, ComparablePrice 0
	}
	got := CheapestHotel(hotels)
	if got.Name != "B" || got.ComparablePrice != 95 {
		t.Fatalf("want B 95 (min comparable, JPY 40 excluded), got %+v", got)
	}
}

func TestCheapestHotel_Empty(t *testing.T) {
	got := CheapestHotel(nil)
	if got.Price != 0 || got.Name != "" || got.Currency != "" {
		t.Fatalf("nil slice must yield zero-value HotelResult, got %+v", got)
	}
}

func TestCheapestHotel_SingleCurrency(t *testing.T) {
	hotels := []models.HotelResult{
		{Name: "A", Price: 120, Currency: "USD"},
		{Name: "B", Price: 110, Currency: "USD"},
	}
	got := CheapestHotel(hotels)
	if got.Name != "B" || got.Price != 110 || got.Currency != "USD" {
		t.Fatalf("want B 110 USD, got %+v", got)
	}
}

func TestCheapestHotel_SkipsNonPositive(t *testing.T) {
	hotels := []models.HotelResult{
		{Name: "Zero", Price: 0, Currency: "EUR"},
		{Name: "Neg", Price: -5, Currency: "EUR"},
		{Name: "Valid", Price: 65, Currency: "EUR"},
	}
	got := CheapestHotel(hotels)
	if got.Name != "Valid" || got.Price != 65 {
		t.Fatalf("want Valid 65 (zero/neg skipped), got %+v", got)
	}
}

// TestCheapestHotel_NoCurrencyFallback: when no hotel carries a currency there
// is no cohort, so the guard falls back to the lowest positive price.
func TestCheapestHotel_NoCurrencyFallback(t *testing.T) {
	hotels := []models.HotelResult{
		{Name: "X", Price: 80},
		{Name: "Y", Price: 70},
	}
	got := CheapestHotel(hotels)
	if got.Name != "Y" || got.Price != 70 {
		t.Fatalf("want Y 70 (no-cohort fallback to lowest), got %+v", got)
	}
}
