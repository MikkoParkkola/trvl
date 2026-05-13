package trips

import (
	"os"
	"testing"
	"time"
)

// --- Save ---

func TestSave_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	_, err := s.Add(Trip{Name: "SaveTest", Status: "planning"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Save explicitly (Add already saves, but we test Save directly).
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Reload and verify.
	s2 := NewStore(dir)
	if err := s2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	trips := s2.List()
	if len(trips) != 1 || trips[0].Name != "SaveTest" {
		t.Errorf("expected 'SaveTest' after Save, got %+v", trips)
	}
}

func TestSave_WithAlerts(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	tripID, _ := s.Add(Trip{Name: "AlertTrip"})
	if err := s.AddAlert(Alert{
		TripID:  tripID,
		Message: "Price dropped",
	}); err != nil {
		t.Fatalf("AddAlert: %v", err)
	}

	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	s2 := NewStore(dir)
	if err := s2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	alerts := s2.Alerts(false)
	if len(alerts) != 1 {
		t.Errorf("expected 1 alert after Save/Load, got %d", len(alerts))
	}
}

// --- parseDateTime ---

func TestParseDateTime_ISO8601WithTZ(t *testing.T) {
	tests := []struct {
		s    string
		want string
	}{
		{"2026-07-01T10:00:00Z", "2026-07-01T10:00:00Z"},
		{"2026-07-01T12:00:00+02:00", "2026-07-01T10:00:00Z"},
		{"2026-07-01T10:00Z", "2026-07-01T10:00:00Z"},
	}
	for _, tt := range tests {
		t.Run(tt.s, func(t *testing.T) {
			got, err := parseDateTime(tt.s)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			// Normalize to UTC for comparison.
			gotUTC := got.UTC().Format(time.RFC3339)
			if gotUTC != tt.want {
				t.Errorf("parseDateTime(%q) = %q, want %q", tt.s, gotUTC, tt.want)
			}
		})
	}
}

func TestParseDateTime_NoTZ(t *testing.T) {
	tests := []string{
		"2026-07-01T10:00:00",
		"2026-07-01T10:00",
		"2026-07-01",
	}
	for _, s := range tests {
		t.Run(s, func(t *testing.T) {
			got, err := parseDateTime(s)
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", s, err)
			}
			if got.IsZero() {
				t.Errorf("expected non-zero time for %q", s)
			}
		})
	}
}

func TestParseDateTime_Invalid(t *testing.T) {
	_, err := parseDateTime("not-a-date")
	if err == nil {
		t.Error("expected error for invalid date string")
	}
}

func TestParseDateTime_Empty(t *testing.T) {
	_, err := parseDateTime("")
	if err == nil {
		t.Error("expected error for empty string")
	}
}

// --- tripStartsWithin ---

func TestTripStartsWithin_LegInWindow(t *testing.T) {
	now := time.Now()
	future := now.Add(24 * time.Hour)
	deadline := now.Add(48 * time.Hour)

	trip := Trip{
		Legs: []TripLeg{
			{StartTime: future.Format("2006-01-02T15:04:05")},
		},
	}
	if !tripStartsWithin(trip, now, deadline) {
		t.Error("expected true for leg within window")
	}
}

func TestTripStartsWithin_LegBeforeWindow(t *testing.T) {
	now := time.Now()
	past := now.Add(-24 * time.Hour)
	deadline := now.Add(48 * time.Hour)

	trip := Trip{
		Legs: []TripLeg{
			{StartTime: past.Format("2006-01-02T15:04:05")},
		},
	}
	if tripStartsWithin(trip, now, deadline) {
		t.Error("expected false for leg before window")
	}
}

func TestTripStartsWithin_LegAfterWindow(t *testing.T) {
	now := time.Now()
	farFuture := now.Add(7 * 24 * time.Hour)
	deadline := now.Add(3 * 24 * time.Hour)

	trip := Trip{
		Legs: []TripLeg{
			{StartTime: farFuture.Format("2006-01-02T15:04:05")},
		},
	}
	if tripStartsWithin(trip, now, deadline) {
		t.Error("expected false for leg after window")
	}
}

func TestTripStartsWithin_NoLegs(t *testing.T) {
	now := time.Now()
	trip := Trip{}
	if tripStartsWithin(trip, now, now.Add(time.Hour)) {
		t.Error("expected false for trip with no legs")
	}
}

func TestTripStartsWithin_EmptyStartTime(t *testing.T) {
	now := time.Now()
	trip := Trip{
		Legs: []TripLeg{{StartTime: ""}},
	}
	if tripStartsWithin(trip, now, now.Add(time.Hour)) {
		t.Error("expected false for empty start time")
	}
}

func TestTripStartsWithin_InvalidStartTime(t *testing.T) {
	now := time.Now()
	trip := Trip{
		Legs: []TripLeg{{StartTime: "not-a-date"}},
	}
	if tripStartsWithin(trip, now, now.Add(time.Hour)) {
		t.Error("expected false for invalid start time")
	}
}

// --- generateID ---

func TestGenerateID_Format(t *testing.T) {
	id := generateID()
	if len(id) < 5 {
		t.Errorf("generated ID too short: %q", id)
	}
	if id[:5] != "trip_" {
		t.Errorf("expected ID to start with 'trip_', got %q", id)
	}
}

func TestGenerateID_Unique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 20; i++ {
		id := generateID()
		if seen[id] {
			t.Errorf("duplicate ID generated: %q", id)
		}
		seen[id] = true
	}
}

// --- saveJSON / loadJSON ---

func TestSaveLoadJSON_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/test.json"

	type testData struct {
		Name string `json:"name"`
		Val  int    `json:"val"`
	}

	data := testData{Name: "hello", Val: 42}
	if err := saveJSON(path, data); err != nil {
		t.Fatalf("saveJSON: %v", err)
	}

	var result testData
	if err := loadJSON(path, &result); err != nil {
		t.Fatalf("loadJSON: %v", err)
	}
	if result.Name != "hello" || result.Val != 42 {
		t.Errorf("round-trip failed: got %+v", result)
	}
}

func TestLoadJSON_MissingFile(t *testing.T) {
	var dst []Trip
	err := loadJSON("/nonexistent/path/file.json", &dst)
	if err != nil {
		t.Errorf("expected nil for missing file, got %v", err)
	}
}

func TestLoadJSON_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/empty.json"

	// Create empty file.
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create empty file: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close empty file: %v", err)
	}

	var dst []Trip
	err = loadJSON(path, &dst)
	if err != nil {
		t.Errorf("expected nil for empty file, got %v", err)
	}
}
