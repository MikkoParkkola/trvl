package cache

import (
	"sync"
	"testing"
	"time"
)

func TestDateClass(t *testing.T) {
	cases := map[string]string{
		"2026-07-15":    "2026-07",
		"2026-12-01":    "2026-12",
		"  2026-03-09 ": "2026-03",
		"":              "",
		"garbage":       "garbage",
		"2026/07/15":    "2026/07/15", // wrong separator => raw fallback
	}
	for in, want := range cases {
		if got := DateClass(in); got != want {
			t.Errorf("DateClass(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNegativeKey_NormalizesAndBuckets(t *testing.T) {
	a := NegativeKey("Ryanair", "hel", "agp", "2026-07-15")
	b := NegativeKey("ryanair", "HEL", "AGP", "2026-07-28") // same month
	if a != b {
		t.Errorf("keys for same provider/route/month should match:\n a=%q\n b=%q", a, b)
	}
	if a != "ryanair|HEL|AGP|2026-07" {
		t.Errorf("unexpected key form: %q", a)
	}

	// Different month must produce a different key (seasonal safety).
	if NegativeKey("ryanair", "HEL", "AGP", "2026-08-01") == a {
		t.Error("different month should produce a different key")
	}
}

func TestNegativeCache_StoreAndServe(t *testing.T) {
	c := NewNegativeCache(30 * time.Minute)
	key := NegativeKey("wizzair", "HEL", "BUD", "2026-07-10")

	if c.Seen(key) {
		t.Fatal("fresh cache should not report a hit")
	}
	c.Mark(key)
	if !c.Seen(key) {
		t.Fatal("expected a hit after Mark")
	}
	if c.Len() != 1 {
		t.Errorf("Len() = %d, want 1", c.Len())
	}
}

func TestNegativeCache_TTLExpiry(t *testing.T) {
	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	c := NewNegativeCacheWithClock(30*time.Minute, clock)

	key := NegativeKey("ryanair", "HEL", "AGP", "2026-07-15")
	c.Mark(key)
	if !c.Seen(key) {
		t.Fatal("expected hit immediately after Mark")
	}

	// Advance to just before expiry — still a hit.
	now = now.Add(29 * time.Minute)
	if !c.Seen(key) {
		t.Fatal("expected hit just before TTL expiry")
	}

	// Advance past expiry — miss, and the entry is reaped.
	now = now.Add(2 * time.Minute)
	if c.Seen(key) {
		t.Fatal("expected miss after TTL expiry")
	}
	if c.Len() != 0 {
		t.Errorf("expired entry should be reaped on access; Len() = %d", c.Len())
	}
}

func TestNegativeCache_BoundedEviction(t *testing.T) {
	c := NewNegativeCache(time.Hour)
	c.maxEntries = 10
	for i := 0; i < 50; i++ {
		c.Mark(NegativeKey("p", "AAA", "BBB", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, i, 0).Format("2006-01-02")))
	}
	if c.Len() > 10 {
		t.Errorf("Len() = %d, want <= 10 (bounded)", c.Len())
	}
}

func TestNegativeCache_ConcurrentAccess(t *testing.T) {
	c := NewNegativeCache(time.Hour)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := NegativeKey("p", "AAA", "BBB", "2026-07-15")
			c.Mark(key)
			c.Seen(key)
		}(i)
	}
	wg.Wait()
}
