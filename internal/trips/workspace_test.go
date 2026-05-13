package trips

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNormalizeTripInitializesWorkspaceV2(t *testing.T) {
	tr := Trip{Name: "Prague weekend", Status: "planning"}
	got := NormalizeWorkspace(tr)
	if got.SchemaVersion != CurrentSchemaVersion {
		t.Fatalf("SchemaVersion = %d, want %d", got.SchemaVersion, CurrentSchemaVersion)
	}
	if got.Workspace == nil {
		t.Fatalf("Workspace is nil")
	}
	if got.Legs == nil {
		t.Fatalf("Legs should be initialized to empty slice")
	}
	if got.Workspace.Candidates == nil || got.Workspace.ImportedRecords == nil {
		t.Fatalf("workspace slices should be initialized: %#v", got.Workspace)
	}
}

func TestLoadLegacyTripStillWorks(t *testing.T) {
	legacy := `[
	  {"id":"trip_legacy","name":"Legacy","status":"planning","created_at":"2026-05-01T00:00:00Z","updated_at":"2026-05-01T00:00:00Z","legs":[]}
	]`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "trips.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewStore(dir)
	if err := store.Load(); err != nil {
		t.Fatal(err)
	}
	got := store.List()[0]
	if got.SchemaVersion != CurrentSchemaVersion || got.Workspace == nil {
		t.Fatalf("legacy trip was not normalized: %#v", got)
	}
}

func TestMergeReservationArtifactsIsIdempotent(t *testing.T) {
	now := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	rec := ImportedRecord{
		Type:       "flight",
		Provider:   "KLM",
		Reference:  "ABC123",
		ImportedAt: now,
		TravelDate: "2026-07-01",
		From:       "HEL",
		To:         "AMS",
	}
	leg := TripLeg{
		Type:      "flight",
		From:      "HEL",
		To:        "AMS",
		Provider:  "KLM",
		StartTime: "2026-07-01",
		Reference: "ABC123",
		Confirmed: true,
	}
	action := ActionItem{Type: "watch", Title: "Re-check imported KLM booking", RelatedID: ImportedRecordID(rec)}

	tr := NormalizeWorkspace(Trip{Name: "Summer", Status: "planning"})
	var summary MergeSummary
	tr, summary = MergeReservationArtifacts(tr, []ImportedRecord{rec}, []TripLeg{leg}, []ActionItem{action})
	if summary.ImportedRecordsAdded != 1 || summary.LegsAdded != 1 || summary.ActionsAdded != 1 {
		t.Fatalf("first merge summary = %#v", summary)
	}
	_, summary = MergeReservationArtifacts(tr, []ImportedRecord{rec}, []TripLeg{leg}, []ActionItem{action})
	if summary.ImportedRecordsAdded != 0 || summary.LegsAdded != 0 || summary.ActionsAdded != 0 {
		t.Fatalf("second merge should be idempotent, got %#v", summary)
	}
}

func TestBookingCandidateStaleness(t *testing.T) {
	now := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	fresh := BookingCandidate{CheckedAt: now.Add(-time.Hour)}
	if fresh.IsStale(now, 24*time.Hour) {
		t.Fatalf("recent candidate should be fresh")
	}
	old := BookingCandidate{CheckedAt: now.Add(-48 * time.Hour)}
	if !old.IsStale(now, 24*time.Hour) {
		t.Fatalf("old candidate should be stale")
	}
	expired := BookingCandidate{CheckedAt: now, ExpiresAt: now.Add(-time.Minute)}
	if !expired.IsStale(now, 24*time.Hour) {
		t.Fatalf("expired candidate should be stale")
	}
}
