package main

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestHotelsCmd_ChildrenFlagDefault verifies the --children flag exists and
// defaults to 0 (no exclusion).
func TestHotelsCmd_ChildrenFlagDefault(t *testing.T) {
	cmd := hotelsCmd()
	f := cmd.Flags().Lookup("children")
	if f == nil {
		t.Fatal("expected --children flag to be registered")
	}
	if f.DefValue != "0" {
		t.Errorf("expected --children default 0, got %q", f.DefValue)
	}
}

// TestExcludeAdultsOnly verifies adults-only properties are filtered out and
// counted, without mutating the input slice.
func TestExcludeAdultsOnly(t *testing.T) {
	in := []models.HotelResult{
		{Name: "Family Resort"},
		{Name: "TUI BLUE Madeira Gardens", AdultsOnly: true},
		{Name: "City Hotel"},
		{Name: "Quiet Retreat", AdultsOnly: true},
	}

	kept, hidden := excludeAdultsOnly(in)

	if hidden != 2 {
		t.Errorf("expected 2 hidden, got %d", hidden)
	}
	if len(kept) != 2 {
		t.Fatalf("expected 2 kept, got %d", len(kept))
	}
	for _, h := range kept {
		if h.AdultsOnly {
			t.Errorf("adults-only hotel %q survived exclusion", h.Name)
		}
	}
	// Input slice must not be mutated.
	if len(in) != 4 {
		t.Errorf("input slice mutated: len=%d", len(in))
	}
}

// TestExcludeAdultsOnly_NoneFlagged verifies a party-safe result set passes
// through unchanged.
func TestExcludeAdultsOnly_NoneFlagged(t *testing.T) {
	in := []models.HotelResult{{Name: "A"}, {Name: "B"}}
	kept, hidden := excludeAdultsOnly(in)
	if hidden != 0 {
		t.Errorf("expected 0 hidden, got %d", hidden)
	}
	if len(kept) != 2 {
		t.Errorf("expected 2 kept, got %d", len(kept))
	}
}
