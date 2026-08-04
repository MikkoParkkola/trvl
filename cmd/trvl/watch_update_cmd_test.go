package main

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/pricealert"
	"github.com/MikkoParkkola/trvl/internal/watch"
)

// loadWatches re-reads committed state from disk through a fresh store, so an
// assertion cannot pass on an in-memory struct the test itself mutated.
func loadWatches(t *testing.T) []watch.Watch {
	t.Helper()
	store, err := watch.DefaultStore()
	if err != nil {
		t.Fatalf("DefaultStore: %v", err)
	}
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	return store.List()
}

func runWatchCmd(t *testing.T, cmd interface {
	SetArgs([]string)
	Execute() error
}, args ...string) {
	t.Helper()
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("command %v: %v", args, err)
	}
}

// TRVL.WATCH.UNSET.1
func TestWatchUpdateCmd_ClearWebhookPersists(t *testing.T) {
	setTestHome(t, t.TempDir())

	runWatchCmd(t, watchAddCmd(), "HEL", "BCN", "--depart", "2026-07-01",
		"--below", "200", "--webhook", "https://example.test/hook")

	before := loadWatches(t)
	if len(before) != 1 || before[0].WebhookURL != "https://example.test/hook" {
		t.Fatalf("setup: %+v", before)
	}

	runWatchCmd(t, watchUpdateCmd(), before[0].ID, "--clear-webhook")

	after := loadWatches(t)
	if len(after) != 1 {
		t.Fatalf("stored watches = %d, want 1 (update must not delete the watch)", len(after))
	}
	if after[0].WebhookURL != "" {
		t.Fatalf("WebhookURL = %q, want empty after --clear-webhook", after[0].WebhookURL)
	}
}

// TRVL.WATCH.UNSET.2
func TestWatchUpdateCmd_ClearAlertThresholdsAndLastMinute(t *testing.T) {
	setTestHome(t, t.TempDir())

	runWatchCmd(t, watchAddCmd(), "Prague", "--type", "hotel",
		"--depart", "2026-07-01", "--return", "2026-07-02",
		"--alert-drop", "20", "--alert-drop-abs", "50",
		"--last-minute", "--last-minute-drop", "30")

	before := loadWatches(t)
	if len(before) != 1 || before[0].AlertDropPct != 20 || !before[0].LastMinuteMode {
		t.Fatalf("setup: %+v", before)
	}

	runWatchCmd(t, watchUpdateCmd(), before[0].ID, "--clear-alert-drop", "--clear-last-minute")

	after := loadWatches(t)[0]
	if after.AlertDropPct != 0 {
		t.Fatalf("AlertDropPct = %v, want 0", after.AlertDropPct)
	}
	if after.AlertDropAbs != 0 {
		t.Fatalf("AlertDropAbs = %v, want 0", after.AlertDropAbs)
	}
	if after.LastMinuteMode {
		t.Fatal("LastMinuteMode = true, want false")
	}
	if after.LastMinuteDropPct != 0 {
		t.Fatalf("LastMinuteDropPct = %v, want 0", after.LastMinuteDropPct)
	}
	// Clearing removes the *configured* threshold; it does not disable proactive
	// alerting. With both limbs zero pricealert substitutes DefaultDropPercent, so
	// a fall of exactly that default still fires. Asserted through Evaluate rather
	// than stated in a comment, because it is the surprising half of the contract.
	base := 100.0
	drop := base * (1 - pricealert.DefaultDropPercent/100)
	_, _, fired := pricealert.Evaluate(
		pricealert.State{Baseline: base}, drop,
		pricealert.Threshold{DropPercent: after.AlertDropPct, DropAbsolute: after.AlertDropAbs},
	)
	if !fired {
		t.Fatalf("cleared thresholds silenced alerting; want the %v%% default still in force", pricealert.DefaultDropPercent)
	}
}

// TRVL.WATCH.UNSET.2 — the update surface also sets, not only clears.
func TestWatchUpdateCmd_SetWebhookAndAlertDrop(t *testing.T) {
	setTestHome(t, t.TempDir())

	runWatchCmd(t, watchAddCmd(), "HEL", "BCN", "--depart", "2026-07-01", "--below", "200")
	id := loadWatches(t)[0].ID

	runWatchCmd(t, watchUpdateCmd(), id, "--webhook", "https://example.test/new", "--alert-drop", "15")

	after := loadWatches(t)[0]
	if after.WebhookURL != "https://example.test/new" {
		t.Fatalf("WebhookURL = %q", after.WebhookURL)
	}
	if after.AlertDropPct != 15 {
		t.Fatalf("AlertDropPct = %v, want 15", after.AlertDropPct)
	}
}

