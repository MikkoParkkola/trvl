// Package dategrid persists the full flight price calendar (date grid) that the
// watch scheduler already fetches via SearchCalendar but otherwise discards
// (keeping only the cheapest date). Persisting the whole grid is the enabler for
// MIK-6234 Tier-0 shift-day counterfactuals: a single-date flight search can
// then answer "depart a day earlier and save EUR X" from already-fetched data,
// with ZERO new provider calls.
//
// The store is deliberately independent of watch.Store (its own file, mutex, and
// path) so it composes without touching the hot watch-persistence path.
package dategrid

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/atomicjson"
)

// maxGridPoints caps the points retained per route, bounding the file.
const maxGridPoints = 120

// Point is one date's cheapest price within a grid.
type Point struct {
	Date       string  `json:"date"`
	ReturnDate string  `json:"return_date,omitempty"`
	Price      float64 `json:"price"`
	Currency   string  `json:"currency"`
}

// Grid is a persisted price calendar for one route.
type Grid struct {
	RouteKey  string    `json:"route_key"`
	Currency  string    `json:"currency"`
	Points    []Point   `json:"points"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Fresh reports whether the grid was updated within maxAge of now.
func (g Grid) Fresh(now time.Time, maxAge time.Duration) bool {
	return !g.UpdatedAt.IsZero() && now.Sub(g.UpdatedAt) <= maxAge
}

// RouteKey builds the canonical grid key for a route (origin-destination,
// date-agnostic since a grid spans dates). Upper-cased and trimmed.
func RouteKey(origin, destination string) string {
	norm := func(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }
	return norm(origin) + "|" + norm(destination)
}

// Store persists route grids to ~/.trvl/date-grids.json. Safe for concurrent use.
type Store struct {
	mu    sync.Mutex
	dir   string
	grids map[string]Grid
}

// NewStore creates a store rooted at dir (typically ~/.trvl).
func NewStore(dir string) *Store {
	return &Store{dir: dir, grids: make(map[string]Grid)}
}

// DefaultStore returns a store at ~/.trvl.
func DefaultStore() (*Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return NewStore(filepath.Join(home, ".trvl")), nil
}

func (s *Store) path() string { return filepath.Join(s.dir, "date-grids.json") }

// Load reads grids from disk. A missing file starts the store empty.
func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.grids = make(map[string]Grid)
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
	var list []Grid
	if err := json.Unmarshal(data, &list); err != nil {
		return err
	}
	for _, g := range list {
		s.grids[g.RouteKey] = g
	}
	return nil
}

// Put stores (replacing) the grid for routeKey and persists. Points are capped
// to maxGridPoints (cheapest first). A zero/empty points slice is a no-op.
func (s *Store) Put(routeKey, currency string, points []Point, now time.Time) error {
	if routeKey == "" || len(points) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	cp := append([]Point(nil), points...)
	sort.SliceStable(cp, func(i, j int) bool { return cp[i].Price < cp[j].Price })
	if len(cp) > maxGridPoints {
		cp = cp[:maxGridPoints]
	}
	s.grids[routeKey] = Grid{
		RouteKey:  routeKey,
		Currency:  currency,
		Points:    cp,
		UpdatedAt: now,
	}
	return s.saveLocked()
}

// Get returns the grid for routeKey, or false if absent.
func (s *Store) Get(routeKey string) (Grid, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.grids[routeKey]
	return g, ok
}

// saveLocked persists the grids deterministically (sorted by route key) via the
// shared atomicjson helper, which centralises MkdirAll(0700), the O_EXCL temp
// file (0600), fsync, and the atomic rename (with the Windows fallback).
func (s *Store) saveLocked() error {
	keys := make([]string, 0, len(s.grids))
	for k := range s.grids {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	list := make([]Grid, 0, len(keys))
	for _, k := range keys {
		list = append(list, s.grids[k])
	}
	return atomicjson.Write(s.path(), list)
}
