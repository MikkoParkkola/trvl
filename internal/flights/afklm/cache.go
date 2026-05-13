package afklm

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// cacheVersion is embedded in cache entry files to allow future migrations.
const cacheVersion = 1

// cacheEntry is the on-disk representation of a cached API response.
type cacheEntry struct {
	Version    int       `json:"version"`
	CachedAt   time.Time `json:"cached_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	StaleUntil time.Time `json:"stale_until"`
	Status     int       `json:"status"`
	Body       []byte    `json:"body"`
}

// quotaEntry is the on-disk representation of the daily quota counter.
type quotaEntry struct {
	Count    int       `json:"count"`
	LastCall time.Time `json:"last_call"`
}

// lastRequestEntry records debug info about the most recent request.
type lastRequestEntry struct {
	Timestamp    time.Time `json:"timestamp"`
	RequestHash  string    `json:"request_hash"`
	CacheOutcome string    `json:"cache_outcome"` // "hit", "stale", "miss"
}

// Entry is the result returned by Cache.Get.
type Entry struct {
	Body       []byte
	CachedAt   time.Time
	ExpiresAt  time.Time
	StaleUntil time.Time
	Stale      bool
}

// Cache is an on-disk TTL cache and daily quota tracker for AF-KLM API
// responses. All file operations use 0600 perms on files and 0700 on dirs.
// Writes are atomic via tmp-file + rename.
type Cache struct {
	dir string
	now func() time.Time
}

// NewCache creates a Cache backed by the given directory.
// The directory hierarchy is created if absent.
func NewCache(dir string, now func() time.Time) (*Cache, error) {
	if now == nil {
		now = time.Now
	}
	for _, sub := range []string{"entries", "quota"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o700); err != nil {
			return nil, fmt.Errorf("afklm cache: mkdir %s: %w", sub, err)
		}
	}
	c := &Cache{dir: dir, now: now}
	// Prune stale quota files on startup (best-effort).
	_ = c.Purge(30 * 24 * time.Hour)
	return c, nil
}

// CacheKey computes the sha256-based key for a request.
// endpoint is the API path; body is the canonical JSON of the request body.
func CacheKey(endpoint string, body []byte) string {
	// Canonicalize body by round-tripping through map.
	var m interface{}
	if err := json.Unmarshal(body, &m); err == nil {
		if canonical, err := json.Marshal(m); err == nil {
			body = canonical
		}
	}
	h := sha256.New()
	_, _ = h.Write([]byte(endpoint))
	_, _ = h.Write([]byte{0x00})
	_, _ = h.Write(body)
	return fmt.Sprintf("%x", h.Sum(nil))
}

// Get retrieves a cache entry by key.
// Returns (entry, false, nil) for a fresh hit.
// Returns (entry, true, nil) for a stale hit (within stale window).
// Returns (nil, false, nil) for a miss.
func (c *Cache) Get(key string) (*Entry, bool, error) {
	path := c.entryPath(key)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("afklm cache get: %w", err)
	}

	var ce cacheEntry
	if err := json.Unmarshal(data, &ce); err != nil {
		// Corrupt entry — treat as miss.
		_ = os.Remove(path)
		return nil, false, nil
	}

	now := c.now()
	if now.After(ce.StaleUntil) {
		// Expired beyond stale window — miss.
		_ = os.Remove(path)
		return nil, false, nil
	}

	stale := now.After(ce.ExpiresAt)
	entry := &Entry{
		Body:       ce.Body,
		CachedAt:   ce.CachedAt,
		ExpiresAt:  ce.ExpiresAt,
		StaleUntil: ce.StaleUntil,
		Stale:      stale,
	}
	return entry, stale, nil
}

// Put stores a response body under the given key with the specified TTL.
// Stale window is set to TTL × 2.
func (c *Cache) Put(key string, body []byte, ttl time.Duration) error {
	now := c.now()
	ce := cacheEntry{
		Version:    cacheVersion,
		CachedAt:   now,
		ExpiresAt:  now.Add(ttl),
		StaleUntil: now.Add(ttl * 2),
		Status:     200,
		Body:       body,
	}
	data, err := json.Marshal(ce)
	if err != nil {
		return fmt.Errorf("afklm cache put: marshal: %w", err)
	}
	return c.atomicWrite(c.entryPath(key), data)
}

// QuotaUsed returns the number of API calls made on the given calendar day.
func (c *Cache) QuotaUsed(day time.Time) (int, error) {
	path := c.quotaPath(day)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("afklm quota read: %w", err)
	}
	var qe quotaEntry
	if err := json.Unmarshal(data, &qe); err != nil {
		return 0, nil
	}
	return qe.Count, nil
}

// IncQuota increments the quota counter for the given day.
func (c *Cache) IncQuota(day time.Time) error {
	path := c.quotaPath(day)
	count := 0
	if data, err := os.ReadFile(path); err == nil {
		var qe quotaEntry
		if json.Unmarshal(data, &qe) == nil {
			count = qe.Count
		}
	}
	qe := quotaEntry{Count: count + 1, LastCall: c.now()}
	data, err := json.Marshal(qe)
	if err != nil {
		return fmt.Errorf("afklm quota inc: %w", err)
	}
	return c.atomicWrite(path, data)
}

// Purge removes quota files older than maxAge. Entry files are not purged
// by age (their TTL handles expiry lazily). Old quota files accumulate one
// per day, so we prune those at startup.
func (c *Cache) Purge(maxAge time.Duration) error {
	quotaDir := filepath.Join(c.dir, "quota")
	entries, err := os.ReadDir(quotaDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("afklm purge: %w", err)
	}

	cutoff := c.now().Add(-maxAge)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		// Parse ISO date from filename like "2026-04-21.json".
		name := strings.TrimSuffix(e.Name(), ".json")
		t, err := time.Parse("2006-01-02", name)
		if err != nil {
			continue
		}
		if t.Before(cutoff) {
			_ = os.Remove(filepath.Join(quotaDir, e.Name()))
		}
	}
	return nil
}

// WriteLastRequest records debug info about the most recent cache interaction.
func (c *Cache) WriteLastRequest(hash, outcome string) {
	lr := lastRequestEntry{
		Timestamp:    c.now(),
		RequestHash:  hash,
		CacheOutcome: outcome,
	}
	data, err := json.Marshal(lr)
	if err != nil {
		return
	}
	_ = c.atomicWrite(filepath.Join(c.dir, "last_request.json"), data)
}

// entryPath returns the file path for a cache entry.
func (c *Cache) entryPath(key string) string {
	return filepath.Join(c.dir, "entries", key+".json")
}

// quotaPath returns the file path for a day's quota file.
func (c *Cache) quotaPath(day time.Time) string {
	return filepath.Join(c.dir, "quota", day.UTC().Format("2006-01-02")+".json")
}

// atomicWrite writes data to path atomically via a temp file + rename.
func (c *Cache) atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("afklm cache write: mkdir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-")
	if err != nil {
		return fmt.Errorf("afklm cache write: create tmp: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("afklm cache write: write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("afklm cache write: close: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("afklm cache write: chmod: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("afklm cache write: rename: %w", err)
	}
	return nil
}

// DepArrTTL returns the cache TTL for a request given the number of days
// until departure.
func DepArrTTL(daysUntilDeparture int) time.Duration {
	switch {
	case daysUntilDeparture >= 30:
		return 72 * time.Hour
	case daysUntilDeparture >= 15:
		return 24 * time.Hour
	case daysUntilDeparture >= 7:
		return 12 * time.Hour
	case daysUntilDeparture >= 3:
		return 6 * time.Hour
	default:
		return 2 * time.Hour
	}
}
