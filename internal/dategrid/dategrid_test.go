package dategrid

import (
	"testing"
	"time"
)

func TestPutGetRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	key := RouteKey("ams", "VLC")

	pts := []Point{
		{Date: "2026-07-15", Price: 150, Currency: "EUR"},
		{Date: "2026-07-14", Price: 90, Currency: "EUR"},
		{Date: "2026-07-16", Price: 140, Currency: "EUR"},
	}
	if err := s.Put(key, "EUR", pts, now); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Reload from disk.
	s2 := NewStore(dir)
	if err := s2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	g, ok := s2.Get(key)
	if !ok {
		t.Fatalf("grid not found after reload")
	}
	if len(g.Points) != 3 {
		t.Fatalf("want 3 points, got %d", len(g.Points))
	}
	// Stored cheapest-first.
	if g.Points[0].Price != 90 {
		t.Fatalf("want cheapest-first, got %v", g.Points[0].Price)
	}
	if !g.UpdatedAt.Equal(now) {
		t.Fatalf("UpdatedAt mismatch: %v", g.UpdatedAt)
	}
}

func TestFresh(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	g := Grid{UpdatedAt: now.Add(-2 * time.Hour)}
	if !g.Fresh(now, 24*time.Hour) {
		t.Fatalf("2h-old grid should be fresh under 24h window")
	}
	if g.Fresh(now, time.Hour) {
		t.Fatalf("2h-old grid should be stale under 1h window")
	}
	if (Grid{}).Fresh(now, time.Hour) {
		t.Fatalf("zero-time grid must never be fresh")
	}
}

func TestPutNoOpOnEmpty(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	now := time.Now()
	if err := s.Put("", "EUR", []Point{{Date: "x", Price: 1}}, now); err != nil {
		t.Fatal(err)
	}
	if err := s.Put("K", "EUR", nil, now); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("K"); ok {
		t.Fatalf("empty points must not store a grid")
	}
}

func TestPutCapsPoints(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	now := time.Now()
	var pts []Point
	for i := 0; i < maxGridPoints+30; i++ {
		pts = append(pts, Point{Date: "d", Price: float64(1000 - i), Currency: "EUR"})
	}
	_ = s.Put("K", "EUR", pts, now)
	g, _ := s.Get("K")
	if len(g.Points) != maxGridPoints {
		t.Fatalf("want cap %d, got %d", maxGridPoints, len(g.Points))
	}
}
