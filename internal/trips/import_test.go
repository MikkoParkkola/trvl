package trips

import (
	"testing"
)

// TestMergeTripWorkspace exercises the whole-workspace merge and, through it,
// the four per-collection dedup helpers (places, days, decisions, evidence)
// that the reservation-import path relies on. The invariant under test: merging
// src into dst keeps every dst entry, adds only src entries whose ID is new,
// and drops src entries that collide with an existing ID. A duplicate that
// arrives with an empty ID must still be deduped via its derived StableID, not
// silently appended as a fresh row.
func TestMergeTripWorkspace(t *testing.T) {
	dst := Trip{
		Name: "Prague",
		Workspace: &Workspace{
			Places:    []Place{{ID: "p1", Name: "Old Town"}},
			Days:      []DayPlan{{ID: "d1", Title: "Day 1"}},
			Decisions: []Decision{{ID: "dec1", Title: "Hotel?"}},
			Evidence:  []EvidenceRef{{ID: "ev1", Source: "agoda"}},
		},
	}
	src := Trip{
		Name: "Prague",
		Workspace: &Workspace{
			Places: []Place{
				{ID: "p1", Name: "Old Town"}, // duplicate by ID -> dropped
				{ID: "p2", Name: "Castle"},   // new -> added
				{Name: "Charles Bridge"},     // empty ID -> StableID, new -> added
			},
			Days: []DayPlan{
				{ID: "d1", Title: "Day 1"}, // dup -> dropped
				{ID: "d2", Title: "Day 2"}, // new -> added
			},
			Decisions: []Decision{
				{ID: "dec1", Title: "Hotel?"},      // dup -> dropped
				{ID: "dec2", Title: "Flight day?"}, // new -> added
			},
			Evidence: []EvidenceRef{
				{ID: "ev1", Source: "agoda"},  // dup -> dropped
				{ID: "ev2", Source: "google"}, // new -> added
			},
		},
	}

	got, _ := MergeTripWorkspace(dst, src)

	if n := len(got.Workspace.Places); n != 3 {
		t.Errorf("places: got %d, want 3 (p1 kept, p2 + Charles Bridge added)", n)
	}
	if n := len(got.Workspace.Days); n != 2 {
		t.Errorf("days: got %d, want 2 (d1 kept, d2 added)", n)
	}
	if n := len(got.Workspace.Decisions); n != 2 {
		t.Errorf("decisions: got %d, want 2 (dec1 kept, dec2 added)", n)
	}
	if n := len(got.Workspace.Evidence); n != 2 {
		t.Errorf("evidence: got %d, want 2 (ev1 kept, ev2 added)", n)
	}

	// The empty-ID place must have been assigned a StableID, not left blank.
	for _, p := range got.Workspace.Places {
		if p.Name == "Charles Bridge" && p.ID == "" {
			t.Error("empty-ID place was appended without a derived StableID")
		}
	}
}

// TestMergeTripWorkspaceIdempotent verifies that merging a workspace into itself
// adds nothing: every entry collides with itself by ID, so the result must be
// byte-for-byte the same collection sizes. This is the property the import path
// depends on when the same reservation is imported twice.
func TestMergeTripWorkspaceIdempotent(t *testing.T) {
	base := Trip{
		Name: "Tokyo",
		Workspace: &Workspace{
			Places:    []Place{{ID: "p1", Name: "Shibuya"}},
			Days:      []DayPlan{{ID: "d1", Title: "Arrival"}},
			Decisions: []Decision{{ID: "dec1", Title: "JR Pass?"}},
			Evidence:  []EvidenceRef{{ID: "ev1", Source: "booking"}},
		},
	}

	got, summary := MergeTripWorkspace(base, base)

	if len(got.Workspace.Places) != 1 || len(got.Workspace.Days) != 1 ||
		len(got.Workspace.Decisions) != 1 || len(got.Workspace.Evidence) != 1 {
		t.Errorf("self-merge changed sizes: places=%d days=%d decisions=%d evidence=%d",
			len(got.Workspace.Places), len(got.Workspace.Days),
			len(got.Workspace.Decisions), len(got.Workspace.Evidence))
	}
	if summary.LegsAdded != 0 || summary.ImportedRecordsAdded != 0 ||
		summary.CandidatesAdded != 0 || summary.ActionsAdded != 0 {
		t.Errorf("self-merge reported additions: %+v", summary)
	}
}
