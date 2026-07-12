package pricefeed

import (
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/booking"
	"github.com/MikkoParkkola/trvl/internal/counterfactual"
	"github.com/MikkoParkkola/trvl/internal/dategrid"
	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/probecache"
)

var now = time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)

// Regression for the coupling fix: shift-day and Tier-1 savings come from the
// grid and probe caches, NOT the watch store. With an empty watch store (no
// price_position), those cache-backed savings must still be served — proving the
// savings are independent of the watch store.
func TestFlightServesCacheSavingsIndependentOfWatchStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	gs, err := dategrid.DefaultStore()
	if err != nil {
		t.Fatal(err)
	}
	_ = gs.Load()
	_ = gs.Put(dategrid.RouteKey("AMS", "VLC"), "EUR", []dategrid.Point{
		{Date: "2026-07-14", Price: 90, Currency: "EUR"},
		{Date: "2026-07-15", Price: 150, Currency: "EUR"},
	}, now)

	ps, err := probecache.DefaultStore()
	if err != nil {
		t.Fatal(err)
	}
	_ = ps.Load()
	_ = ps.Put(probecache.RouteKey("AMS", "VLC"), []counterfactual.Saving{
		{Kind: counterfactual.KindProbe, Description: "Fly from BGY", Amount: 40, Currency: "EUR", CallFree: true, AsOf: now},
	}, now)

	res := &models.FlightSearchResult{Success: true, Flights: []models.FlightResult{{Price: 150, Currency: "EUR"}}}
	out := Flight("AMS", "VLC", "2026-07-15", res, now)

	var hasShift, hasProbe bool
	for _, s := range out.Savings {
		switch s.Kind {
		case counterfactual.KindShiftDay:
			hasShift = true
		case counterfactual.KindProbe:
			hasProbe = true
		}
	}
	if !hasShift {
		t.Errorf("shift-day must be served from the grid regardless of the watch store; got %+v", out.Savings)
	}
	if !hasProbe {
		t.Errorf("Tier-1 must be served from the probe cache regardless of the watch store; got %+v", out.Savings)
	}
}

func TestFlightMultiAirportReturnsEmpty(t *testing.T) {
	res := &models.FlightSearchResult{Success: true, Flights: []models.FlightResult{{Price: 100, Currency: "EUR"}}}
	if out := Flight("", "", "2026-07-15", res, now); out.Position != nil || out.Savings != nil {
		t.Fatalf("multi-airport (empty O/D) must return empty, got %+v", out)
	}
}

func TestFlightSameDaySaving(t *testing.T) {
	// HOME isolation so it doesn't touch the real ~/.trvl.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	res := &models.FlightSearchResult{Success: true, Flights: []models.FlightResult{
		{Price: 220, Currency: "EUR"}, // headline
		{Price: 150, Currency: "EUR"}, // cheapest
	}}
	out := Flight("AMS", "VLC", "2026-07-15", res, now)
	found := false
	for _, s := range out.Savings {
		if s.Kind == counterfactualSameDay {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a same-day saving, got %+v", out.Savings)
	}
}

// counterfactualSameDay mirrors counterfactual.KindSameDay without importing it
// into the test (keeps the assertion readable).
const counterfactualSameDay = "same_day_alternative"

func TestHotelPricesReadinessNeverReady(t *testing.T) {
	// Even with the best price+link, prices endpoint lacks refundability → caution.
	v := HotelPricesReadiness("/g/x", []models.ProviderPrice{
		{Price: 100, Currency: "EUR", LinkDurability: "stable", PriceConfidence: models.PriceConfidenceVerified},
	})
	if v.Readiness == booking.Ready {
		t.Fatalf("prices endpoint must not reach ready (no refundability), got %s", v.Readiness)
	}
}

func TestRoomsReadinessReachable(t *testing.T) {
	refundable := true
	avail := &hotels.RoomAvailability{
		HotelID: "/g/x",
		Rooms: []hotels.RoomType{{
			Refundable:       &refundable,
			ProviderURL:      "https://www.booking.com/hotel/x.html",
			InventoryOptions: []models.RoomInventoryQuote{{PriceConfidence: models.PriceConfidenceVerified}},
		}},
	}
	if v := RoomsReadiness(avail); v.Readiness != booking.Ready {
		t.Fatalf("rooms with stable link + refundability + verified + identity must be ready, got %s (%v)", v.Readiness, v.Reasons)
	}
}

func TestRoomsReadinessExpiringDowngrades(t *testing.T) {
	refundable := true
	avail := &hotels.RoomAvailability{
		HotelID: "/g/x",
		Rooms: []hotels.RoomType{{
			Refundable:       &refundable,
			ProviderURL:      "https://www.google.com/aclk?adurl=x",
			InventoryOptions: []models.RoomInventoryQuote{{PriceConfidence: models.PriceConfidenceVerified}},
		}},
	}
	if v := RoomsReadiness(avail); v.Readiness == booking.Ready {
		t.Fatalf("expiring link must downgrade below ready")
	}
}

func TestRoomsReadinessNil(t *testing.T) {
	if v := RoomsReadiness(nil); v.Readiness == booking.Ready {
		t.Fatalf("nil availability must not be ready")
	}
}

func TestCheapestProvider(t *testing.T) {
	got := CheapestProvider([]models.ProviderPrice{{Price: 0}, {Price: 200, Currency: "EUR"}, {Price: 150, Currency: "EUR"}})
	if got.Price != 150 {
		t.Fatalf("want 150, got %v", got.Price)
	}
}

// TestCheapestProvider_ExcludesForeignCohort proves a nominally-smaller foreign
// price cannot win across currencies: with two EUR providers and one JPY, the
// dominant EUR cohort is ranked and the 5 JPY quote is excluded rather than
// compared against 80 EUR.
func TestCheapestProvider_ExcludesForeignCohort(t *testing.T) {
	got := CheapestProvider([]models.ProviderPrice{
		{Provider: "a", Price: 90, Currency: "EUR"},
		{Provider: "b", Price: 80, Currency: "EUR"},
		{Provider: "c", Price: 5, Currency: "JPY"},
	})
	if got.Price != 80 || got.Currency != "EUR" {
		t.Fatalf("want 80 EUR (JPY excluded), got %v %s", got.Price, got.Currency)
	}
}
