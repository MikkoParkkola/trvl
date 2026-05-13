package watch

import (
	"os"
	"testing"
	"time"
)

// --- Save ---

func TestSave_WritesAndLoads(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	w := Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-01",
		BelowPrice:  200,
		Currency:    "EUR",
	}
	id, err := store.Add(w)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Save explicitly.
	if err := store.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Reload from disk.
	store2 := NewStore(dir)
	if err := store2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	watches := store2.List()
	if len(watches) != 1 {
		t.Errorf("expected 1 watch after Save, got %d", len(watches))
	}
	if watches[0].ID != id {
		t.Errorf("ID mismatch: got %q, want %q", watches[0].ID, id)
	}
}

// makePricePoints creates PricePoint history from float64 values.
func makePricePoints(prices []float64) []PricePoint {
	pts := make([]PricePoint, len(prices))
	for i, p := range prices {
		pts[i] = PricePoint{Price: p}
	}
	return pts
}

// --- Sparkline ---

func TestSparkline_Empty(t *testing.T) {
	got := Sparkline(nil, 20)
	if got != "" {
		t.Errorf("expected empty sparkline for nil prices, got %q", got)
	}
}

func TestSparkline_SinglePoint(t *testing.T) {
	got := Sparkline(makePricePoints([]float64{100.0}), 20)
	// Fewer than 2 points returns "".
	if got != "" {
		t.Errorf("expected empty sparkline for single price, got %q", got)
	}
}

func TestSparkline_MultiplePoints(t *testing.T) {
	got := Sparkline(makePricePoints([]float64{100.0, 150.0, 80.0, 200.0, 120.0}), 20)
	if len(got) == 0 {
		t.Error("expected non-empty sparkline for multiple prices")
	}
}

func TestSparkline_AllSamePrice(t *testing.T) {
	got := Sparkline(makePricePoints([]float64{100.0, 100.0, 100.0}), 20)
	// Should not panic; all same value is valid (flat line).
	if len(got) == 0 {
		t.Error("expected non-empty sparkline for uniform prices")
	}
}

func TestSparkline_TruncatesToMaxPoints(t *testing.T) {
	pts := makePricePoints([]float64{100, 110, 120, 130, 140, 150, 160, 170, 180, 190, 200})
	got := Sparkline(pts, 5)
	// Result should be 5 runes (5 maxPoints of the tail).
	if len([]rune(got)) != 5 {
		t.Errorf("expected 5 chars for maxPoints=5, got %d", len([]rune(got)))
	}
}

// --- TrendArrow ---

func TestTrendArrow_Rising(t *testing.T) {
	pts := makePricePoints([]float64{100, 130}) // last > prev → ↑
	got := TrendArrow(pts)
	if got != "↑" {
		t.Errorf("expected ↑ for rising prices, got %q", got)
	}
}

func TestTrendArrow_Falling(t *testing.T) {
	pts := makePricePoints([]float64{200, 140}) // last < prev → ↓
	got := TrendArrow(pts)
	if got != "↓" {
		t.Errorf("expected ↓ for falling prices, got %q", got)
	}
}

func TestTrendArrow_TooFewPoints(t *testing.T) {
	got := TrendArrow(nil)
	if got != "" {
		t.Errorf("expected empty for no data, got %q", got)
	}
}

func TestTrendArrow_SinglePoint(t *testing.T) {
	got := TrendArrow(makePricePoints([]float64{100}))
	if got != "" {
		t.Errorf("expected empty for single point, got %q", got)
	}
}

func TestTrendArrow_Equal(t *testing.T) {
	pts := makePricePoints([]float64{100, 100})
	got := TrendArrow(pts)
	// Equal prices → "→"
	if got == "↑" || got == "↓" {
		t.Errorf("expected neutral for equal prices, got %q", got)
	}
}

// --- validateWatchDate ---

func TestValidateWatchDate_Valid(t *testing.T) {
	err := validateWatchDate("depart_date", "2026-07-01")
	if err != nil {
		t.Errorf("unexpected error for valid date: %v", err)
	}
}

