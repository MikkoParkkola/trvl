package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/watch"
)

// TestHandleCheckWatches_ReportsAlertClearedByCurrencyChange proves round 22's
// fix #4: when a currency mismatch clears a watch's alert threshold, the MCP
// tool's JSON response surfaces alert_cleared_by_currency_change:true so a
// client (not just the CLI notifier) can tell the user their threshold
// vanished. Before the fix, CheckResult.AlertsClearedByCurrencyChange (fix
// #2) existed internally but was never wired into the response DTO.
func TestHandleCheckWatches_ReportsAlertClearedByCurrencyChange(t *testing.T) {
	watchHome := t.TempDir()
	t.Setenv("HOME", watchHome)
	t.Setenv("USERPROFILE", watchHome)

	store, err := watch.DefaultStore()
	if err != nil {
		t.Fatalf("DefaultStore: %v", err)
	}
	if _, _, err := store.Add(watch.Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "CDG",
		DepartDate:  "2026-09-01",
		Currency:    "USD",
		BelowPrice:  500,
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	prev := checkWatchesChecker
	checkWatchesChecker = fixedChecker{price: 450, currency: "EUR"} // mismatches watch's USD.
	defer func() { checkWatchesChecker = prev }()

	_, structured, err := handleCheckWatches(context.Background(), nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("handleCheckWatches: %v", err)
	}

	raw, err := json.Marshal(structured)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(raw)
	if !strings.Contains(got, `"alert_cleared_by_currency_change":true`) {
		t.Errorf("expected alert_cleared_by_currency_change:true in response, got %s", got)
	}
}

// TestHandleCheckWatches_OmitsAlertClearedFieldWhenNotCleared is the negative
// case: the omitempty JSON tag must keep the field out entirely (not just
// false) when nothing was cleared, matching the response's existing
// terse-on-the-happy-path convention.
func TestHandleCheckWatches_OmitsAlertClearedFieldWhenNotCleared(t *testing.T) {
	watchHome := t.TempDir()
	t.Setenv("HOME", watchHome)
	t.Setenv("USERPROFILE", watchHome)

	store, err := watch.DefaultStore()
	if err != nil {
		t.Fatalf("DefaultStore: %v", err)
	}
	if _, _, err := store.Add(watch.Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "CDG",
		DepartDate:  "2026-09-01",
		Currency:    "EUR",
		BelowPrice:  500,
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	prev := checkWatchesChecker
	checkWatchesChecker = fixedChecker{price: 123, currency: "EUR"} // matches, no mismatch.
	defer func() { checkWatchesChecker = prev }()

	_, structured, err := handleCheckWatches(context.Background(), nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("handleCheckWatches: %v", err)
	}

	raw, err := json.Marshal(structured)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(raw)
	if strings.Contains(got, `alert_cleared_by_currency_change`) {
		t.Errorf("expected alert_cleared_by_currency_change omitted (omitempty), got %s", got)
	}
}
