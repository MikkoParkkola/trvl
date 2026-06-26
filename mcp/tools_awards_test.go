package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleSearchAwards_EmptySeats(t *testing.T) {
	t.Parallel()
	content, _, err := handleSearchAwards(context.Background(), map[string]any{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content) == 0 || !strings.Contains(content[0].Text, "No award sweet spots") {
		t.Fatalf("expected no-spots message, got %q", content[0].Text)
	}
}

func TestHandleSearchAwards_NativeRedemption(t *testing.T) {
	t.Parallel()
	args := map[string]any{
		"seats": []interface{}{
			map[string]interface{}{
				"program":           "VS",
				"origin":            "AMS",
				"destination":       "JFK",
				"date":              "2026-08-01",
				"cabin":             "economy",
				"miles_cost":        50000,
				"cash_fees":         55.0,
				"cash_equivalent":   600.0,
				"bookable_segments": 1,
			},
		},
		"balances": []interface{}{
			map[string]interface{}{"program": "VS", "balance": 60000},
		},
	}
	content, structured, err := handleSearchAwards(context.Background(), args, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content) < 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(content))
	}
	if !strings.Contains(content[0].Text, "AMS") || !strings.Contains(content[0].Text, "JFK") {
		t.Fatalf("summary missing route, got %q", content[0].Text)
	}

	b, _ := json.Marshal(structured)
	var resp struct {
		Count      int `json:"count"`
		SweetSpots []struct {
			Program       string  `json:"program"`
			Affordable    bool    `json:"affordable"`
			CentsPerPoint float64 `json:"cents_per_point"`
		} `json:"sweet_spots"`
	}
	if err := json.Unmarshal(b, &resp); err != nil {
		t.Fatalf("structured unmarshal: %v", err)
	}
	if resp.Count == 0 {
		t.Fatal("expected at least 1 sweet spot")
	}
	// At least one spot must be affordable (VS native has enough balance).
	anyAffordable := false
	for _, sp := range resp.SweetSpots {
		if sp.Affordable {
			anyAffordable = true
		}
	}
	if !anyAffordable {
		t.Fatal("want at least one affordable=true spot when VS balance covers miles_cost")
	}
}

