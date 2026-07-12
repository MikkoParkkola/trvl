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
