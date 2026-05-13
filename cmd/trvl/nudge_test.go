package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// shouldShowNudge
// ---------------------------------------------------------------------------

func envNone(string) string { return "" }
func termYes(int) bool      { return true }
func termNo(int) bool       { return false }

func TestShouldShowNudge_SearchCommand(t *testing.T) {
	if !shouldShowNudge("flights", "table", envNone, 1, termYes) {
		t.Error("flights + table + terminal should show nudge")
	}
}

func TestShouldShowNudge_NonSearchCommand(t *testing.T) {
	if shouldShowNudge("prefs", "table", envNone, 1, termYes) {
		t.Error("prefs should not trigger nudge")
	}
}

func TestShouldShowNudge_JSONFormat(t *testing.T) {
	if shouldShowNudge("flights", "json", envNone, 1, termYes) {
		t.Error("json format should suppress nudge")
	}
}

func TestShouldShowNudge_EnvSuppressed(t *testing.T) {
	env := func(k string) string {
		if k == "TRVL_NO_NUDGE" {
			return "1"
		}
		return ""
	}
	if shouldShowNudge("flights", "table", env, 1, termYes) {
		t.Error("TRVL_NO_NUDGE=1 should suppress nudge")
	}
}

func TestShouldShowNudge_PipedStderr(t *testing.T) {
	if shouldShowNudge("flights", "table", envNone, 1, termNo) {
		t.Error("non-terminal stderr should suppress nudge")
	}
}

func TestShouldShowNudge_AllSearchCommands(t *testing.T) {
	for name := range searchCommands {
		if !shouldShowNudge(name, "table", envNone, 1, termYes) {
			t.Errorf("search command %q should trigger nudge", name)
		}
	}
}

// ---------------------------------------------------------------------------
// loadNudgeState / saveNudgeState round-trip
// ---------------------------------------------------------------------------

func TestNudgeState_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nudge.json")

	s := nudgeState{SearchCount: 5, Shown: true}
	saveNudgeState(path, s)

	got := loadNudgeState(path)
	if got.SearchCount != 5 {
		t.Errorf("SearchCount = %d, want 5", got.SearchCount)
	}
	if !got.Shown {
		t.Error("Shown should be true")
	}
}

func TestNudgeState_MissingFile(t *testing.T) {
	got := loadNudgeState("/nonexistent/path/nudge.json")
	if got.SearchCount != 0 || got.Shown {
		t.Errorf("missing file should return zero state, got %+v", got)
	}
}

func TestNudgeState_CorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nudge.json")
	_ = os.WriteFile(path, []byte("not json"), 0o644)

	got := loadNudgeState(path)
	if got.SearchCount != 0 {
		t.Error("corrupt file should return zero state")
	}
}

// ---------------------------------------------------------------------------
// Threshold logic (integration-style, using state file directly)
// ---------------------------------------------------------------------------

func TestNudge_ThresholdBehavior(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nudge.json")

	// Simulate searches below threshold -- nudge should NOT fire.
	for i := 1; i < nudgeThreshold; i++ {
		s := loadNudgeState(path)
		s.SearchCount++
		saveNudgeState(path, s)

		got := loadNudgeState(path)
		if got.Shown {
			t.Fatalf("nudge fired at search %d, threshold is %d", i, nudgeThreshold)
		}
	}

	// Simulate the threshold-reaching search -- nudge SHOULD fire.
	s := loadNudgeState(path)
	s.SearchCount++
	if s.SearchCount >= nudgeThreshold {
		s.Shown = true
	}
	saveNudgeState(path, s)

	got := loadNudgeState(path)
	if !got.Shown {
		t.Error("nudge should have fired at threshold")
	}
	if got.SearchCount != nudgeThreshold {
		t.Errorf("SearchCount = %d, want %d", got.SearchCount, nudgeThreshold)
	}
}

func TestNudge_NeverShowsTwice(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nudge.json")

	// Write state as if nudge already shown.
	s := nudgeState{SearchCount: 10, Shown: true}
	saveNudgeState(path, s)

	// Load and verify it stays shown without incrementing display.
	got := loadNudgeState(path)
	if !got.Shown {
		t.Error("already-shown state should persist")
	}
}

func TestNudge_StateFileCreatesDir(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "sub", "deep")
	path := filepath.Join(nested, "nudge.json")

	saveNudgeState(path, nudgeState{SearchCount: 1})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("state file not created: %v", err)
	}

	var got nudgeState
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if got.SearchCount != 1 {
		t.Errorf("SearchCount = %d, want 1", got.SearchCount)
	}
}
