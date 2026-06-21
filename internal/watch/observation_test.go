package watch

import "testing"

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
	prices := s.RoutePrices(key)
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
	if got := s2.RoutePrices(key); len(got) != 3 {
		t.Fatalf("after reload want 3, got %d", len(got))
	}
}
