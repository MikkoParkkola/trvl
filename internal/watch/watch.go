// Package watch provides price tracking for flights and hotels.
// It stores watch definitions and price history as JSON files
// under ~/.trvl/ and supports threshold-based alerting.
package watch

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Scaling guards for ad-hoc route observations (MIK-6229 improve pass).
const (
	// maxObservationsPerRoute caps retained points per route key, bounding the
	// price-history file to cap x number-of-routes.
	maxObservationsPerRoute = 1000
	// observationThrottle suppresses near-identical repeat observations for the
	// same route+currency within this window.
	observationThrottle = 15 * time.Minute
	// observationEpsilonPct is the relative price delta below which a throttled
	// observation is treated as a duplicate.
	observationEpsilonPct = 0.005
)

// Watch represents a price tracking rule for a flight or hotel route.
//
// Three watch modes:
//   - Specific date: DepartDate set, no DepartFrom/DepartTo → checks one date
//   - Date range: DepartFrom + DepartTo set → checks cheapest across range
//   - Route watch: no dates at all → checks next 60 days for cheapest on any date
type Watch struct {
	ID           string    `json:"id"`
	Type         string    `json:"type"` // "flight", "hotel", or "room"
	Origin       string    `json:"origin"`
	Destination  string    `json:"destination"`
	DepartDate   string    `json:"depart_date,omitempty"`
	ReturnDate   string    `json:"return_date,omitempty"`
	DepartFrom   string    `json:"depart_from,omitempty"` // date range start (YYYY-MM-DD)
	DepartTo     string    `json:"depart_to,omitempty"`   // date range end (YYYY-MM-DD)
	BelowPrice   float64   `json:"below_price"`
	Currency     string    `json:"currency"`
	CreatedAt    time.Time `json:"created_at"`
	LastCheck    time.Time `json:"last_check"`
	LastPrice    float64   `json:"last_price"`
	LowestPrice  float64   `json:"lowest_price"`
	CheapestDate string    `json:"cheapest_date,omitempty"` // which date had the lowest price

	// Last-minute hotel mode flags sub-48h availability when the current price
	// is materially below LastPrice. Drop threshold defaults to 25%.
	LastMinuteMode    bool    `json:"last_minute_mode,omitempty"`
	LastMinuteDropPct float64 `json:"last_minute_drop_pct,omitempty"`

	// Webhook notification URL. When set and a price drop is detected, a JSON
	// payload is POSTed to this URL (fire-and-forget, 10s timeout).
	WebhookURL string `json:"webhook_url,omitempty"`

	// Proactive price-drop alerting (innovation #6: pull -> push).
	// AlertDropPct / AlertDropAbs configure how far the fare must fall below the
	// captured baseline before trvl proactively alerts. Either limb qualifies;
	// when both are zero a sane default (10%) applies. BaselinePrice is the
	// reference fare (captured on first observation, tracks the running peak) and
	// LastAlertedPrice is dedup state so a single drop alerts exactly once.
	AlertDropPct     float64 `json:"alert_drop_pct,omitempty"`
	AlertDropAbs     float64 `json:"alert_drop_abs,omitempty"`
	BaselinePrice    float64 `json:"baseline_price,omitempty"`
	LastAlertedPrice float64 `json:"last_alerted_price,omitempty"`

	// Room watch fields (Type == "room").
	HotelName    string   `json:"hotel_name,omitempty"`    // hotel name for room availability lookups
	RoomKeywords []string `json:"room_keywords,omitempty"` // all keywords must match room name+description
	MatchedRoom  string   `json:"matched_room,omitempty"`  // last matched room name (for display)

	// Opportunity watch fields (Type == "opportunity").
	// Favourites defaults to BucketList ∪ PreviousTrips ∩ AirportAffinity≥0.3 if empty.
	Favourites []string `json:"favourites,omitempty"`  // IATA codes
	WindowFrom string   `json:"window_from,omitempty"` // YYYY-MM-DD or "next_Nd" (e.g. "next_30d")
	WindowTo   string   `json:"window_to,omitempty"`   // YYYY-MM-DD or "next_Nd"
	MinScore   int      `json:"min_score,omitempty"`   // default 85
	MinNights  int      `json:"min_nights,omitempty"`  // default 3
	MaxNights  int      `json:"max_nights,omitempty"`  // default 14
}

