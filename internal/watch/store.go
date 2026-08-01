package watch

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/atomicjson"
)

// Store manages persistence of watches and price history to disk.
// All methods are safe for concurrent use.
type Store struct {
	mu      sync.Mutex
	dir     string
	watches []Watch
	history []PricePoint
}

// NewStore creates a store rooted at the given directory (typically ~/.trvl/).
// The directory is created on first write if it does not exist.
func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

// DefaultStore returns a store at ~/.trvl/.
func DefaultStore() (*Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home directory: %w", err)
	}
	return NewStore(filepath.Join(home, ".trvl")), nil
}

func (s *Store) watchesPath() string {
	return filepath.Join(s.dir, "watches.json")
}

func (s *Store) historyPath() string {
	return filepath.Join(s.dir, "price-history.json")
}

func (s *Store) ensureDir() error {
	return os.MkdirAll(s.dir, 0o700)
}

// Load reads watches and history from disk. If the files do not exist,
// the store starts empty (not an error).
func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

// loadLocked is Load for callers already holding s.mu. Go mutexes are not
// reentrant, so every in-transaction reload must come through here.
func (s *Store) loadLocked() error {
	s.watches = nil
	s.history = nil

	if err := loadJSON(s.watchesPath(), &s.watches); err != nil {
		return fmt.Errorf("load watches: %w", err)
	}
	if err := loadJSON(s.historyPath(), &s.history); err != nil {
		return fmt.Errorf("load history: %w", err)
	}
	return nil
}

// Save writes the store's current in-memory snapshot to disk atomically.
//
// Save deliberately does NOT reload first: it means "publish what I am holding",
// which is last-writer-wins by construction. It takes the cross-process lock so
// it cannot interleave with another writer's transaction, but a caller that
// loaded long ago and then calls Save can still discard another process's
// committed work. Mutating callers should use Add / Remove / Mutate /
// RecordPrice / RecordObservation, all of which reload inside the lock.
func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ensureDir(); err != nil {
		return fmt.Errorf("create storage dir: %w", err)
	}
	lock, err := acquireFileLock(s.lockPath())
	if err != nil {
		return err
	}
	defer releaseFileLock(lock)
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	if err := s.ensureDir(); err != nil {
		return fmt.Errorf("create storage dir: %w", err)
	}
	if err := saveJSON(s.watchesPath(), s.watches); err != nil {
		return fmt.Errorf("save watches: %w", err)
	}
	if err := saveJSON(s.historyPath(), s.history); err != nil {
		return fmt.Errorf("save history: %w", err)
	}
	return nil
}

// Add inserts a new watch and persists to disk. Returns the assigned ID.
//
// The insert is a single transaction against committed on-disk state, so a
// concurrent writer adding a different watch cannot be overwritten
// (TRVL.STORE.TXN.1).
//
// Adds are idempotent on identity: if a watch with the same dedupeKey (polled
// target plus price threshold) is already stored, its ID is returned and
// nothing is written. Repeating an identical request therefore cannot
// accumulate duplicates (#509, MULTIPRICE.4), while a request differing only
// in threshold is a distinct intent and is stored alongside (MULTIPRICE.1).
// The lookup runs inside the transaction, so it sees committed state rather
// than this process's snapshot and two processes cannot both win the race.
func (s *Store) Add(w Watch) (string, error) {
	if err := w.Validate(); err != nil {
		return "", err
	}

	w.ID = shortID()
	w.CreatedAt = time.Now()

	id := w.ID
	key := w.dedupeKey()
	if err := s.withTxn(func() error {
		if existing, ok := findByDedupeKey(s.watches, key); ok {
			// Identical intent already stored: adopt its ID and skip the write
			// entirely rather than rewriting both files with the same content.
			// The existing record keeps its history, lowest price and creation
			// date (MULTIPRICE.5); nothing about it is overwritten.
			id = existing.ID
			return errTxnNoop
		}
		s.watches = append(s.watches, w)
		return nil
	}); err != nil {
		return "", err
	}
	return id, nil
}

