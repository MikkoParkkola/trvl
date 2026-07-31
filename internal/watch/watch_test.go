package watch

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestStoreAddListRemove(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	// Start empty.
	if got := store.List(); len(got) != 0 {
		t.Fatalf("expected empty list, got %d", len(got))
	}

	// Add a watch.
	w := Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-01",
		ReturnDate:  "2026-07-08",
		BelowPrice:  200,
		Currency:    "EUR",
	}
	id, _, err := store.Add(w)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty ID")
	}

	// List should have one entry.
	watches := store.List()
	if len(watches) != 1 {
		t.Fatalf("expected 1 watch, got %d", len(watches))
	}
	if watches[0].ID != id {
		t.Errorf("ID = %q, want %q", watches[0].ID, id)
	}
	if watches[0].Origin != "HEL" {
		t.Errorf("Origin = %q, want HEL", watches[0].Origin)
	}
	if watches[0].CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero")
	}

	// Get by ID.
	got, ok := store.Get(id)
	if !ok {
		t.Fatal("Get: not found")
	}
	if got.Destination != "BCN" {
		t.Errorf("Destination = %q, want BCN", got.Destination)
	}

	// Get nonexistent.
	_, ok = store.Get("nonexistent")
	if ok {
		t.Error("Get: should not find nonexistent ID")
	}

	// Remove.
	found, err := store.Remove(id)
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !found {
		t.Error("Remove: should return true for existing watch")
	}
	if got := store.List(); len(got) != 0 {
		t.Fatalf("expected empty list after remove, got %d", len(got))
	}

	// Remove nonexistent.
	found, err = store.Remove("nonexistent")
	if err != nil {
		t.Fatalf("Remove nonexistent: %v", err)
	}
	if found {
		t.Error("Remove: should return false for nonexistent")
	}
}

func TestStorePersistence(t *testing.T) {
	dir := t.TempDir()

	// Create and save.
	store1 := NewStore(dir)
	_, _, err := store1.Add(Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "TYO",
		DepartDate:  "2026-08-01",
		Currency:    "EUR",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Load in a new store instance.
	store2 := NewStore(dir)
	if err := store2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	watches := store2.List()
	if len(watches) != 1 {
		t.Fatalf("expected 1 watch after reload, got %d", len(watches))
	}
	if watches[0].Origin != "HEL" {
		t.Errorf("Origin = %q after reload, want HEL", watches[0].Origin)
	}
}

func TestStorePersistenceUsesPrivateAtomicFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "store")
	store := NewStore(dir)

	id, _, err := store.Add(Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-01",
		Currency:    "EUR",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := store.RecordPrice(id, 199, "EUR"); err != nil {
		t.Fatalf("RecordPrice: %v", err)
	}

	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if runtime.GOOS != "windows" {
		if got := dirInfo.Mode().Perm(); got != 0o700 {
			t.Fatalf("dir perms = %o, want 700", got)
		}
	}

	for _, name := range []string{"watches.json", "price-history.json"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if runtime.GOOS != "windows" {
			if got := info.Mode().Perm(); got != 0o600 {
				t.Fatalf("%s perms = %o, want 600", name, got)
			}
		}
	}

	if leftovers, err := filepath.Glob(filepath.Join(dir, "*.tmp-*")); err != nil {
		t.Fatalf("glob temp files: %v", err)
	} else if len(leftovers) != 0 {
		t.Fatalf("unexpected temp files left behind: %v", leftovers)
	}
}

func TestStoreLoadEmpty(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	// Load from empty dir should not error.
	if err := store.Load(); err != nil {
		t.Fatalf("Load empty: %v", err)
	}
	if got := store.List(); len(got) != 0 {
		t.Fatalf("expected empty, got %d", len(got))
	}
}

func TestStoreAddRejectsMalformedDates(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	_, _, err := store.Add(Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "not-a-date",
		Currency:    "EUR",
	})
	if err == nil {
		t.Fatal("expected malformed depart date error")
	}
	if got := err.Error(); got != "depart date must use YYYY-MM-DD" {
		t.Fatalf("unexpected error: %q", got)
	}
	if got := store.List(); len(got) != 0 {
		t.Fatalf("expected invalid watch to be rejected, got %d stored watches", len(got))
	}
}

func TestStoreAddRejectsPartialDateRange(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	_, _, err := store.Add(Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "BCN",
		DepartFrom:  "2026-07-01",
		Currency:    "EUR",
	})
	if err == nil {
		t.Fatal("expected partial date range error")
	}
	if got := err.Error(); got != "date range requires both start and end dates" {
		t.Fatalf("unexpected error: %q", got)
	}
}

func TestStoreAddRejectsInvertedDateRange(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	_, _, err := store.Add(Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "BCN",
		DepartFrom:  "2026-07-10",
		DepartTo:    "2026-07-01",
		Currency:    "EUR",
	})
	if err == nil {
		t.Fatal("expected inverted date range error")
	}
	if got := err.Error(); got != "date range start must be on or before end" {
		t.Fatalf("unexpected error: %q", got)
	}
}

