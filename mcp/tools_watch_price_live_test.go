package mcp

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/watch"
)

// fixedChecker is a deterministic watch.PriceChecker for handler tests.
type fixedChecker struct {
	price    float64
	currency string
}

func (f fixedChecker) CheckPrice(_ context.Context, _ watch.Watch) (float64, string, string, error) {
	return f.price, f.currency, "", nil
}

// TestHandleCheckWatches_ReturnsLivePrice proves the end-to-end wiring: a watch
// is re-priced through the injected checker and the real price surfaces in the
// tool response. Before the fix the response always carried current_price 0
// regardless of the checker. Not parallel: it sets HOME and swaps a package var.
func TestHandleCheckWatches_ReturnsLivePrice(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	store, err := watch.DefaultStore()
	if err != nil {
		t.Fatalf("DefaultStore: %v", err)
	}
	if _, err := store.Add(watch.Watch{
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
	checkWatchesChecker = fixedChecker{price: 123, currency: "EUR"}
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
	if !strings.Contains(got, `"checked":1`) {
		t.Errorf("expected checked:1, got %s", got)
	}
	if !strings.Contains(got, `"current_price":123`) {
		t.Errorf("expected current_price:123 (live price must flow through), got %s", got)
	}
	// 123 <= 500 target, so the watch should trigger.
	if !strings.Contains(got, `"below_goal":true`) {
		t.Errorf("expected below_goal:true, got %s", got)
	}
}

// TestHandleCheckWatches_LiveProbe re-prices a real watch against live providers.
// Probe-gated so the default suite stays offline (per CLAUDE.md).
func TestHandleCheckWatches_LiveProbe(t *testing.T) {
	if os.Getenv("TRVL_TEST_LIVE_PROBES") != "1" {
		t.Skip("hits live flight APIs; set TRVL_TEST_LIVE_PROBES=1 to run")
	}
	t.Setenv("HOME", t.TempDir())

	store, err := watch.DefaultStore()
	if err != nil {
		t.Fatalf("DefaultStore: %v", err)
	}
	if _, err := store.Add(watch.Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "LHR",
		DepartDate:  "2026-09-15",
		Currency:    "EUR",
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	_, structured, err := handleCheckWatches(context.Background(), nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("handleCheckWatches: %v", err)
	}
	raw, _ := json.Marshal(structured)
	if strings.Contains(string(raw), `"current_price":0`) {
		t.Errorf("live probe returned current_price 0 — re-check did not produce a real price: %s", string(raw))
	}
}