// List returns all active watches.
func (s *Store) List() []Watch {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]Watch, len(s.watches))
	copy(out, s.watches)
	return out
}

// Get returns a single watch by ID, or false if not found.
func (s *Store) Get(id string) (Watch, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, w := range s.watches {
		if w.ID == id {
			return w, true
		}
	}
	return Watch{}, false
}

// Remove deletes a watch by ID. Returns true if found and removed.
func (s *Store) Remove(id string) (bool, error) {
	found := false
	err := s.withTxn(func() error {
		for i, w := range s.watches {
			if w.ID == id {
				s.watches = append(s.watches[:i], s.watches[i+1:]...)
				found = true
				return nil
			}
		}
		// Nothing matched, so there is nothing to publish: skip the write rather
		// than rewriting both files with identical content.
		return errTxnNoop
	})
	if err != nil {
		return false, err
	}
	return found, nil
}

// UpdateWatch replaces a watch in-place by ID and persists.
//
// Whole-record replacement is inherently last-writer-wins for the fields of
// that one record: a caller that read the watch, went away, and came back writes
// its stale copy of every field. The transaction stops it from clobbering OTHER
// watches, not other fields of this one. In-package callers persist through
// Mutate instead, which edits the freshly reloaded record field by field.
// UpdateWatch stays as the exported whole-record write for callers outside this
// package; as of this change no such caller exists in-tree (mcp/ and cmd/trvl/
// use Add, Remove, Get, List and History only), so nothing in the repo is
// exposed to the last-writer-wins behaviour today. Any future out-of-package
// read-modify-write should go through a field-level path instead.
func (s *Store) UpdateWatch(updated Watch) error {
	found := false
	err := s.withTxn(func() error {
		for i, w := range s.watches {
			if w.ID == updated.ID {
				s.watches[i] = updated
				found = true
				return nil
			}
		}
		// Unknown ID: unwind without writing, then report the error below.
		return errTxnNoop
	})
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("watch %s not found", updated.ID)
	}
	return nil
}

// RecordPrice appends a price point to history and persists.
func (s *Store) RecordPrice(watchID string, price float64, currency string) error {
	return s.withTxn(func() error {
		s.history = append(s.history, PricePoint{
			WatchID:   watchID,
			Price:     price,
			Currency:  currency,
			Timestamp: time.Now(),
		})
		return nil
	})
}

// History returns all price points for a given watch ID, ordered by time.
func (s *Store) History(watchID string) []PricePoint {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []PricePoint
	for _, p := range s.history {
		if p.WatchID == watchID {
			out = append(out, p)
		}
	}
	return out
}

// RecordObservation appends an ad-hoc (route-keyed) price observation and
// persists. This is the MIK-6229 enabler: every flight/hotel search can log its
// observed price so the history corpus compounds across all searched routes,
// not only watched ones. A non-positive price is ignored (never a real fare).
//
// Two scaling guards (added in the MIK-6229 improve pass) keep the corpus
// bounded and the per-search write cheap:
//   - Throttle: a near-identical observation for the same route+currency within
//     observationThrottle is skipped entirely (no write), so rapid repeat
//     searches of the same route do not each rewrite the history file.
//   - Cap: at most maxObservationsPerRoute points are retained per route key;
//     the oldest are pruned, bounding file growth to cap x number-of-routes.
func (s *Store) RecordObservation(routeKey string, price float64, currency string) error {
	if routeKey == "" || price <= 0 {
		return nil
	}
	cur := strings.ToUpper(strings.TrimSpace(currency))

	// The throttle is a check whose outcome decides a mutation, so it has to run
	// against committed state inside the transaction — evaluating it against a
	// stale in-memory snapshot was itself a check-then-act race (#512).
	return s.withTxn(func() error {
		if last, ok := s.lastObservationLocked(routeKey, cur); ok && last.Price > 0 {
			if time.Since(last.Timestamp) < observationThrottle &&
				math.Abs(price-last.Price)/last.Price <= observationEpsilonPct {
				return errTxnNoop // redundant near-duplicate; skip the write entirely
			}
		}

		s.history = append(s.history, PricePoint{
			RouteKey:  routeKey,
			Price:     price,
			Currency:  cur,
			Timestamp: time.Now(),
		})
		s.pruneRouteLocked(routeKey)
		s.pruneGlobalRouteLocked()
		return nil
	})
}

