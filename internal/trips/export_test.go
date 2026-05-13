package trips

import (
	"strings"
	"testing"
)

func TestExportJSONRoundTripsWorkspace(t *testing.T) {
	tr := NormalizeWorkspace(Trip{
		Name:   "Japan",
		Status: "planning",
		Workspace: &Workspace{
			Candidates: []BookingCandidate{{
				Type:     "hotel",
				Provider: "Google Hotels",
				Title:    "Shinjuku stay",
				Price:    120,
				Currency: "EUR",
			}},
		},
	})
	data, err := ExportJSON(tr)
	if err != nil {
		t.Fatalf("ExportJSON: %v", err)
	}
	got, err := ImportJSON(data)
	if err != nil {
		t.Fatalf("ImportJSON: %v", err)
	}
	if got.SchemaVersion != CurrentSchemaVersion {
		t.Fatalf("SchemaVersion = %d", got.SchemaVersion)
	}
	if len(got.Workspace.Candidates) != 1 || got.Workspace.Candidates[0].ID == "" {
		t.Fatalf("candidate did not round-trip with stable ID: %#v", got.Workspace.Candidates)
	}
}

func TestExportMarkdownIncludesWorkspaceSignals(t *testing.T) {
	tr := NormalizeWorkspace(Trip{
		Name:   "Japan",
		Status: "planning",
		Legs: []TripLeg{{
			Type:      "flight",
			From:      "HEL",
			To:        "NRT",
			Provider:  "Finnair",
			StartTime: "2026-07-01",
		}},
		Workspace: &Workspace{
			Days: []DayPlan{{
				Title:                 "Tokyo",
				PlaceIDs:              []string{"place_1", "place_2"},
				EstimatedRouteMinutes: 44,
			}},
			UnresolvedActions: []ActionItem{{Title: "Verify hotel cancellation", Status: "open"}},
		},
	})
	md := ExportMarkdown(tr)
	for _, want := range []string{"# Japan", "HEL -> NRT", "44 min route time", "Verify hotel cancellation"} {
		if !strings.Contains(md, want) {
			t.Fatalf("markdown missing %q:\n%s", want, md)
		}
	}
}
