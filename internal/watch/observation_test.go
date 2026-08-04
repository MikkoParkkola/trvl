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
	// An empty currency selects the CURRENCYLESS records, not every record.
	//
	// This assertion previously read "empty currency must return all" and
	// expected 3 -- which contradicts this test's own stated purpose one line
	// above it. Returning all is precisely the mixing that makes pricesignal
	// bands garbage: a provider result carrying no currency produced a series
	// spanning EUR and USD magnitudes, and the percentile a user is shown was
	// computed across incomparable numbers (trvl#564).
	if none := s.RoutePrices(key, ""); len(none) != 0 {
		t.Fatalf("empty currency returned %d labelled price(s) (%v); it must not act as a wildcard",
			len(none), none)
	}

	// ...and it is selective, not merely always empty: a currencyless
	// observation is still recorded and still retrievable on its own terms.
	_ = s.RecordObservation(key, 999, "")
	if none := s.RoutePrices(key, ""); len(none) != 1 {
		t.Fatalf("want the 1 currencyless price, got %d", len(none))
	}
	if eur := s.RoutePrices(key, "EUR"); len(eur) != 2 {
		t.Fatalf("the currencyless observation leaked into the EUR series: got %d, want 2", len(eur))
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

// improve pass: the global route-keyed cap bounds total ad-hoc points across
// many routes while never evicting watch-keyed history.
func TestGlobalRouteCapPreservesWatchHistory(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	// Watch-keyed history that must survive eviction.
	for i := 0; i < 5; i++ {
		_ = s.RecordPrice("watch-1", float64(100+i), "EUR")
	}
	// Route-keyed points seeded well past the global cap.
	base := time.Now().Add(-time.Hour)
	for i := 0; i < maxRouteObservations+100; i++ {
		s.history = append(s.history, PricePoint{
			RouteKey:  "FLIGHT|AMS|VLC|2026-07-15",
			Price:     float64(100 + i),
			Currency:  "EUR",
			Timestamp: base.Add(time.Duration(i) * time.Second),
		})
	}
	s.pruneGlobalRouteLocked()

	routeCount := 0
	for _, p := range s.history {
		if p.RouteKey != "" && p.WatchID == "" {
			routeCount++
		}
	}
	if routeCount != maxRouteObservations {
		t.Fatalf("route-keyed points = %d, want cap %d", routeCount, maxRouteObservations)
	}
	if got := len(s.History("watch-1")); got != 5 {
		t.Fatalf("watch history must be preserved, got %d want 5", got)
	}
}

// TRVL.OBS.CURRENCY.1 -- the throttle must not compare magnitudes across
// currencies.
//
// RecordObservation skips a near-duplicate within the throttle window by
// comparing |price-last|/last against an epsilon, finding `last` via
// lastObservationLocked. An empty currency argument used to match ANY currency,
// so recording a CURRENCYLESS observation looked up the most recent LABELLED
// one and compared against it. A currencyless quote that happened to sit near
// some unrelated currency's magnitude was silently discarded -- and the ratio
// driving that decision was meaningless (trvl#564).
//
// The direction matters, which is why an earlier version of this test was
// vacuous: two LABELLED observations never trigger it, because a non-empty
// query filters exactly. Only an empty query reached the wildcard.
func TestRecordObservationThrottleDoesNotCompareAcrossCurrencies(t *testing.T) {
	s := NewStore(t.TempDir())
	key := RouteKey("flight", "HEL", "NRT", "2027-05-01")

	if err := s.RecordObservation(key, 180, "EUR"); err != nil {
		t.Fatalf("seed EUR: %v", err)
	}
	// A currencyless observation at a near-identical MAGNITUDE -- within the
	// 0.5% epsilon of 180, deliberately, since a delta outside it is not
	// throttled by anything and the test would prove nothing. Recorded
	// immediately after, so it is inside the throttle window. There is no
	// currencyless history at all, so nothing legitimate can suppress it: only
	// a comparison against the EUR point can.
	if err := s.RecordObservation(key, 180.5, ""); err != nil {
		t.Fatalf("record currencyless: %v", err)
	}

	if none := s.RoutePrices(key, ""); len(none) != 1 {
		t.Errorf("the currencyless observation was dropped (%d recorded): the throttle compared it "+
			"against a EUR price, which is not a comparison that means anything", len(none))
	}
	if eur := s.RoutePrices(key, "EUR"); len(eur) != 1 {
		t.Errorf("the EUR observation is missing or was joined by the currencyless one: %d recorded, want 1", len(eur))
	}
}
