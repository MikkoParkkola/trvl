package pricefeed

import (
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/booking"
	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
)

var now = time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)

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