func TestValidateWatchDate_Empty(t *testing.T) {
	err := validateWatchDate("depart_date", "")
	if err != nil {
		t.Errorf("expected nil for empty date, got %v", err)
	}
}

func TestValidateWatchDate_InvalidFormat(t *testing.T) {
	err := validateWatchDate("depart_date", "01/07/2026")
	if err == nil {
		t.Error("expected error for invalid date format")
	}
}

// --- saveJSON / loadJSON ---

func TestWatchSaveLoadJSON_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/watches.json"

	watches := []Watch{
		{
			ID:          "w001",
			Type:        "flight",
			Origin:      "HEL",
			Destination: "BCN",
			BelowPrice:  200,
			Currency:    "EUR",
			CreatedAt:   time.Now(),
		},
	}

	if err := saveJSON(path, watches); err != nil {
		t.Fatalf("saveJSON: %v", err)
	}

	var loaded []Watch
	if err := loadJSON(path, &loaded); err != nil {
		t.Fatalf("loadJSON: %v", err)
	}
	if len(loaded) != 1 || loaded[0].ID != "w001" {
		t.Errorf("round-trip failed: got %+v", loaded)
	}
}

func TestWatchLoadJSON_MissingFile(t *testing.T) {
	var dst []Watch
	err := loadJSON("/nonexistent/watches.json", &dst)
	if err != nil {
		t.Errorf("expected nil for missing file, got %v", err)
	}
}

func TestWatchLoadJSON_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/empty.json"
	f, _ := os.Create(path)
	_ = f.Close()

	var dst []Watch
	err := loadJSON(path, &dst)
	if err != nil {
		t.Errorf("expected nil for empty file, got %v", err)
	}
}

// --- MatchRoomKeywords ---

func TestMatchRoomKeywords_AllMatch(t *testing.T) {
	got := MatchRoomKeywords([]string{"double", "sea view"}, "Double Room Sea View", "Great balcony")
	if !got {
		t.Error("expected true when all keywords match")
	}
}

func TestMatchRoomKeywords_PartialMiss(t *testing.T) {
	got := MatchRoomKeywords([]string{"double", "jacuzzi"}, "Double Room Sea View", "")
	if got {
		t.Error("expected false when keyword missing")
	}
}

func TestMatchRoomKeywords_Empty(t *testing.T) {
	got := MatchRoomKeywords(nil, "any room", "description")
	if got {
		t.Error("expected false for nil keywords")
	}
}

func TestMatchRoomKeywords_CaseInsensitive(t *testing.T) {
	got := MatchRoomKeywords([]string{"DOUBLE"}, "double room", "")
	if !got {
		t.Error("expected case-insensitive match")
	}
}

// --- Watch.IsRouteWatch / IsDateRange / IsRoomWatch ---

func TestWatchIsRouteWatch(t *testing.T) {
	w := Watch{Type: "flight", Origin: "HEL", Destination: "BCN"}
	if !w.IsRouteWatch() {
		t.Error("expected IsRouteWatch=true")
	}
	w.DepartDate = "2026-07-01"
	if w.IsRouteWatch() {
		t.Error("expected IsRouteWatch=false when DepartDate set")
	}
}

func TestWatchIsDateRange(t *testing.T) {
	w := Watch{DepartFrom: "2026-07-01", DepartTo: "2026-07-31"}
	if !w.IsDateRange() {
		t.Error("expected IsDateRange=true")
	}
	w.DepartFrom = ""
	if w.IsDateRange() {
		t.Error("expected IsDateRange=false when DepartFrom empty")
	}
}

func TestWatchIsRoomWatch(t *testing.T) {
	w := Watch{Type: "room"}
	if !w.IsRoomWatch() {
		t.Error("expected IsRoomWatch=true for room type")
	}
	w.Type = "flight"
	if w.IsRoomWatch() {
		t.Error("expected IsRoomWatch=false for flight type")
	}
}
