package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

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
	watchHome := t.TempDir()
	t.Setenv("HOME", watchHome)
	t.Setenv("USERPROFILE", watchHome) // os.UserHomeDir() reads USERPROFILE on Windows

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
	if !strings.Contains(got, `"below_goal":true`) {
		t.Errorf("expected below_goal:true, got %s", got)
	}
}

// storedWatch re-opens the on-disk watch store (the same path handleWatchPrice
// writes to, under the test HOME) and returns the single watch it finds. It
// proves the handler persisted what we expect rather than only echoing the
// response struct.
func storedWatch(t *testing.T) watch.Watch {
	t.Helper()
	store, err := watch.DefaultStore()
	if err != nil {
		t.Fatalf("DefaultStore: %v", err)
	}
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := store.List()
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 stored watch, got %d", len(got))
	}
	return got[0]
}

// TestHandleWatchPrice_WebhookPersists proves the webhook arg is wired into the
// stored watch, mirroring the CLI --webhook flag.
func TestHandleWatchPrice_WebhookPersists(t *testing.T) {
	watchHome := t.TempDir()
	t.Setenv("HOME", watchHome)
	t.Setenv("USERPROFILE", watchHome) // os.UserHomeDir() reads USERPROFILE on Windows

	const hookURL = "https://hooks.invalid/notify"
	_, _, err := handleWatchPrice(context.Background(), map[string]any{
		"type":         "flight",
		"origin":       "HEL",
		"destination":  "BCN",
		"date":         "2026-09-01",
		"target_price": 200.0,
		"webhook":      hookURL,
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("handleWatchPrice: %v", err)
	}

	w := storedWatch(t)
	if w.WebhookURL != hookURL {
		t.Errorf("expected webhook to persist, got %q", w.WebhookURL)
	}
}

// TestHandleWatchPrice_FlightDateRange proves a flight watch with a
// depart_from/depart_to window (and no single date) is accepted and stored as a
// date-range watch, mirroring the CLI --from/--to mode.
func TestHandleWatchPrice_FlightDateRange(t *testing.T) {
	watchHome := t.TempDir()
	t.Setenv("HOME", watchHome)
	t.Setenv("USERPROFILE", watchHome) // os.UserHomeDir() reads USERPROFILE on Windows

	_, _, err := handleWatchPrice(context.Background(), map[string]any{
		"type":         "flight",
		"origin":       "HEL",
		"destination":  "PRG",
		"depart_from":  "2026-07-01",
		"depart_to":    "2026-08-31",
		"target_price": 100.0,
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("handleWatchPrice (date range): %v", err)
	}

	w := storedWatch(t)
	if !w.IsDateRange() {
		t.Errorf("expected a date-range watch, got DepartFrom=%q DepartTo=%q DepartDate=%q",
			w.DepartFrom, w.DepartTo, w.DepartDate)
	}
	if w.DepartFrom != "2026-07-01" || w.DepartTo != "2026-08-31" {
		t.Errorf("date range not persisted: from=%q to=%q", w.DepartFrom, w.DepartTo)
	}
	if w.DepartDate != "" {
		t.Errorf("date-range watch must not set a single DepartDate, got %q", w.DepartDate)
	}
}

// TestHandleWatchPrice_FlightNoDateErrors proves a flight watch with neither a
// single date nor a depart_from/depart_to range is rejected.
func TestHandleWatchPrice_FlightNoDateErrors(t *testing.T) {
	watchHome := t.TempDir()
	t.Setenv("HOME", watchHome)
	t.Setenv("USERPROFILE", watchHome) // os.UserHomeDir() reads USERPROFILE on Windows

	_, _, err := handleWatchPrice(context.Background(), map[string]any{
		"type":         "flight",
		"origin":       "HEL",
		"destination":  "BCN",
		"target_price": 200.0,
	}, nil, nil, nil)
	if err == nil {
		t.Fatal("expected an error when a flight watch has neither date nor date range")
	}
}

// TestHandleWatchPrice_AlertDropPersists proves alert_drop / alert_drop_abs
// persist and that target_price may be omitted when a proactive drop alert is
// set, mirroring the CLI which allows --alert-drop without --below.
func TestHandleWatchPrice_AlertDropPersists(t *testing.T) {
	watchHome := t.TempDir()
	t.Setenv("HOME", watchHome)
	t.Setenv("USERPROFILE", watchHome) // os.UserHomeDir() reads USERPROFILE on Windows

	_, _, err := handleWatchPrice(context.Background(), map[string]any{
		"type":           "flight",
		"origin":         "HEL",
		"destination":    "NRT",
		"date":           "2026-09-01",
		"alert_drop":     12.0,
		"alert_drop_abs": 50.0,
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("handleWatchPrice (alert drop, no target_price): %v", err)
	}

	w := storedWatch(t)
	if w.AlertDropPct != 12.0 {
		t.Errorf("expected AlertDropPct=12, got %v", w.AlertDropPct)
	}
	if w.AlertDropAbs != 50.0 {
		t.Errorf("expected AlertDropAbs=50, got %v", w.AlertDropAbs)
	}
	if w.BelowPrice != 0 {
		t.Errorf("expected no fixed threshold, got BelowPrice=%v", w.BelowPrice)
	}
}

// TestHandleWatchPrice_NoThresholdErrors proves the handler rejects a watch with
// no target_price and no proactive drop alert (nothing to fire on).
func TestHandleWatchPrice_NoThresholdErrors(t *testing.T) {
	watchHome := t.TempDir()
	t.Setenv("HOME", watchHome)
	t.Setenv("USERPROFILE", watchHome) // os.UserHomeDir() reads USERPROFILE on Windows

	_, _, err := handleWatchPrice(context.Background(), map[string]any{
		"type":        "flight",
		"origin":      "HEL",
		"destination": "BCN",
		"date":        "2026-09-01",
	}, nil, nil, nil)
	if err == nil {
		t.Fatal("expected an error when neither target_price nor alert_drop is set")
	}
}

// TestHandleWatchPrice_ThresholdsAndRepeats proves the MCP surface inherits the
// #509 store semantics: two thresholds on one route are two watches, an
// identical repeat is neither a second watch nor a new creation timestamp.
func TestHandleWatchPrice_ThresholdsAndRepeats(t *testing.T) {
	watchHome := t.TempDir()
	t.Setenv("HOME", watchHome)
	t.Setenv("USERPROFILE", watchHome)

	call := func(target float64) map[string]any {
		t.Helper()
		_, structured, err := handleWatchPrice(context.Background(), map[string]any{
			"type":         "flight",
			"origin":       "AMS",
			"destination":  "VLC",
			"depart_date":  "2027-03-01",
			"target_price": target,
			"currency":     "EUR",
		}, nil, nil, nil)
		if err != nil {
			t.Fatalf("handleWatchPrice(%v): %v", target, err)
		}
		raw, err := json.Marshal(structured)
		if err != nil {
			t.Fatalf("marshal response: %v", err)
		}
		var out map[string]any
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		return out
	}

	first := call(200)
	second := call(120)
	if first["watch_id"] == second["watch_id"] {
		t.Fatalf("MULTIPRICE.1: distinct thresholds returned the same watch_id %v", first["watch_id"])
	}

	// created_at is RFC3339, i.e. second resolution: cross a second boundary so
	// a regression that re-stamps "now" is actually visible in the comparison
	// below rather than hidden by two calls landing in the same second.
	time.Sleep(1100 * time.Millisecond)
	repeat := call(200)
	if repeat["watch_id"] != first["watch_id"] {
		t.Fatalf("MULTIPRICE.4: repeat returned watch_id %v, want existing %v", repeat["watch_id"], first["watch_id"])
	}
	if repeat["created_at"] != first["created_at"] {
		t.Fatalf("MULTIPRICE.4: repeat claimed a new creation time %v, want %v", repeat["created_at"], first["created_at"])
	}

	store, err := watch.DefaultStore()
	if err != nil {
		t.Fatalf("DefaultStore: %v", err)
	}
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := len(store.List()); got != 2 {
		t.Fatalf("MULTIPRICE.4: store holds %d watches after 3 calls, want 2", got)
	}
}
