package pricefeed

import (
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/watch"
)

// TRVL.POSBLANK.2/.3 -- a blank currency must not produce a price-position.
//
// Position carries a buy/wait Verdict, so it is an active recommendation rather
// than a diagnostic. When the cheapest fare has no currency, RoutePrices is
// called with an empty argument, and since #564 that exact-matches: it selects
// every currencyless observation on the route. Those can be different real
// currencies -- one provider omitting the label on a EUR quote, another on a JPY
// one -- so the median, band and percentile are computed across incomparable
// numbers and shipped as advice.
//
// Unlike the blank-currency saving in #549, this one has no visible tell. That
// rendered "save 120 " with a conspicuous gap where the currency belonged; a
// position renders normally and reads as trustworthy.
//
// Seeded with a spread wide enough that a pooled median would be confidently
// wrong rather than accidentally harmless: prices from 80 to 400 with no
// currency, then a current fare of 100 that would land near the bottom of that
// invented distribution and read as a strong BUY.
func TestFlightRefusesAPositionWhenTheCheapestFareHasNoCurrency(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)

	store, err := watch.DefaultStore()
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if err := store.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	// Enough currencyless history for pricesignal to consider itself confident.
	key := watch.RouteKey("flight", "HEL", "NRT", "2026-09-01")
	for i := 0; i < 30; i++ {
		if err := store.RecordObservation(key, float64(80+(i*11)%320), ""); err != nil {
			t.Fatalf("seed observation %d: %v", i, err)
		}
	}

	res := &models.FlightSearchResult{
		Success: true,
		Flights: []models.FlightResult{
			{Price: 100}, // cheapest, and NO currency
			{Price: 260},
		},
	}

	got := Flight("HEL", "NRT", "2026-09-01", res, time.Now())

	if got.Position != nil {
		t.Errorf("a currencyless cheapest fare produced a price-position %+v -- its median comes "+
			"from a series that pools observations which may be different currencies, and Position "+
			"carries a buy/wait verdict, so this is an active recommendation computed across "+
			"incomparable numbers", got.Position)
	}
}

// The control that makes the assertion above mean something. A LABELLED fare on
// the same seeded history must still produce a position, so the fix refuses
// blankness rather than disabling price-position altogether.
func TestFlightStillProducesAPositionForALabelledFare(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)

	store, err := watch.DefaultStore()
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if err := store.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	key := watch.RouteKey("flight", "HEL", "NRT", "2026-09-01")
	for i := 0; i < 30; i++ {
		if err := store.RecordObservation(key, float64(80+(i*11)%320), "EUR"); err != nil {
			t.Fatalf("seed observation %d: %v", i, err)
		}
	}

	res := &models.FlightSearchResult{
		Success: true,
		Flights: []models.FlightResult{
			{Price: 100, Currency: "EUR"},
			{Price: 260, Currency: "EUR"},
		},
	}

	got := Flight("HEL", "NRT", "2026-09-01", res, time.Now())

	if got.Position == nil {
		t.Error("a labelled fare produced no price-position; the guard must refuse an unlabelled " +
			"currency, not switch the feature off")
	}
}

// TRVL.POSBLANK.4 -- the hotel path has the same shape and is fixed with it,
// rather than recorded as unaffected.
func TestHotelPositionRefusesABlankProviderCurrency(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)

	store, err := watch.DefaultStore()
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if err := store.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	key := watch.RouteKey("hotel", "hotel-xyz", "", "2026-09-01")
	for i := 0; i < 30; i++ {
		if err := store.RecordObservation(key, float64(120+(i*7)%200), ""); err != nil {
			t.Fatalf("seed observation %d: %v", i, err)
		}
	}

	res := &models.HotelPriceResult{
		Providers: []models.ProviderPrice{
			{Provider: "someota", Price: 130}, // no currency
		},
	}

	if pos := HotelPosition("hotel-xyz", "2026-09-01", res); pos != nil {
		t.Errorf("a currencyless provider price produced a hotel price-position %+v -- same defect "+
			"as the flight path, and it would ship the same buy/wait verdict", pos)
	}
}
