package obslog

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/watch"
)

type fakeRecorder struct {
	calls []call
	err   error
}

type call struct {
	key      string
	price    float64
	currency string
}

func (f *fakeRecorder) RecordObservation(key string, price float64, currency string) error {
	f.calls = append(f.calls, call{key, price, currency})
	return f.err
}

func TestFlightSearchLogsCheapest(t *testing.T) {
	r := &fakeRecorder{}
	res := &models.FlightSearchResult{Flights: []models.FlightResult{
		{Price: 200, Currency: "EUR"},
		{Price: 150, Currency: "EUR"},
		{Price: 175, Currency: "EUR"},
	}}
	if err := FlightSearch(r, "ams", "VLC", "2026-07-15", res); err != nil {
		t.Fatalf("FlightSearch: %v", err)
	}
	if len(r.calls) != 1 {
		t.Fatalf("want 1 record, got %d", len(r.calls))
	}
	c := r.calls[0]
	if c.price != 150 {
		t.Fatalf("want cheapest 150, got %v", c.price)
	}
	if c.key != watch.RouteKey("flight", "ams", "VLC", "2026-07-15") {
		t.Fatalf("unexpected key %q", c.key)
	}
}

func TestNoOpOnEmptyOrZero(t *testing.T) {
	r := &fakeRecorder{}
	if err := FlightSearch(r, "AMS", "VLC", "d", &models.FlightSearchResult{}); err != nil {
		t.Fatal(err)
	}
	if err := FlightSearch(r, "AMS", "VLC", "d", nil); err != nil {
		t.Fatal(err)
	}
	// All zero/negative prices -> no record.
	if err := FlightSearch(r, "AMS", "VLC", "d", &models.FlightSearchResult{
		Flights: []models.FlightResult{{Price: 0, Currency: "EUR"}},
	}); err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != 0 {
		t.Fatalf("expected no records, got %d", len(r.calls))
	}
	// Nil recorder must not panic.
	if err := FlightSearch(nil, "AMS", "VLC", "d", &models.FlightSearchResult{
		Flights: []models.FlightResult{{Price: 100, Currency: "EUR"}},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestHotelPricesLogsCheapest(t *testing.T) {
	r := &fakeRecorder{}
	res := &models.HotelPriceResult{Providers: []models.ProviderPrice{
		{Provider: "a", Price: 300, Currency: "EUR"},
		{Provider: "b", Price: 250, Currency: "EUR"},
	}}
	if err := HotelPrices(r, "/g/abc", "2026-07-15", res); err != nil {
		t.Fatalf("HotelPrices: %v", err)
	}
	if len(r.calls) != 1 || r.calls[0].price != 250 {
		t.Fatalf("want cheapest 250, got %+v", r.calls)
	}
	if r.calls[0].key != watch.RouteKey("hotel", "/g/abc", "", "2026-07-15") {
		t.Fatalf("unexpected key %q", r.calls[0].key)
	}
}

// Integration: obslog writes through a real Store and pricesignal can read it.
func TestEndToEndThroughStore(t *testing.T) {
	dir := t.TempDir()
	s := watch.NewStore(dir)
	for _, p := range []float64{120, 110, 130, 140, 100} {
		res := &models.FlightSearchResult{Flights: []models.FlightResult{{Price: p, Currency: "EUR"}}}
		if err := FlightSearch(s, "AMS", "VLC", "2026-07-15", res); err != nil {
			t.Fatal(err)
		}
	}
	prices := s.RoutePrices(watch.RouteKey("flight", "AMS", "VLC", "2026-07-15"), "EUR")
	if len(prices) != 5 {
		t.Fatalf("want 5 logged prices, got %d", len(prices))
	}
}