// pruneGlobalRouteLocked bounds the total number of ad-hoc route-keyed
// observations to maxRouteObservations. Watch-keyed points (WatchID set) are
// never touched.
//
// Eviction is fair, not oldest-first (#511). Oldest-first across the whole
// corpus lets one busy route's recent points push a quiet route's entire history
// past the eviction boundary: the quiet route loses everything while never
// exceeding its own per-route cap, and the loss is silent and permanent.
//
// The policy is water-filling. Find the largest per-route quota q for which
// sum over routes of min(len(route), q) fits the global cap, then keep the
// newest q points of every route. Routes below quota are untouched, so pressure
// falls entirely on the largest contributors (TRVL.WATCH.EVICT.2), every route
// keeps a floor of q (EVICT.1), what survives is always the newest points —
// the ones sparklines and drop detection read (EVICT.4) — and the retained
// total never exceeds the cap (EVICT.5).
//
// Degradation when routes outnumber the cap: q would be 0 and fairness cannot
// be satisfied, since not even one point per route fits. In that case the
// most-recently-active maxRouteObservations routes keep their newest point each
// and the rest are dropped, which still bounds the file and still favours the
// newest data. maxRouteObservations is 20000, so this is a theoretical branch,
// not an operational one.
func (s *Store) pruneGlobalRouteLocked() {
	// Cheap counting pass first. Compaction runs on every observation, so the
	// overwhelming majority of calls are under-cap no-ops; grouping by route
	// before knowing that costs a map build (~5x the whole pass, measured by
	// BenchmarkPruneAtCap*) for nothing.
	total := 0
	for _, p := range s.history {
		if p.RouteKey != "" && p.WatchID == "" {
			total++
		}
	}
	if total <= maxRouteObservations {
		return
	}

	// Over cap: index every route-keyed point by route, in insertion
	// (chronological) order.
	order := make([]string, 0, 8)
	byRoute := make(map[string][]int)
	for i, p := range s.history {
		if p.RouteKey == "" || p.WatchID != "" {
			continue
		}
		if _, seen := byRoute[p.RouteKey]; !seen {
			order = append(order, p.RouteKey)
		}
		byRoute[p.RouteKey] = append(byRoute[p.RouteKey], i)
	}

	keep := make(map[int]bool, maxRouteObservations)
	if len(byRoute) > maxRouteObservations {
		// More routes than budget: keep the newest point of the most recently
		// active routes until the budget is spent.
		type lastPoint struct {
			idx int
			ts  time.Time
		}
		newest := make([]lastPoint, 0, len(byRoute))
		for _, key := range order {
			idxs := byRoute[key]
			last := idxs[len(idxs)-1]
			newest = append(newest, lastPoint{idx: last, ts: s.history[last].Timestamp})
		}
		sort.SliceStable(newest, func(a, b int) bool { return newest[a].ts.After(newest[b].ts) })
		for _, lp := range newest[:maxRouteObservations] {
			keep[lp.idx] = true
		}
	} else {
		quota := routeQuota(byRoute, maxRouteObservations)
		for _, idxs := range byRoute {
			tail := idxs
			if len(tail) > quota {
				tail = tail[len(tail)-quota:] // newest quota points
			}
			for _, i := range tail {
				keep[i] = true
			}
		}
	}

	kept := s.history[:0:0]
	for i, p := range s.history {
		if p.RouteKey == "" || p.WatchID != "" || keep[i] {
			kept = append(kept, p)
		}
	}
	s.history = kept
}