func TestPriceHistory(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	id, _, err := store.Add(Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-01",
		Currency:    "EUR",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Record prices.
	if err := store.RecordPrice(id, 250, "EUR"); err != nil {
		t.Fatalf("RecordPrice 1: %v", err)
	}
	if err := store.RecordPrice(id, 220, "EUR"); err != nil {
		t.Fatalf("RecordPrice 2: %v", err)
	}
	if err := store.RecordPrice(id, 195, "EUR"); err != nil {
		t.Fatalf("RecordPrice 3: %v", err)
	}

	// Check history.
	history := store.History(id)
	if len(history) != 3 {
		t.Fatalf("expected 3 history points, got %d", len(history))
	}
	if history[0].Price != 250 {
		t.Errorf("history[0].Price = %.0f, want 250", history[0].Price)
	}
	if history[2].Price != 195 {
		t.Errorf("history[2].Price = %.0f, want 195", history[2].Price)
	}

	// History for nonexistent watch.
	other := store.History("nonexistent")
	if len(other) != 0 {
		t.Errorf("expected empty history for nonexistent, got %d", len(other))
	}
}

func TestUpdateWatch(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	id, _, err := store.Add(Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-01",
		Currency:    "EUR",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	w, _ := store.Get(id)
	w.LastPrice = 199
	w.LowestPrice = 199
	if err := store.UpdateWatch(w); err != nil {
		t.Fatalf("UpdateWatch: %v", err)
	}

	got, _ := store.Get(id)
	if got.LastPrice != 199 {
		t.Errorf("LastPrice = %.0f, want 199", got.LastPrice)
	}

	// Update nonexistent.
	err = store.UpdateWatch(Watch{ID: "nonexistent"})
	if err == nil {
		t.Error("UpdateWatch nonexistent: expected error")
	}
}

func TestJSONRoundTrip(t *testing.T) {
	w := Watch{
		ID:          "abc12345",
		Type:        "flight",
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-01",
		ReturnDate:  "2026-07-08",
		BelowPrice:  200,
		Currency:    "EUR",
		LastPrice:   250,
		LowestPrice: 195,
	}

	data, err := json.Marshal(w)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got Watch
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.ID != w.ID {
		t.Errorf("ID = %q, want %q", got.ID, w.ID)
	}
	if got.BelowPrice != w.BelowPrice {
		t.Errorf("BelowPrice = %.0f, want %.0f", got.BelowPrice, w.BelowPrice)
	}
	if got.ReturnDate != w.ReturnDate {
		t.Errorf("ReturnDate = %q, want %q", got.ReturnDate, w.ReturnDate)
	}
}

func TestJSONFileFormat(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	_, _, err := store.Add(Watch{
		Type:        "hotel",
		Origin:      "Helsinki",
		Destination: "Barcelona",
		DepartDate:  "2026-07-01",
		ReturnDate:  "2026-07-08",
		BelowPrice:  150,
		Currency:    "USD",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Read raw file and verify it's valid JSON.
	data, err := os.ReadFile(filepath.Join(dir, "watches.json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("file is not valid JSON array: %v", err)
	}
	if len(raw) != 1 {
		t.Errorf("expected 1 entry in file, got %d", len(raw))
	}
}

// mockChecker is a test double for PriceChecker.
type mockChecker struct {
	price    float64
	currency string
	err      error
}

func (m *mockChecker) CheckPrice(_ context.Context, _ Watch) (float64, string, string, error) {
	return m.price, m.currency, "", m.err
}

func TestCheckAllThreshold(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	_, _, err := store.Add(Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-01",
		BelowPrice:  200,
		Currency:    "EUR",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	checker := &mockChecker{price: 180, currency: "EUR"}
	results := CheckAll(context.Background(), store, checker)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	r := results[0]
	if r.Error != nil {
		t.Fatalf("unexpected error: %v", r.Error)
	}
	if !r.BelowGoal {
		t.Error("expected BelowGoal = true for price 180 < goal 200")
	}
	if r.NewPrice != 180 {
		t.Errorf("NewPrice = %.0f, want 180", r.NewPrice)
	}
}

func TestCheckAllPriceDrop(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	id, _, err := store.Add(Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-01",
		BelowPrice:  100,
		Currency:    "EUR",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Set a previous price.
	w, _ := store.Get(id)
	w.LastPrice = 300
	if err := store.UpdateWatch(w); err != nil {
		t.Fatalf("UpdateWatch: %v", err)
	}

	checker := &mockChecker{price: 250, currency: "EUR"}
	results := CheckAll(context.Background(), store, checker)

	r := results[0]
	if r.Error != nil {
		t.Fatalf("unexpected error: %v", r.Error)
	}
	if r.PriceDrop != -50 {
		t.Errorf("PriceDrop = %.0f, want -50", r.PriceDrop)
	}
	if r.BelowGoal {
		t.Error("BelowGoal should be false for price 250 > goal 100")
	}

	// Verify lowest price was updated.
	updated, _ := store.Get(id)
	if updated.LowestPrice != 250 {
		t.Errorf("LowestPrice = %.0f, want 250", updated.LowestPrice)
	}
}

func TestCheckAllError(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	_, _, err := store.Add(Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-01",
		Currency:    "EUR",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	checker := &mockChecker{err: context.DeadlineExceeded}
	results := CheckAll(context.Background(), store, checker)

	if results[0].Error == nil {
		t.Error("expected error result")
	}
}

func TestCheckAllZeroPrice(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	_, _, err := store.Add(Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-01",
		BelowPrice:  200,
		Currency:    "EUR",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Zero price = no results found.
	checker := &mockChecker{price: 0, currency: ""}
	results := CheckAll(context.Background(), store, checker)

	r := results[0]
	if r.Error != nil {
		t.Fatalf("unexpected error: %v", r.Error)
	}
	if r.BelowGoal {
		t.Error("BelowGoal should be false for zero price")
	}
}

func TestSparklineEmpty(t *testing.T) {
	if got := Sparkline(nil, 10); got != "" {
		t.Errorf("nil history: got %q, want empty", got)
	}
	if got := Sparkline([]PricePoint{{Price: 100}}, 10); got != "" {
		t.Errorf("single point: got %q, want empty", got)
	}
}

func TestSparklineBasic(t *testing.T) {
	history := []PricePoint{
		{Price: 100},
		{Price: 200},
		{Price: 300},
		{Price: 200},
		{Price: 100},
	}
	got := Sparkline(history, 10)
	if len([]rune(got)) != 5 {
		t.Errorf("expected 5 chars, got %d: %q", len([]rune(got)), got)
	}
	runes := []rune(got)
	// First and last should be lowest bar, middle should be highest.
	if runes[0] != '▁' {
		t.Errorf("first char = %c, want ▁", runes[0])
	}
	if runes[2] != '█' {
		t.Errorf("middle char = %c, want █", runes[2])
	}
	if runes[4] != '▁' {
		t.Errorf("last char = %c, want ▁", runes[4])
	}
}

func TestSparklineTruncates(t *testing.T) {
	// 20 points, maxPoints=5 — should only render the last 5.
	history := make([]PricePoint, 20)
	for i := range history {
		history[i] = PricePoint{Price: float64(100 + i)}
	}
	got := Sparkline(history, 5)
	if len([]rune(got)) != 5 {
		t.Errorf("expected 5 chars, got %d: %q", len([]rune(got)), got)
	}
}

func TestSparklineFlatLine(t *testing.T) {
	history := []PricePoint{
		{Price: 200},
		{Price: 200},
		{Price: 200},
	}
	got := Sparkline(history, 10)
	runes := []rune(got)
	// All bars should be the same (middle bar).
	for i, r := range runes {
		if r != runes[0] {
			t.Errorf("flat line: char[%d] = %c, want %c", i, r, runes[0])
		}
	}
}

func TestTrendArrowDown(t *testing.T) {
	history := []PricePoint{{Price: 300}, {Price: 200}}
	if got := TrendArrow(history); got != "↓" {
		t.Errorf("price drop: got %q, want ↓", got)
	}
}

func TestTrendArrowUp(t *testing.T) {
	history := []PricePoint{{Price: 200}, {Price: 300}}
	if got := TrendArrow(history); got != "↑" {
		t.Errorf("price increase: got %q, want ↑", got)
	}
}

func TestTrendArrowFlat(t *testing.T) {
	history := []PricePoint{{Price: 200}, {Price: 200}}
	if got := TrendArrow(history); got != "→" {
		t.Errorf("flat: got %q, want →", got)
	}
}

func TestTrendArrowEmpty(t *testing.T) {
	if got := TrendArrow(nil); got != "" {
		t.Errorf("nil: got %q, want empty", got)
	}
	if got := TrendArrow([]PricePoint{{Price: 100}}); got != "" {
		t.Errorf("single: got %q, want empty", got)
	}
}

func TestHistoryPersistence(t *testing.T) {
	dir := t.TempDir()

	// Store 1: add watch and record price.
	store1 := NewStore(dir)
	id, _, err := store1.Add(Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-01",
		Currency:    "EUR",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := store1.RecordPrice(id, 250, "EUR"); err != nil {
		t.Fatalf("RecordPrice: %v", err)
	}

	// Store 2: reload and verify history survived.
	store2 := NewStore(dir)
	if err := store2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	history := store2.History(id)
	if len(history) != 1 {
		t.Fatalf("expected 1 history point after reload, got %d", len(history))
	}
	if history[0].Price != 250 {
		t.Errorf("Price = %.0f, want 250", history[0].Price)
	}
}
