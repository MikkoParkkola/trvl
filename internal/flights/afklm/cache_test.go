package afklm

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestCachePutGetFresh(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	c, err := NewCache(t.TempDir(), func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}

	key := "test-key-fresh"
	body := []byte(`{"recommendations":[]}`)
	if err := c.Put(key, body, 24*time.Hour); err != nil {
		t.Fatalf("Put: %v", err)
	}

	entry, stale, err := c.Get(key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if entry == nil {
		t.Fatal("expected cache hit")
	}
	if stale {
		t.Error("expected fresh, got stale")
	}
	if string(entry.Body) != string(body) {
		t.Errorf("body mismatch: got %q", entry.Body)
	}
}

func TestCacheStaleWithinWindow(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	cur := now
	c, err := NewCache(t.TempDir(), func() time.Time { return cur })
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}

	key := "test-key-stale"
	body := []byte(`{"stale":true}`)
	ttl := 2 * time.Hour
	if err := c.Put(key, body, ttl); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Advance time past TTL but within stale window (TTL*2).
	cur = now.Add(ttl + 1*time.Minute) // past TTL

	entry, stale, err := c.Get(key)
	if err != nil {
		t.Fatalf("Get after TTL: %v", err)
	}
	if entry == nil {
		t.Fatal("expected stale hit within stale window")
	}
	if !stale {
		t.Error("expected stale=true")
	}
}

func TestCacheMissAfterStaleWindow(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	cur := now
	c, err := NewCache(t.TempDir(), func() time.Time { return cur })
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}

	key := "test-key-expired"
	ttl := 2 * time.Hour
	c.Put(key, []byte(`{}`), ttl)

	// Advance beyond stale window (TTL*2).
	cur = now.Add(ttl*2 + 1*time.Second)

	entry, _, err := c.Get(key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if entry != nil {
		t.Error("expected cache miss after stale window expired")
	}
}

func TestCacheQuotaIncrementAndReset(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	c, err := NewCache(t.TempDir(), func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}

	day := now
	for i := 0; i < 5; i++ {
		if err := c.IncQuota(day); err != nil {
			t.Fatalf("IncQuota: %v", err)
		}
	}
	used, err := c.QuotaUsed(day)
	if err != nil {
		t.Fatalf("QuotaUsed: %v", err)
	}
	if used != 5 {
		t.Errorf("expected 5, got %d", used)
	}

	// New day — quota resets (different file).
	newDay := now.AddDate(0, 0, 1)
	used2, err := c.QuotaUsed(newDay)
	if err != nil {
		t.Fatalf("QuotaUsed new day: %v", err)
	}
	if used2 != 0 {
		t.Errorf("expected 0 for new day, got %d", used2)
	}
}

func TestCachePurgeOldQuotaFiles(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	c, err := NewCache(t.TempDir(), func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}

	// Write quota files for 40 days ago, 10 days ago, and today.
	for _, daysAgo := range []int{40, 10, 0} {
		day := now.AddDate(0, 0, -daysAgo)
		c.IncQuota(day)
	}

	// Purge files older than 30 days.
	if err := c.Purge(30 * 24 * time.Hour); err != nil {
		t.Fatalf("Purge: %v", err)
	}

	// 40-days-ago file should be gone.
	oldDay := now.AddDate(0, 0, -40)
	used, err := c.QuotaUsed(oldDay)
	if err != nil {
		t.Fatalf("QuotaUsed: %v", err)
	}
	if used != 0 {
		t.Error("old quota file should have been purged")
	}

	// 10-days-ago file should survive.
	recentDay := now.AddDate(0, 0, -10)
	used2, err := c.QuotaUsed(recentDay)
	if err != nil {
		t.Fatalf("QuotaUsed recent: %v", err)
	}
	if used2 != 1 {
		t.Errorf("recent quota file should survive, got %d", used2)
	}
}

func TestCacheAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	c, err := NewCache(dir, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}

	key := "atomic-test"
	body := []byte(`{"test":true}`)
	if err := c.Put(key, body, time.Hour); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Verify no temp files remain.
	entries, err := os.ReadDir(filepath.Join(dir, "entries"))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if len(e.Name()) > 0 && e.Name()[0] == '.' {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}

	// Verify the written file has 0600 perms on Unix. Windows does not
	// honour POSIX file modes — os.Chmod maps to a two-bit read-only flag,
	// so Stat() returns 0666 regardless. Skip the permission assertion
	// there; the atomic-write behaviour under test is unaffected.
	path := filepath.Join(dir, "entries", key+".json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("expected 0600, got %o", info.Mode().Perm())
	}
}

func TestDepArrTTL(t *testing.T) {
	cases := []struct {
		days int
		want time.Duration
	}{
		{45, 72 * time.Hour},
		{30, 72 * time.Hour},
		{20, 24 * time.Hour},
		{15, 24 * time.Hour},
		{10, 12 * time.Hour},
		{7, 12 * time.Hour},
		{5, 6 * time.Hour},
		{3, 6 * time.Hour},
		{1, 2 * time.Hour},
		{0, 2 * time.Hour},
	}
	for _, tc := range cases {
		got := DepArrTTL(tc.days)
		if got != tc.want {
			t.Errorf("DepArrTTL(%d) = %v, want %v", tc.days, got, tc.want)
		}
	}
}
