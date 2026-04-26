package mcp

import (
	"context"
	"encoding/json"
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