func TestHandleSearchAwards_CabinFilter(t *testing.T) {
	t.Parallel()
	args := map[string]any{
		"seats": []interface{}{
			map[string]interface{}{
				"program": "VS", "origin": "AMS", "destination": "JFK",
				"date": "2026-08-01", "cabin": "business",
				"miles_cost": 80000, "cash_fees": 100.0, "cash_equivalent": 1200.0,
				"bookable_segments": 1,
			},
			map[string]interface{}{
				"program": "VS", "origin": "AMS", "destination": "JFK",
				"date": "2026-08-01", "cabin": "economy",
				"miles_cost": 50000, "cash_fees": 55.0, "cash_equivalent": 600.0,
				"bookable_segments": 1,
			},
		},
		"balances": []interface{}{
			map[string]interface{}{"program": "VS", "balance": 100000},
		},
		"cabin": "business",
	}
	_, structured, err := handleSearchAwards(context.Background(), args, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b, _ := json.Marshal(structured)
	var resp struct {
		Count      int `json:"count"`
		SweetSpots []struct {
			Cabin string `json:"cabin"`
		} `json:"sweet_spots"`
	}
	if err := json.Unmarshal(b, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Count == 0 {
		t.Fatal("expected at least 1 business-cabin spot")
	}
	for _, sp := range resp.SweetSpots {
		if sp.Cabin != "business" {
			t.Fatalf("cabin filter leaking: got cabin=%q, want business", sp.Cabin)
		}
	}
}

func TestHandleSearchAwards_TransferRoute(t *testing.T) {
	t.Parallel()
	// User holds MR (Amex) and transfers to VS at 1:1 to book a seat.
	args := map[string]any{
		"seats": []interface{}{
			map[string]interface{}{
				"program": "VS", "origin": "LHR", "destination": "JFK",
				"date": "2026-09-15", "cabin": "economy",
				"miles_cost": 20000, "cash_fees": 40.0, "cash_equivalent": 400.0,
				"bookable_segments": 1,
			},
		},
		"balances": []interface{}{
			map[string]interface{}{"program": "MR", "balance": 25000},
		},
	}
	content, _, err := handleSearchAwards(context.Background(), args, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// MR -> VS transfer route should appear in summary.
	if !strings.Contains(content[0].Text, "MR") {
		t.Fatalf("expected MR transfer route in summary, got: %q", content[0].Text)
	}
}

// TestHandleSearchAwards_SeedsFromProfile proves that when the caller
// passes no balances, the program set is seeded from the traveller's
// saved loyalty profile. It points HOME at a temp dir holding a known
// preferences.json so the assertion is deterministic and filesystem-
// independent. Not parallel: it mutates process-wide HOME.
func TestHandleSearchAwards_SeedsFromProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)        // unix
	t.Setenv("USERPROFILE", home) // windows
	trvlDir := filepath.Join(home, ".trvl")
	if err := os.MkdirAll(trvlDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	prefsJSON := `{"frequent_flyer_programs":[{"alliance":"skyteam","tier":"elite","airline_code":"VS","miles_balance":60000}]}`
	if err := os.WriteFile(filepath.Join(trvlDir, "preferences.json"), []byte(prefsJSON), 0o644); err != nil {
		t.Fatalf("write prefs: %v", err)
	}

	args := map[string]any{
		"seats": []interface{}{
			map[string]interface{}{
				"program": "VS", "origin": "AMS", "destination": "JFK",
				"date": "2026-08-01", "cabin": "economy",
				"miles_cost": 50000, "cash_fees": 55.0, "cash_equivalent": 600.0,
				"bookable_segments": 1,
			},
		},
		// No "balances" — must come from the profile.
	}
	_, structured, err := handleSearchAwards(context.Background(), args, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b, _ := json.Marshal(structured)
	var resp struct {
		Count      int `json:"count"`
		SweetSpots []struct {
			SourceProgram string `json:"source_program"`
			Affordable    bool   `json:"affordable"`
		} `json:"sweet_spots"`
	}
	if err := json.Unmarshal(b, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Count == 0 {
		t.Fatal("expected profile-seeded VS balance to produce a sweet spot")
	}
	gotAffordableVS := false
	for _, sp := range resp.SweetSpots {
		if sp.SourceProgram == "VS" && sp.Affordable {
			gotAffordableVS = true
		}
	}
	if !gotAffordableVS {
		t.Errorf("want an affordable VS spot seeded from the profile, got %+v", resp.SweetSpots)
	}
}

// TestHandleSearchAwards_ExplicitBalanceOverridesProfile proves an
// explicit balance wins even when the on-disk profile would seed a
// different program. Not parallel: mutates HOME.
func TestHandleSearchAwards_ExplicitBalanceOverridesProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	trvlDir := filepath.Join(home, ".trvl")
	if err := os.MkdirAll(trvlDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Profile would seed AY only; the explicit VS balance must win.
	prefsJSON := `{"frequent_flyer_programs":[{"airline_code":"AY","miles_balance":1000}]}`
	if err := os.WriteFile(filepath.Join(trvlDir, "preferences.json"), []byte(prefsJSON), 0o644); err != nil {
		t.Fatalf("write prefs: %v", err)
	}

	args := map[string]any{
		"seats": []interface{}{
			map[string]interface{}{
				"program": "VS", "origin": "AMS", "destination": "JFK",
				"date": "2026-08-01", "cabin": "economy",
				"miles_cost": 50000, "cash_fees": 55.0, "cash_equivalent": 600.0,
				"bookable_segments": 1,
			},
		},
		"balances": []interface{}{
			map[string]interface{}{"program": "VS", "balance": 60000},
		},
	}
	_, structured, err := handleSearchAwards(context.Background(), args, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b, _ := json.Marshal(structured)
	var resp struct {
		SweetSpots []struct {
			SourceProgram string `json:"source_program"`
			Affordable    bool   `json:"affordable"`
		} `json:"sweet_spots"`
	}
	if err := json.Unmarshal(b, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	gotAffordableVS := false
	for _, sp := range resp.SweetSpots {
		if sp.SourceProgram == "VS" && sp.Affordable {
			gotAffordableVS = true
		}
		if sp.SourceProgram == "AY" {
			t.Errorf("profile AY leaked despite explicit VS balance: %+v", sp)
		}
	}
	if !gotAffordableVS {
		t.Errorf("explicit VS balance should produce an affordable VS spot, got %+v", resp.SweetSpots)
	}
}
