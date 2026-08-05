package mcp

import (
	"context"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/ground"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/profile"
)

func TestHandleSearchGround_ProfileDoesNotSeedType(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	// Persist a minimal profile fixture whose dominant ground mode is train.
	// OLD pre-fix code in handleSearchGround would load via profile.Load +
	// profile.GroundHints and seed opts.Type="train" (hard filter) when no
	// explicit "type" arg was passed. This test (with fixture) ensures the
	// MCP handler never consults profile for hard-filter seeding of Type.
	p := &profile.TravelProfile{
		TopGroundModes: []profile.ModeStats{{Mode: "train", Count: 100}},
	}
	if err := profile.Save(p); err != nil {
		t.Fatalf("persist profile fixture: %v", err)
	}

	// Confirm the fixture would trigger the old bug path.
	if h := profile.GroundHints(p, "Prague", "Vienna"); h.PreferredType != "train" {
		t.Fatalf("GroundHints.PreferredType = %q, want train", h.PreferredType)
	}

	orig := searchGroundByNameFunc
	t.Cleanup(func() { searchGroundByNameFunc = orig })

	var captured ground.SearchOptions
	searchGroundByNameFunc = func(_ context.Context, _, _, _ string, opts ground.SearchOptions) (*models.GroundSearchResult, error) {
		captured = opts
		return &models.GroundSearchResult{Success: true, Count: 0}, nil
	}

	t.Run("no explicit type yields empty opts.Type", func(t *testing.T) {
		captured = ground.SearchOptions{}
		_, _, err := handleSearchGround(context.Background(), map[string]any{
			"from": "Prague",
			"to":   "Vienna",
			"date": "2026-07-10",
		}, nil, nil, nil)
		if err != nil {
			t.Fatalf("handleSearchGround without type: %v", err)
		}
		if captured.Type != "" {
			t.Errorf("opts.Type = %q, want empty (profile-derived train must not seed hard filter)", captured.Type)
		}
	})

	t.Run("explicit type bus is passed through", func(t *testing.T) {
		captured = ground.SearchOptions{}
		_, _, err := handleSearchGround(context.Background(), map[string]any{
			"from": "Prague",
			"to":   "Vienna",
			"date": "2026-07-10",
			"type": "bus",
		}, nil, nil, nil)
		if err != nil {
			t.Fatalf("handleSearchGround with type: %v", err)
		}
		if captured.Type != "bus" {
			t.Errorf("opts.Type = %q, want bus", captured.Type)
		}
	})
}