// IsRouteWatch returns true if this watch monitors a route without specific dates.
func (w Watch) IsRouteWatch() bool {
	return w.DepartDate == "" && w.DepartFrom == "" && w.DepartTo == ""
}

// IsDateRange returns true if this watch monitors a date range.
func (w Watch) IsDateRange() bool {
	return w.DepartFrom != "" && w.DepartTo != ""
}

// IsRoomWatch returns true if this watch monitors room availability.
func (w Watch) IsRoomWatch() bool {
	return w.Type == "room"
}

// IsOpportunityWatch returns true if this watch is an opportunity watch.
func (w Watch) IsOpportunityWatch() bool {
	return w.Type == "opportunity"
}

// MatchRoomKeywords checks whether all keywords appear (case-insensitive) in the
// combined room name and description. Returns true if every keyword is found.
func MatchRoomKeywords(keywords []string, roomName, roomDescription string) bool {
	if len(keywords) == 0 {
		return false
	}
	text := strings.ToLower(roomName + " " + roomDescription)
	for _, kw := range keywords {
		if !strings.Contains(text, strings.ToLower(kw)) {
			return false
		}
	}
	return true
}

const watchDateLayout = "2006-01-02"

// Validate rejects malformed or ambiguous watch date combinations before they
// get persisted and later fail during background checks.
func (w Watch) Validate() error {
	if err := validateWatchDate("depart date", w.DepartDate); err != nil {
		return err
	}
	if err := validateWatchDate("return date", w.ReturnDate); err != nil {
		return err
	}
	if err := validateWatchDate("date range start", w.DepartFrom); err != nil {
		return err
	}
	if err := validateWatchDate("date range end", w.DepartTo); err != nil {
		return err
	}

	// Room watch validation.
	if w.IsRoomWatch() {
		if w.HotelName == "" {
			return fmt.Errorf("room watch requires a hotel name")
		}
		if len(w.RoomKeywords) == 0 {
			return fmt.Errorf("room watch requires at least one keyword")
		}
		if w.DepartDate == "" || w.ReturnDate == "" {
			return fmt.Errorf("room watch requires check-in (depart_date) and check-out (return_date)")
		}
		return nil
	}

	// Opportunity watch validation.
	if w.IsOpportunityWatch() {
		if len(w.Favourites) == 0 && w.MinNights <= 0 {
			return fmt.Errorf("opportunity watch requires favourites or a min_nights value")
		}
		return nil
	}

	if w.LastMinuteMode {
		if w.Type != "hotel" {
			return fmt.Errorf("last-minute mode is only supported for hotel watches")
		}
		if w.LastMinuteDropPct < 0 {
			return fmt.Errorf("last-minute drop threshold must be non-negative")
		}
	}

	if w.DepartDate != "" && (w.DepartFrom != "" || w.DepartTo != "") {
		return fmt.Errorf("cannot combine a specific depart date with a date range")
	}
	if (w.DepartFrom == "") != (w.DepartTo == "") {
		return fmt.Errorf("date range requires both start and end dates")
	}
	if w.IsDateRange() {
		from, _ := time.Parse(watchDateLayout, w.DepartFrom)
		to, _ := time.Parse(watchDateLayout, w.DepartTo)
		if from.After(to) {
			return fmt.Errorf("date range start must be on or before end")
		}
	}
	return nil
}

func validateWatchDate(label, value string) error {
	if value == "" {
		return nil
	}
	if _, err := time.Parse(watchDateLayout, value); err != nil {
		return fmt.Errorf("%s must use YYYY-MM-DD", label)
	}
	return nil
}

// PricePoint records a single price observation.
//
// Observations come from two sources, distinguished by which key is set:
//   - Watch scheduler checks set WatchID (the original, watch-scoped corpus).
//   - Ad-hoc searches set RouteKey (MIK-6229), so the history corpus compounds
//     across every searched route, not only the watched ones.
//
// Exactly one of WatchID / RouteKey is expected to be set on a given point;
// older records carry only WatchID and remain valid.
type PricePoint struct {
	WatchID   string    `json:"watch_id,omitempty"`
	RouteKey  string    `json:"route_key,omitempty"`
	Price     float64   `json:"price"`
	Currency  string    `json:"currency"`
	Timestamp time.Time `json:"timestamp"`
}