// TRVL.WATCH.UNSET.3 — a plain re-watch omitting the notification flags must
// leave the stored settings untouched (protection introduced by #509).
func TestWatchAddCmd_RepeatOmittingNotificationFieldsLeavesThemUntouched(t *testing.T) {
	setTestHome(t, t.TempDir())

	runWatchCmd(t, watchAddCmd(), "HEL", "BCN", "--depart", "2026-07-01",
		"--below", "200", "--webhook", "https://example.test/hook", "--alert-drop", "20")

	runWatchCmd(t, watchAddCmd(), "HEL", "BCN", "--depart", "2026-07-01", "--below", "200")

	after := loadWatches(t)
	if len(after) != 1 {
		t.Fatalf("stored watches = %d, want 1", len(after))
	}
	if after[0].WebhookURL != "https://example.test/hook" {
		t.Fatalf("WebhookURL = %q, want the original webhook preserved", after[0].WebhookURL)
	}
	if after[0].AlertDropPct != 20 {
		t.Fatalf("AlertDropPct = %v, want 20 preserved", after[0].AlertDropPct)
	}
}

// TRVL.WATCH.UNSET.4
func TestWatchUpdateCmd_ClearPreservesHistoryAndCreatedAt(t *testing.T) {
	setTestHome(t, t.TempDir())

	runWatchCmd(t, watchAddCmd(), "HEL", "BCN", "--depart", "2026-07-01",
		"--below", "200", "--webhook", "https://example.test/hook")

	store, err := watch.DefaultStore()
	if err != nil {
		t.Fatalf("DefaultStore: %v", err)
	}
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	id := store.List()[0].ID
	if err := store.RecordPrice(id, 180, "EUR"); err != nil {
		t.Fatalf("RecordPrice: %v", err)
	}
	// RecordPrice only appends history; LowestPrice is accrued by the checker, so
	// seed it explicitly or the preservation assertion would compare 0 to 0.
	if _, err := store.Mutate(id, func(w *watch.Watch) { w.LowestPrice = 180 }); err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	before := loadWatches(t)[0]
	if before.LowestPrice == 0 || before.CreatedAt.IsZero() {
		t.Fatalf("setup: LowestPrice=%v CreatedAt=%v, both must be non-zero", before.LowestPrice, before.CreatedAt)
	}

	runWatchCmd(t, watchUpdateCmd(), id, "--clear-webhook")

	after := loadWatches(t)[0]
	if !after.CreatedAt.Equal(before.CreatedAt) {
		t.Fatalf("CreatedAt = %v, want %v", after.CreatedAt, before.CreatedAt)
	}
	if after.LowestPrice != before.LowestPrice {
		t.Fatalf("LowestPrice = %v, want %v", after.LowestPrice, before.LowestPrice)
	}

	fresh, err := watch.DefaultStore()
	if err != nil {
		t.Fatalf("DefaultStore: %v", err)
	}
	if err := fresh.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := len(fresh.History(id)); got != len(store.History(id)) {
		t.Fatalf("history points = %d, want %d", got, len(store.History(id)))
	}
	if len(fresh.History(id)) == 0 {
		t.Fatal("history is empty; the test would pass vacuously")
	}
}

func TestWatchUpdateCmd_RejectsSetAndClearOfSameField(t *testing.T) {
	setTestHome(t, t.TempDir())

	runWatchCmd(t, watchAddCmd(), "HEL", "BCN", "--depart", "2026-07-01", "--below", "200")
	id := loadWatches(t)[0].ID

	cmd := watchUpdateCmd()
	cmd.SetArgs([]string{id, "--webhook", "https://example.test/x", "--clear-webhook"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected an error when a field is both set and cleared")
	}
}

func TestWatchUpdateCmd_UnknownIDErrors(t *testing.T) {
	setTestHome(t, t.TempDir())

	cmd := watchUpdateCmd()
	cmd.SetArgs([]string{"nope", "--clear-webhook"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected an error for an unknown watch ID")
	}
}
