package trips

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// --- Store: Load/Save round-trip ---

func TestLoadSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	// Start empty — Load on missing files should not error.
	if err := s.Load(); err != nil {
		t.Fatalf("Load (empty): %v", err)
	}
	if got := s.List(); len(got) != 0 {
		t.Fatalf("expected empty, got %d", len(got))
	}

	// Add two trips.
	id1, err := s.Add(Trip{Name: "Alpha", Status: "planning"})
	if err != nil {
		t.Fatalf("Add alpha: %v", err)
	}
	id2, err := s.Add(Trip{Name: "Beta", Status: "booked"})
	if err != nil {
		t.Fatalf("Add beta: %v", err)
	}

	// Reload from disk in a fresh store.
	s2 := NewStore(dir)
	if err := s2.Load(); err != nil {
		t.Fatalf("Load (reload): %v", err)
	}

	trips := s2.List()
	if len(trips) != 2 {
		t.Fatalf("expected 2 trips, got %d", len(trips))
	}

	ids := map[string]bool{id1: true, id2: true}
	for _, trip := range trips {
		if !ids[trip.ID] {
			t.Errorf("unexpected ID %q", trip.ID)
		}
	}
}

// --- Add ---

func TestAddGeneratesID(t *testing.T) {
	s := NewStore(t.TempDir())
	id, err := s.Add(Trip{Name: "Test"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty ID")
	}
	if len(id) < 5 {
		t.Errorf("ID too short: %q", id)
	}
}

func TestAddDefaultsStatusToPlanning(t *testing.T) {
	s := NewStore(t.TempDir())
	id, err := s.Add(Trip{Name: "NoStatus"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	trip, err := s.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if trip.Status != "planning" {
		t.Errorf("Status = %q, want planning", trip.Status)
	}
}

func TestAddRequiresName(t *testing.T) {
	s := NewStore(t.TempDir())
	_, err := s.Add(Trip{})
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestAddRejectsInvalidStatus(t *testing.T) {
	s := NewStore(t.TempDir())
	_, err := s.Add(Trip{Name: "X", Status: "unknown"})
	if err == nil {
		t.Fatal("expected error for invalid status")
	}
}

func TestAddSetsTimestamps(t *testing.T) {
	s := NewStore(t.TempDir())
	before := time.Now().Add(-time.Second)
	id, _ := s.Add(Trip{Name: "Timestamps"})
	after := time.Now().Add(time.Second)

	trip, _ := s.Get(id)
	if trip.CreatedAt.Before(before) || trip.CreatedAt.After(after) {
		t.Errorf("CreatedAt out of range: %v", trip.CreatedAt)
	}
	if trip.UpdatedAt.Before(before) || trip.UpdatedAt.After(after) {
		t.Errorf("UpdatedAt out of range: %v", trip.UpdatedAt)
	}
}

// --- Get ---

func TestGetNotFound(t *testing.T) {
	s := NewStore(t.TempDir())
	_, err := s.Get("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent trip")
	}
}

func TestGetReturnsCopy(t *testing.T) {
	s := NewStore(t.TempDir())
	id, _ := s.Add(Trip{Name: "Original"})
	p, _ := s.Get(id)
	p.Name = "Mutated"

	p2, _ := s.Get(id)
	if p2.Name == "Mutated" {
		t.Error("Get should return a copy, not a reference to internal state")
	}
}

// --- Update ---

func TestUpdateModifiesTrip(t *testing.T) {
	s := NewStore(t.TempDir())
	id, _ := s.Add(Trip{Name: "Orig", Status: "planning"})

	err := s.Update(id, func(t *Trip) error {
		t.Status = "booked"
		t.Notes = "Updated"
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	trip, _ := s.Get(id)
	if trip.Status != "booked" {
		t.Errorf("Status = %q, want booked", trip.Status)
	}
	if trip.Notes != "Updated" {
		t.Errorf("Notes = %q, want Updated", trip.Notes)
	}
}

func TestUpdatePropagatesError(t *testing.T) {
	s := NewStore(t.TempDir())
	id, _ := s.Add(Trip{Name: "X"})

	sentinel := errors.New("fn error")
	err := s.Update(id, func(_ *Trip) error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Errorf("Update: got %v, want sentinel", err)
	}
}

func TestUpdateNotFound(t *testing.T) {
	s := NewStore(t.TempDir())
	err := s.Update("missing", func(_ *Trip) error { return nil })
	if err == nil {
		t.Fatal("expected error for nonexistent trip")
	}
}

func TestUpdateSetsUpdatedAt(t *testing.T) {
	s := NewStore(t.TempDir())
	id, _ := s.Add(Trip{Name: "TS"})

	original, _ := s.Get(id)
	time.Sleep(2 * time.Millisecond)

	_ = s.Update(id, func(t *Trip) error { return nil })
	updated, _ := s.Get(id)

	if !updated.UpdatedAt.After(original.UpdatedAt) {
		t.Errorf("UpdatedAt not advanced: orig=%v updated=%v", original.UpdatedAt, updated.UpdatedAt)
	}
}

// --- Delete ---

func TestDeleteRemovesTrip(t *testing.T) {
	s := NewStore(t.TempDir())
	id, _ := s.Add(Trip{Name: "ToDelete"})

	if err := s.Delete(id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(id); err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestDeleteNotFound(t *testing.T) {
	s := NewStore(t.TempDir())
	err := s.Delete("ghost")
	if err == nil {
		t.Fatal("expected error for nonexistent trip")
	}
}

// --- Active filter ---

func TestActive(t *testing.T) {
	s := NewStore(t.TempDir())
	for _, trip := range []Trip{
		{Name: "Planning", Status: "planning"},
		{Name: "Booked", Status: "booked"},
		{Name: "InProg", Status: "in_progress"},
		{Name: "Done", Status: "completed"},
		{Name: "Gone", Status: "cancelled"},
	} {
		if _, err := s.Add(trip); err != nil {
			t.Fatalf("Add(%s): %v", trip.Name, err)
		}
	}

	active := s.Active()
	if len(active) != 3 {
		t.Errorf("Active() = %d, want 3", len(active))
	}
	for _, a := range active {
		if !activeStatuses[a.Status] {
			t.Errorf("unexpected inactive status %q in Active()", a.Status)
		}
	}
}

// --- Upcoming filter ---

func TestUpcoming(t *testing.T) {
	s := NewStore(t.TempDir())
	now := time.Now()

	// Leg starting in 6 hours — should appear in Upcoming(24h).
	soon := now.Add(6 * time.Hour)
	if _, err := s.Add(Trip{
		Name:   "Soon",
		Status: "booked",
		Legs:   []TripLeg{{Type: "flight", From: "HEL", To: "AMS", StartTime: soon.Format("2006-01-02T15:04")}},
	}); err != nil {
		t.Fatalf("Add Soon: %v", err)
	}

	// Leg starting in 5 days — outside 24h window.
	far := now.Add(5 * 24 * time.Hour)
	if _, err := s.Add(Trip{
		Name:   "Far",
		Status: "booked",
		Legs:   []TripLeg{{Type: "flight", From: "AMS", To: "PRG", StartTime: far.Format("2006-01-02T15:04")}},
	}); err != nil {
		t.Fatalf("Add Far: %v", err)
	}

	// Completed trip should be excluded even with a near leg.
	if _, err := s.Add(Trip{
		Name:   "Done",
		Status: "completed",
		Legs:   []TripLeg{{Type: "flight", From: "X", To: "Y", StartTime: soon.Format("2006-01-02T15:04")}},
	}); err != nil {
		t.Fatalf("Add Done: %v", err)
	}

	upcoming := s.Upcoming(24 * time.Hour)
	if len(upcoming) != 1 {
		t.Errorf("Upcoming(24h) = %d, want 1", len(upcoming))
	}
	if len(upcoming) > 0 && upcoming[0].Name != "Soon" {
		t.Errorf("expected Soon, got %q", upcoming[0].Name)
	}
}

func TestUpcomingEmpty(t *testing.T) {
	s := NewStore(t.TempDir())
	if _, err := s.Add(Trip{Name: "NoLegs", Status: "planning"}); err != nil {
		t.Fatalf("Add NoLegs: %v", err)
	}

	if got := s.Upcoming(24 * time.Hour); len(got) != 0 {
		t.Errorf("expected 0 upcoming, got %d", len(got))
	}
}

func TestUpcomingPastLeg(t *testing.T) {
	s := NewStore(t.TempDir())
	past := time.Now().Add(-1 * time.Hour)
	if _, err := s.Add(Trip{
		Name:   "Past",
		Status: "booked",
		Legs:   []TripLeg{{Type: "flight", From: "A", To: "B", StartTime: past.Format("2006-01-02T15:04")}},
	}); err != nil {
		t.Fatalf("Add Past: %v", err)
	}

	if got := s.Upcoming(48 * time.Hour); len(got) != 0 {
		t.Errorf("past leg should not appear in Upcoming: %d", len(got))
	}
}

// --- File permissions ---

func TestFilePermissions(t *testing.T) {
	if runtime.GOOS != "windows" && os.Getuid() == 0 {
		t.Skip("permission test not meaningful as root")
	}
	dir := t.TempDir()
	s := NewStore(dir)
	_, _ = s.Add(Trip{Name: "Perm"})

	for _, fname := range []string{"trips.json", "alerts.json"} {
		info, err := os.Stat(filepath.Join(dir, fname))
		if err != nil {
			t.Fatalf("stat %s: %v", fname, err)
		}
		assertCrossPlatformPrivateFile(t, filepath.Join(dir, fname), info)
	}
}

func assertCrossPlatformPrivateFile(t *testing.T, path string, info os.FileInfo) {
	t.Helper()

	if !info.Mode().IsRegular() {
		t.Fatalf("%s is not a regular file: %v", path, info.Mode())
	}

	perm := info.Mode().Perm()
	if runtime.GOOS == "windows" {
		if perm != 0o666 {
			t.Errorf("%s permissions on Windows: got %o, want 666", path, perm)
		}
		return
	}

	if perm != 0o600 {
		t.Errorf("%s mode = %o, want 0600", path, perm)
	}
}

// --- Alerts ---

func TestAddAndReadAlerts(t *testing.T) {
	s := NewStore(t.TempDir())

	a := Alert{TripID: "trip_abc", TripName: "Test", Type: "reminder", Message: "Go!"}
	if err := s.AddAlert(a); err != nil {
		t.Fatalf("AddAlert: %v", err)
	}

	all := s.Alerts(false)
	if len(all) != 1 {
		t.Fatalf("Alerts = %d, want 1", len(all))
	}
	if all[0].Message != "Go!" {
		t.Errorf("Message = %q", all[0].Message)
	}
}

func TestMarkAlertsRead(t *testing.T) {
	s := NewStore(t.TempDir())
	_ = s.AddAlert(Alert{TripID: "x", Type: "reminder", Message: "A"})
	_ = s.AddAlert(Alert{TripID: "x", Type: "reminder", Message: "B"})

	if err := s.MarkAlertsRead(); err != nil {
		t.Fatalf("MarkAlertsRead: %v", err)
	}

	unread := s.Alerts(true)
	if len(unread) != 0 {
		t.Errorf("expected 0 unread, got %d", len(unread))
	}
}

// --- FirstLegStart ---

func TestFirstLegStart(t *testing.T) {
	later := time.Now().Add(24 * time.Hour)
	earlier := time.Now().Add(2 * time.Hour)

	trip := Trip{
		Name: "Multi-leg",
		Legs: []TripLeg{
			{StartTime: later.Format("2006-01-02T15:04")},
			{StartTime: earlier.Format("2006-01-02T15:04")},
		},
	}
	got := FirstLegStart(trip)
	if got.IsZero() {
		t.Fatal("expected non-zero time")
	}
	// Should be the earlier time (within a minute of parsing precision).
	if got.After(earlier.Add(time.Minute)) {
		t.Errorf("FirstLegStart = %v, expected ~%v", got, earlier)
	}
}

func TestFirstLegStartNoLegs(t *testing.T) {
	if !FirstLegStart(Trip{Name: "empty"}).IsZero() {
		t.Error("expected zero time for trip with no legs")
	}
}

// --- parseDateTime ---

func TestParseDateTime(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"2026-04-11T18:25", "2026-04-11T18:25"},
		{"2026-04-11", "2026-04-11"},
		{"2026-04-11T18:25:00", "2026-04-11T18:25:00"},
	}
	for _, c := range cases {
		ts, err := parseDateTime(c.in)
		if err != nil {
			t.Errorf("parseDateTime(%q): %v", c.in, err)
			continue
		}
		if ts.IsZero() {
			t.Errorf("parseDateTime(%q) = zero", c.in)
		}
	}

	_, err := parseDateTime("not-a-date")
	if err == nil {
		t.Error("expected error for invalid date")
	}
}