// RouteKey builds the canonical history key for an ad-hoc observation. The key
// is provider-agnostic and date-scoped so that a "flight AMS->VLC on
// 2026-07-15" search accrues to the same series regardless of which provider
// returned the cheapest fare. Inputs are upper-cased and trimmed; an empty
// date is allowed (route-level series).
func RouteKey(kind, origin, destination, date string) string {
	norm := func(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }
	return strings.Join([]string{norm(kind), norm(origin), norm(destination), strings.TrimSpace(date)}, "|")
}

// Sparkline renders a compact Unicode sparkline from price history.
// Uses the last N points (up to maxPoints) scaled to 8 block-element levels.
// Returns "" if fewer than 2 data points exist.
func Sparkline(history []PricePoint, maxPoints int) string {
	if len(history) < 2 {
		return ""
	}

	// Take the tail.
	start := 0
	if len(history) > maxPoints {
		start = len(history) - maxPoints
	}
	pts := history[start:]

	// Find min/max for scaling.
	lo, hi := pts[0].Price, pts[0].Price
	for _, p := range pts[1:] {
		if p.Price < lo {
			lo = p.Price
		}
		if p.Price > hi {
			hi = p.Price
		}
	}

	bars := []rune("▁▂▃▄▅▆▇█")
	spread := hi - lo
	var b []rune
	for _, p := range pts {
		idx := 0
		if spread > 0 {
			idx = int((p.Price - lo) / spread * float64(len(bars)-1))
			if idx >= len(bars) {
				idx = len(bars) - 1
			}
		} else {
			idx = len(bars) / 2 // flat line
		}
		b = append(b, bars[idx])
	}
	return string(b)
}

// TrendArrow returns a directional indicator comparing the last two prices.
// Returns "" if there are fewer than 2 data points.
func TrendArrow(history []PricePoint) string {
	if len(history) < 2 {
		return ""
	}
	prev := history[len(history)-2].Price
	curr := history[len(history)-1].Price
	switch {
	case curr < prev:
		return "↓"
	case curr > prev:
		return "↑"
	default:
		return "→"
	}
}

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

// Save writes watches and history to disk atomically.
func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
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
func (s *Store) Add(w Watch) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := w.Validate(); err != nil {
		return "", err
	}

	w.ID = shortID()
	w.CreatedAt = time.Now()
	s.watches = append(s.watches, w)

	if err := s.saveLocked(); err != nil {
		return "", err
	}
	return w.ID, nil
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
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, w := range s.watches {
		if w.ID == id {
			s.watches = append(s.watches[:i], s.watches[i+1:]...)
			if err := s.saveLocked(); err != nil {
				return false, err
			}
			return true, nil
		}
	}
	return false, nil
}

// UpdateWatch replaces a watch in-place by ID and persists.
func (s *Store) UpdateWatch(updated Watch) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, w := range s.watches {
		if w.ID == updated.ID {
			s.watches[i] = updated
			return s.saveLocked()
		}
	}
	return fmt.Errorf("watch %s not found", updated.ID)
}

// RecordPrice appends a price point to history and persists.
func (s *Store) RecordPrice(watchID string, price float64, currency string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.history = append(s.history, PricePoint{
		WatchID:   watchID,
		Price:     price,
		Currency:  currency,
		Timestamp: time.Now(),
	})
	return s.saveLocked()
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
	s.mu.Lock()
	defer s.mu.Unlock()

	cur := strings.ToUpper(strings.TrimSpace(currency))
	if last, ok := s.lastObservationLocked(routeKey, cur); ok && last.Price > 0 {
		if time.Since(last.Timestamp) < observationThrottle &&
			math.Abs(price-last.Price)/last.Price <= observationEpsilonPct {
			return nil // redundant near-duplicate; skip the write entirely
		}
	}

	s.history = append(s.history, PricePoint{
		RouteKey:  routeKey,
		Price:     price,
		Currency:  cur,
		Timestamp: time.Now(),
	})
	s.pruneRouteLocked(routeKey)
	return s.saveLocked()
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
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
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
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	if err := os.Rename(tmpPath, path); err != nil {
		if runtime.GOOS == "windows" {
			_ = os.Remove(path)
			if err2 := os.Rename(tmpPath, path); err2 == nil {
				cleanup = false
				return nil
			}
		}
		return err
	}

	cleanup = false
	return nil
}
