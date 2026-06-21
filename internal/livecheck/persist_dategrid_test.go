package livecheck

import (
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/dategrid"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// fixedNow is a deterministic timestamp used across grid tests.
var fixedNow = time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)

// newTempStore returns a fresh Store backed by t.TempDir() and panics if Load
// fails. Every test gets an isolated directory; no ~/.trvl access.
func newTempStore(t *testing.T) *dategrid.Store {
	t.Helper()
	s := dategrid.NewStore(t.TempDir())
	if err := s.Load(); err != nil {
		t.Fatalf("Store.Load: %v", err)
	}
	return s
}

// TestPersistDateGridTo_WritesPositivePricesOnly asserts that non-positive
// prices are skipped and valid points are persisted under the correct key.
func TestPersistDateGridTo_WritesPositivePricesOnly(t *testing.T) {
	t.Parallel()

	// GIVEN: a store, a route, and a mix of positive, zero, and negative prices.
	store := newTempStore(t)
	origin, destination := "HEL", "BCN"
	dates := []models.DatePriceResult{
		{Date: "2026-08-01", Price: 0, Currency: "EUR"},    // zero — must be skipped
		{Date: "2026-08-02", Price: -5.0, Currency: "EUR"}, // negative — must be skipped
		{Date: "2026-08-03", Price: 120.50, Currency: "EUR"},
		{Date: "2026-08-04", Price: 98.00, Currency: "EUR", ReturnDate: "2026-08-11"},
	}

	// WHEN: the grid is persisted.
	persistDateGridTo(store, origin, destination, dates, fixedNow)

	// THEN: the grid exists and contains exactly the two positive-price points.
	grid, ok := store.Get(dategrid.RouteKey(origin, destination))
	if !ok {
		t.Fatal("expected grid to be stored, got nothing")
	}
	if got, want := len(grid.Points), 2; got != want {
		t.Errorf("len(Points) = %d, want %d", got, want)
	}
}

// TestPersistDateGridTo_CurrencyFromFirstPositive asserts that the grid
// currency is taken from the first entry with a positive price.
func TestPersistDateGridTo_CurrencyFromFirstPositive(t *testing.T) {
	t.Parallel()

	// GIVEN: the first two entries are non-positive; the first positive one uses EUR.
	store := newTempStore(t)
	dates := []models.DatePriceResult{
		{Date: "2026-08-01", Price: 0, Currency: "USD"},
		{Date: "2026-08-02", Price: 89.0, Currency: "EUR"},
		{Date: "2026-08-03", Price: 75.0, Currency: "GBP"},
	}

	// WHEN
	persistDateGridTo(store, "ARN", "LHR", dates, fixedNow)

	// THEN: grid currency = EUR (first positive entry), not USD or GBP.
	grid, ok := store.Get(dategrid.RouteKey("ARN", "LHR"))
	if !ok {
		t.Fatal("expected grid to be stored")
	}
	if got, want := grid.Currency, "EUR"; got != want {
		t.Errorf("Currency = %q, want %q", got, want)
	}
}

// TestPersistDateGridTo_PointFieldsRoundTrip asserts that Date, ReturnDate,
// Price, and Currency are preserved verbatim through Store.Put / Store.Get.
func TestPersistDateGridTo_PointFieldsRoundTrip(t *testing.T) {
	t.Parallel()

	// GIVEN: a single positive entry with a return date.
	store := newTempStore(t)
	want := models.DatePriceResult{
		Date:       "2026-09-10",
		ReturnDate: "2026-09-17",
		Price:      210.99,
		Currency:   "EUR",
	}

	// WHEN
	persistDateGridTo(store, "HEL", "JFK", []models.DatePriceResult{want}, fixedNow)

	// THEN: the persisted point matches the input exactly.
	grid, ok := store.Get(dategrid.RouteKey("HEL", "JFK"))
	if !ok {
		t.Fatal("expected grid to be stored")
	}
	if len(grid.Points) != 1 {
		t.Fatalf("len(Points) = %d, want 1", len(grid.Points))
	}
	p := grid.Points[0]
	if p.Date != want.Date {
		t.Errorf("Date = %q, want %q", p.Date, want.Date)
	}
	if p.ReturnDate != want.ReturnDate {
		t.Errorf("ReturnDate = %q, want %q", p.ReturnDate, want.ReturnDate)
	}
	if p.Price != want.Price {
		t.Errorf("Price = %f, want %f", p.Price, want.Price)
	}
	if p.Currency != want.Currency {
		t.Errorf("Currency = %q, want %q", p.Currency, want.Currency)
	}
}

// TestPersistDateGridTo_AllNonPositive asserts that when every price is
// non-positive, Put receives an empty slice and the grid is absent (Store.Put
// is a no-op for empty slices).
func TestPersistDateGridTo_AllNonPositive(t *testing.T) {
	t.Parallel()

	// GIVEN: only zero and negative prices.
	store := newTempStore(t)
	dates := []models.DatePriceResult{
		{Date: "2026-08-01", Price: 0, Currency: "EUR"},
		{Date: "2026-08-02", Price: -1, Currency: "EUR"},
	}

	// WHEN
	persistDateGridTo(store, "TLL", "VIE", dates, fixedNow)

	// THEN: no grid is stored.
	_, ok := store.Get(dategrid.RouteKey("TLL", "VIE"))
	if ok {
		t.Error("expected no grid for all-non-positive prices, but found one")
	}
}

// TestPersistDateGridTo_UpdatedAtMatchesNow asserts that the UpdatedAt
// timestamp matches the explicit `now` argument passed by the caller.
func TestPersistDateGridTo_UpdatedAtMatchesNow(t *testing.T) {
	t.Parallel()

	// GIVEN
	store := newTempStore(t)
	dates := []models.DatePriceResult{
		{Date: "2026-10-01", Price: 55.0, Currency: "EUR"},
	}

	// WHEN
	persistDateGridTo(store, "RIX", "MAD", dates, fixedNow)

	// THEN: UpdatedAt is the explicit now, not a real wall-clock time.
	grid, ok := store.Get(dategrid.RouteKey("RIX", "MAD"))
	if !ok {
		t.Fatal("expected grid to be stored")
	}
	if !grid.UpdatedAt.Equal(fixedNow) {
		t.Errorf("UpdatedAt = %v, want %v", grid.UpdatedAt, fixedNow)
	}
}
