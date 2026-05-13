// Package trips provides persistence and management of planned and booked trips.
// Trips are stored as JSON under ~/.trvl/trips.json with mode 0600.
package trips

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

// Trip represents a planned or booked travel itinerary.
type Trip struct {
	ID            string    `json:"id"`
	SchemaVersion int       `json:"schema_version,omitempty"`
	Name          string    `json:"name"`
	Status        string    `json:"status"` // "planning", "booked", "in_progress", "completed", "cancelled"
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`

	// Legs in chronological order.
	Legs []TripLeg `json:"legs"`

	// Booking refs (after booking).
	Bookings []Booking `json:"bookings,omitempty"`

	// Workspace holds richer local-first planning state.
	Workspace *Workspace `json:"workspace,omitempty"`

	// Tags / context.
	Tags  []string `json:"tags,omitempty"`
	Notes string   `json:"notes,omitempty"`
}

// TripLeg is a single segment of a trip (flight, train, hotel stay, etc.).
type TripLeg struct {
	Type       string  `json:"type"`               // "flight", "train", "bus", "ferry", "hotel", "activity"
	From       string  `json:"from"`               // city or "Helsinki home"
	To         string  `json:"to"`                 // city
	Provider   string  `json:"provider,omitempty"` // "KLM", "Tallink", "Czech Inn"
	StartTime  string  `json:"start_time"`         // ISO datetime
	EndTime    string  `json:"end_time"`           // ISO datetime
	Price      float64 `json:"price,omitempty"`
	Currency   string  `json:"currency,omitempty"`
	BookingURL string  `json:"booking_url,omitempty"`
	Confirmed  bool    `json:"confirmed"`           // false = planned, true = booked
	Reference  string  `json:"reference,omitempty"` // PNR / booking code
}

// Booking is a top-level booking record for a trip.
type Booking struct {
	Type        string    `json:"type"`          // "flight", "hotel"
	Provider    string    `json:"provider"`      // "KLM"
	Reference   string    `json:"reference"`     // "ABC123"
	URL         string    `json:"url,omitempty"` // confirmation URL
	CandidateID string    `json:"candidate_id,omitempty"`
	Price       float64   `json:"price,omitempty"`
	Currency    string    `json:"currency,omitempty"`
	ConfirmedAt time.Time `json:"confirmed_at,omitempty"`
	Notes       string    `json:"notes,omitempty"`
}

// Alert is a monitoring notification for a trip.
type Alert struct {
	TripID    string    `json:"trip_id"`
	TripName  string    `json:"trip_name"`
	Type      string    `json:"type"` // "price_drop", "reminder", "weather", "advisory"
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
	Read      bool      `json:"read"`
}

// activeStatuses lists the statuses considered "active".
var activeStatuses = map[string]bool{
	"planning":    true,
	"booked":      true,
	"in_progress": true,
}

// ValidStatuses is the set of allowed trip statuses.
var ValidStatuses = map[string]bool{
	"planning":    true,
	"booked":      true,
	"in_progress": true,
	"completed":   true,
	"cancelled":   true,
}

// Store manages trip and alert persistence to disk.
// All methods are safe for concurrent use.
type Store struct {
	mu     sync.Mutex
	dir    string
	trips  []Trip
	alerts []Alert
}

// NewStore creates a store rooted at the given directory (typically ~/.trvl/).
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

func (s *Store) tripsPath() string {
	return filepath.Join(s.dir, "trips.json")
}

func (s *Store) alertsPath() string {
	return filepath.Join(s.dir, "alerts.json")
}

func (s *Store) ensureDir() error {
	return os.MkdirAll(s.dir, 0o700)
}

// Load reads trips and alerts from disk.
// If the files do not exist the store starts empty (not an error).
func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.trips = nil
	s.alerts = nil

	if err := loadJSON(s.tripsPath(), &s.trips); err != nil {
		return fmt.Errorf("load trips: %w", err)
	}
	if err := loadJSON(s.alertsPath(), &s.alerts); err != nil {
		return fmt.Errorf("load alerts: %w", err)
	}
	for i := range s.trips {
		s.trips[i] = NormalizeWorkspace(s.trips[i])
	}
	return nil
}

// Save writes trips and alerts to disk atomically.
func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	if err := s.ensureDir(); err != nil {
		return fmt.Errorf("create storage dir: %w", err)
	}
	if err := saveJSON(s.tripsPath(), s.trips); err != nil {
		return fmt.Errorf("save trips: %w", err)
	}
	if err := saveJSON(s.alertsPath(), s.alerts); err != nil {
		return fmt.Errorf("save alerts: %w", err)
	}
	return nil
}

// Add appends a new trip with a generated ID and persists to disk.
func (s *Store) Add(t Trip) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if t.Name == "" {
		return "", fmt.Errorf("trip name is required")
	}
	if t.Status == "" {
		t.Status = "planning"
	}
	if !ValidStatuses[t.Status] {
		return "", fmt.Errorf("invalid status %q", t.Status)
	}

	now := time.Now()
	t.ID = generateID()
	t.CreatedAt = now
	t.UpdatedAt = now
	t = NormalizeWorkspace(t)
	s.trips = append(s.trips, t)

	if err := s.saveLocked(); err != nil {
		return "", err
	}
	return t.ID, nil
}

// Get returns a pointer to the trip with the given ID, or nil if not found.
func (s *Store) Get(id string) (*Trip, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, t := range s.trips {
		if t.ID == id {
			cp := s.trips[i]
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("trip %q not found", id)
}

// Update atomically finds a trip by ID, calls fn with a pointer to it,
// and persists the result if fn returns nil.
func (s *Store) Update(id string, fn func(*Trip) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, t := range s.trips {
		if t.ID == id {
			cp := NormalizeWorkspace(t)
			if err := fn(&cp); err != nil {
				return err
			}
			cp.UpdatedAt = time.Now()
			cp = NormalizeWorkspace(cp)
			s.trips[i] = cp
			return s.saveLocked()
		}
	}
	return fmt.Errorf("trip %q not found", id)
}

// Delete removes the trip with the given ID from the store.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, t := range s.trips {
		if t.ID == id {
			s.trips = append(s.trips[:i], s.trips[i+1:]...)
			return s.saveLocked()
		}
	}
	return fmt.Errorf("trip %q not found", id)
}

// List returns a copy of all trips.
func (s *Store) List() []Trip {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]Trip, len(s.trips))
	copy(out, s.trips)
	return out
}

// Active returns trips whose status is in {planning, booked, in_progress}.
func (s *Store) Active() []Trip {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []Trip
	for _, t := range s.trips {
		if activeStatuses[t.Status] {
			out = append(out, t)
		}
	}
	return out
}

// Upcoming returns active trips that have at least one leg starting within
// the given duration from now.
func (s *Store) Upcoming(within time.Duration) []Trip {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	deadline := now.Add(within)

	var out []Trip
	for _, t := range s.trips {
		if !activeStatuses[t.Status] {
			continue
		}
		if tripStartsWithin(t, now, deadline) {
			out = append(out, t)
		}
	}
	return out
}

// tripStartsWithin returns true if any leg of t starts in [now, deadline].
func tripStartsWithin(t Trip, now, deadline time.Time) bool {
	for _, leg := range t.Legs {
		if leg.StartTime == "" {
			continue
		}
		ts, err := parseDateTime(leg.StartTime)
		if err != nil {
			continue
		}
		if !ts.Before(now) && !ts.After(deadline) {
			return true
		}
	}
	return false
}

// AddAlert appends an alert and persists to disk.
func (s *Store) AddAlert(a Alert) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	a.CreatedAt = time.Now()
	s.alerts = append(s.alerts, a)
	return s.saveLocked()
}

// Alerts returns all alerts (optionally only unread).
func (s *Store) Alerts(unreadOnly bool) []Alert {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []Alert
	for _, a := range s.alerts {
		if unreadOnly && a.Read {
			continue
		}
		out = append(out, a)
	}
	return out
}

// MarkAlertsRead marks all alerts as read and persists.
func (s *Store) MarkAlertsRead() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.alerts {
		s.alerts[i].Read = true
	}
	return s.saveLocked()
}

// FirstLegStart returns the earliest leg start time across all legs of t.
// Returns zero time if no parseable start times are found.
func FirstLegStart(t Trip) time.Time {
	var earliest time.Time
	for _, leg := range t.Legs {
		if leg.StartTime == "" {
			continue
		}
		ts, err := parseDateTime(leg.StartTime)
		if err != nil {
			continue
		}
		if earliest.IsZero() || ts.Before(earliest) {
			earliest = ts
		}
	}
	return earliest
}

// parseDateTime parses ISO 8601 datetime strings.
// Formats with explicit timezone (Z / +HH:MM) are parsed as-is.
// Formats without timezone are assumed to be local time.
func parseDateTime(s string) (time.Time, error) {
	// Formats with embedded timezone — parse literally.
	withTZ := []string{
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02T15:04Z07:00",
	}
	for _, f := range withTZ {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}
	// Formats without timezone — interpret as local time.
	localFmts := []string{
		"2006-01-02T15:04:05",
		"2006-01-02T15:04",
		"2006-01-02",
	}
	for _, f := range localFmts {
		if t, err := time.ParseInLocation(f, s, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse datetime %q", s)
}

// generateID creates an 8-character hex trip ID prefixed with "trip_".
func generateID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("trip_%08x", time.Now().UnixNano()&0xFFFFFFFF)
	}
	return "trip_" + hex.EncodeToString(b)
}

// loadJSON reads a JSON file into dst.  Returns nil if the file does not exist.
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

// saveJSON writes data as pretty-printed JSON to path using an atomic rename.
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
