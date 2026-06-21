package probecache

import (
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/counterfactual"
)

func TestPutGetRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	key := RouteKey("ams", "VLC")

	sav := []counterfactual.Saving{
		{Kind: counterfactual.KindProbe, Description: "Fly from BGY", Amount: 40, Currency: "EUR", AsOf: now},
	}
	if err := s.Put(key, sav, now); err != nil {
		t.Fatalf("Put: %v", err)
	}

	s2 := NewStore(dir)
	if err := s2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	e, ok := s2.Get(key)
	if !ok || len(e.Savings) != 1 || e.Savings[0].Amount != 40 {
		t.Fatalf("round-trip failed: %+v ok=%v", e, ok)
	}
	if !e.UpdatedAt.Equal(now) {
		t.Fatalf("UpdatedAt mismatch")
	}
}

func TestFresh(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	e := Entry{UpdatedAt: now.Add(-2 * time.Hour)}
	if !e.Fresh(now, 6*time.Hour) {
		t.Fatalf("2h entry should be fresh under 6h")
	}
	if e.Fresh(now, time.Hour) {
		t.Fatalf("2h entry should be stale under 1h")
	}
	if (Entry{}).Fresh(now, time.Hour) {
		t.Fatalf("zero entry must never be fresh")
	}
}

// An empty-savings probe result is a valid cacheable outcome (suppresses
// redundant re-probing of a route that yielded nothing).
func TestEmptySavingsStillCached(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	now := time.Now()
	if err := s.Put("K", nil, now); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("K"); !ok {
		t.Fatalf("empty-savings entry must still be cached")
	}
}

func TestEviction(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < maxRoutes+10; i++ {
		key := RouteKey("A", string(rune('A'+i%26))+string(rune('0'+i/26)))
		_ = s.Put(key, nil, base.Add(time.Duration(i)*time.Minute))
	}
	s.mu.Lock()
	n := len(s.entries)
	s.mu.Unlock()
	if n > maxRoutes {
		t.Fatalf("eviction failed: %d entries > cap %d", n, maxRoutes)
	}
}
