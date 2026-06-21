package watch

import (
	"testing"
	"time"
)

func TestRouteKeyCanonical(t *testing.T) {
	got := RouteKey("flight", " ams ", "vlc", "2026-07-15")
	want := "FLIGHT|AMS|VLC|2026-07-15"
	if got != want {
		t.Fatalf("RouteKey = %q, want %q", got, want)
	}
}

func TestRecordObservationAndRouteHistory(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	key := RouteKey("flight", "AMS", "VLC", "2026-07-15")
	for _, p := range []float64{120, 110, 130} {
		if err := s.RecordObservation(key, p, "EUR"); err != nil {
			t.Fatalf("RecordObservation: %v", err)
		}
	}
	// Non-positive and empty-key observations are dropped silently.
	if err := s.RecordObservation(key, 0, "EUR"); err != nil {
		t.Fatalf("zero price should be a no-op, got %v", err)
	}
	if err := s.RecordObservation("", 100, "EUR"); err != nil {
		t.Fatalf("empty key should be a no-op, got %v", err)
	}

	hist := s.RouteHistory(key)
	if len(hist) != 3 {
		t.Fatalf("want 3 route points, got %d", len(hist))
	}
	prices := s.RoutePrices(key, "EUR")
	if len(prices) != 3 || prices[0] != 120 || prices[2] != 130 {
		t.Fatalf("RoutePrices wrong: %v", prices)
	}

	// Route observations must not leak into the watch-ID corpus and vice versa.
	if got := s.History("some-watch-id"); len(got) != 0 {
		t.Fatalf("route obs leaked into watch history: %d", len(got))
	}

	// Round-trips through disk.
	s2 := NewStore(dir)
	if err := s2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := s2.RoutePrices(key, "EUR"); len(got) != 3 {
		t.Fatalf("after reload want 3, got %d", len(got))
	}
}

// P0 fix: RoutePrices must not mix currencies, or pricesignal bands are garbage.
func TestRoutePricesCurrencyFilter(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	key := RouteKey("flight", "AMS", "VLC", "2026-07-15")
	// Distinct prices so throttle never collapses them.
	_ = s.RecordObservation(key, 120, "EUR")
	_ = s.RecordObservation(key, 200, "USD")
	_ = s.RecordObservation(key, 130, "EUR")

	if eur := s.RoutePrices(key, "EUR"); len(eur) != 2 {
		t.Fatalf("want 2 EUR prices, got %d (%v)", len(eur), eur)
	}
	if usd := s.RoutePrices(key, "usd"); len(usd) != 1 { // case-insensitive
		t.Fatalf("want 1 USD price, got %d", len(usd))
	}
	if all := s.RoutePrices(key, ""); len(all) != 3 {
		t.Fatalf("empty currency must return all, got %d", len(all))
	}
}

// P0 fix: near-identical repeat observations within the throttle window are
// skipped, so rapid repeat searches do not each rewrite the file.
func TestRecordObservationThrottlesNearDuplicates(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	key := RouteKey("flight", "AMS", "VLC", "2026-07-15")

	_ = s.RecordObservation(key, 100, "EUR")
	_ = s.RecordObservation(key, 100.2, "EUR") // within 0.5% -> skipped
	if got := s.RoutePrices(key, "EUR"); len(got) != 1 {
		t.Fatalf("near-duplicate must be throttled, got %d points", len(got))
	}
	// A material move IS recorded.
	_ = s.RecordObservation(key, 130, "EUR")
	if got := s.RoutePrices(key, "EUR"); len(got) != 2 {
		t.Fatalf("material price move must be recorded, got %d", len(got))
	}
	// A different currency is recorded even at the same price (separate series).
	_ = s.RecordObservation(key, 130, "USD")
	if got := s.RoutePrices(key, "USD"); len(got) != 1 {
		t.Fatalf("different currency must record, got %d", len(got))
	}
}

// P0 fix: per-route cap bounds the history file.
func TestRecordObservationCapsPerRoute(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	key := RouteKey("flight", "AMS", "VLC", "2026-07-15")

	// Push well past the cap with strictly increasing prices (so throttle never
	// collapses them) using direct history seeding to keep the test fast.
	base := time.Now().Add(-time.Hour)
	for i := 0; i < maxObservationsPerRoute+50; i++ {
		s.history = append(s.history, PricePoint{
			RouteKey:  key,
			Price:     float64(100 + i),
			Currency:  "EUR",
			Timestamp: base.Add(time.Duration(i) * time.Second),
		})
	}
	s.pruneRouteLocked(key)
	got := s.RoutePrices(key, "EUR")
	if len(got) != maxObservationsPerRoute {
		t.Fatalf("want cap %d, got %d", maxObservationsPerRoute, len(got))
	}
	// Oldest dropped: first retained price should be the (50+1)th cheapest.
	if got[0] != float64(100+50) {
		t.Fatalf("oldest 50 should be pruned; first kept = %v", got[0])
	}
}