// routeQuota returns the largest per-route retention quota whose water-filled
// total fits cap. Callers guarantee len(byRoute) <= cap, so the result is >= 1.
func routeQuota(byRoute map[string][]int, limit int) int {
	longest := 0
	for _, idxs := range byRoute {
		if len(idxs) > longest {
			longest = len(idxs)
		}
	}
	fits := func(q int) bool {
		sum := 0
		for _, idxs := range byRoute {
			if len(idxs) < q {
				sum += len(idxs)
			} else {
				sum += q
			}
			if sum > limit {
				return false
			}
		}
		return true
	}
	// Binary search the largest q in [1, longest] that fits.
	lo, hi, best := 1, longest, 1
	for lo <= hi {
		mid := lo + (hi-lo)/2
		if fits(mid) {
			best = mid
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	return best
}

// lastObservationLocked returns the most recent price point for a route key,
// optionally filtered to a currency (empty currency matches any). Caller holds s.mu.
func (s *Store) lastObservationLocked(routeKey, currency string) (PricePoint, bool) {
	for i := len(s.history) - 1; i >= 0; i-- {
		p := s.history[i]
		if p.RouteKey != routeKey {
			continue
		}
		if currency != "" && strings.ToUpper(p.Currency) != currency {
			continue
		}
		return p, true
	}
	return PricePoint{}, false
}

// pruneRouteLocked drops the oldest observations for routeKey beyond the cap,
// preserving order. Caller holds s.mu.
func (s *Store) pruneRouteLocked(routeKey string) {
	var idx []int
	for i, p := range s.history {
		if p.RouteKey == routeKey {
			idx = append(idx, i)
		}
	}
	if len(idx) <= maxObservationsPerRoute {
		return
	}
	drop := make(map[int]bool, len(idx)-maxObservationsPerRoute)
	for _, i := range idx[:len(idx)-maxObservationsPerRoute] {
		drop[i] = true
	}
	kept := s.history[:0:0]
	for i, p := range s.history {
		if !drop[i] {
			kept = append(kept, p)
		}
	}
	s.history = kept
}

// AllHistory returns a snapshot of every price point in the store — both
// watch-keyed (WatchID set) and route-keyed (RouteKey set) — ordered by
// insertion time. The returned slice is a copy; mutations do not affect the
// store. Callers that need the full corpus for graph construction (e.g. the
// travelgraph nudge engine) should prefer this over per-watch History calls so
// that ad-hoc route observations (MIK-6229) are included alongside the
// watch-scoped history.
func (s *Store) AllHistory() []PricePoint {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]PricePoint, len(s.history))
	copy(out, s.history)
	return out
}

// RouteHistory returns all price points recorded for a given route key, ordered
// by insertion (chronological) time.
func (s *Store) RouteHistory(routeKey string) []PricePoint {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []PricePoint
	for _, p := range s.history {
		if p.RouteKey == routeKey {
			out = append(out, p)
		}
	}
	return out
}

// RoutePrices returns the price values for a route key, filtered to a currency
// so callers never mix currencies into a single price-position computation.
// An empty currency returns every recorded price for the key.
func (s *Store) RoutePrices(routeKey, currency string) []float64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	cur := strings.ToUpper(strings.TrimSpace(currency))
	var out []float64
	for _, p := range s.history {
		if p.RouteKey != routeKey {
			continue
		}
		if cur != "" && strings.ToUpper(p.Currency) != cur {
			continue
		}
		out = append(out, p.Price)
	}
	return out
}

// shortID generates a 4-byte hex string (8 characters).
func shortID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		// Fallback: use timestamp-based ID
		return fmt.Sprintf("%08x", time.Now().UnixNano()&0xFFFFFFFF)
	}
	return hex.EncodeToString(b)
}

// loadJSON reads a JSON file into dst. Returns nil if file does not exist.
func loadJSON(path string, dst interface{}) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, dst)
}

// saveJSON writes data as pretty-printed JSON.
func saveJSON(path string, data interface{}) error {
	return atomicjson.Write(path, data)
}
