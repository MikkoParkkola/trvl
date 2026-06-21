// Package probecache stores counterfactual savings that the watch scheduler
// pre-computes for a route (MIK-6234 Tier-1, scheduler-amortized fan-out). A
// later flight search reads them back with zero new provider calls.
//
// Like internal/dategrid it is independent of the watch store (own file, mutex,
// path) so it composes without touching the hot watch-persistence path.
package probecache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/counterfactual"
)

// maxRoutes caps how many routes are retained, bounding the file. The oldest
// (by UpdatedAt) are dropped first.
const maxRoutes = 200

// Entry is the cached probe result for one route.
type Entry struct {
	RouteKey  string                  `json:"route_key"`
	Savings   []counterfactual.Saving `json:"savings"`
	UpdatedAt time.Time               `json:"updated_at"`
}

// Fresh reports whether the entry was updated within maxAge of now.
func (e Entry) Fresh(now time.Time, maxAge time.Duration) bool {
	return !e.UpdatedAt.IsZero() && now.Sub(e.UpdatedAt) <= maxAge
}

// RouteKey builds the canonical cache key for a route (origin-destination).
func RouteKey(origin, destination string) string {
	norm := func(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }
	return norm(origin) + "|" + norm(destination)
}

// Store persists probe entries to ~/.trvl/probe-cache.json. Safe for concurrent use.
type Store struct {
	mu      sync.Mutex
	dir     string
	entries map[string]Entry
}

// NewStore creates a store rooted at dir (typically ~/.trvl).
func NewStore(dir string) *Store { return &Store{dir: dir, entries: make(map[string]Entry)} }

// DefaultStore returns a store at ~/.trvl.
func DefaultStore() (*Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return NewStore(filepath.Join(home, ".trvl")), nil
}

func (s *Store) path() string { return filepath.Join(s.dir, "probe-cache.json") }

// Load reads entries from disk. A missing file starts the store empty.
func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.entries = make(map[string]Entry)
	data, err := os.ReadFile(s.path())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}
	var list []Entry
	if err := json.Unmarshal(data, &list); err != nil {
		return err
	}
	for _, e := range list {
		s.entries[e.RouteKey] = e
	}
	return nil
}

// Put stores (replacing) the savings for routeKey and persists. An empty
// savings slice still records an entry (a probe that found nothing is a valid,
// cacheable outcome that suppresses redundant re-probing).
func (s *Store) Put(routeKey string, savings []counterfactual.Saving, now time.Time) error {
	if routeKey == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.entries[routeKey] = Entry{RouteKey: routeKey, Savings: savings, UpdatedAt: now}
	s.evictLocked()
	return s.saveLocked()
}

// Get returns the entry for routeKey, or false if absent.
func (s *Store) Get(routeKey string) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[routeKey]
	return e, ok
}

// evictLocked drops the oldest entries beyond maxRoutes. Caller holds s.mu.
func (s *Store) evictLocked() {
	if len(s.entries) <= maxRoutes {
		return
	}
	type kv struct {
		k string
		t time.Time
	}
	all := make([]kv, 0, len(s.entries))
	for k, e := range s.entries {
		all = append(all, kv{k, e.UpdatedAt})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].t.Before(all[j].t) })
	for _, x := range all[:len(all)-maxRoutes] {
		delete(s.entries, x.k)
	}
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	keys := make([]string, 0, len(s.entries))
	for k := range s.entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	list := make([]Entry, 0, len(keys))
	for _, k := range keys {
		list = append(list, s.entries[k])
	}
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(s.dir, "probe-cache.json.tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, s.path()); err != nil {
		if runtime.GOOS == "windows" {
			_ = os.Remove(s.path())
			if err2 := os.Rename(tmpPath, s.path()); err2 == nil {
				cleanup = false
				return nil
			}
		}
		return err
	}
	cleanup = false
	return nil
}
