package dealquality

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// makeSamples creates n synthetic samples for route "HEL-BCN", kind "flight", season "Q2".
// Prices are evenly distributed from minPrice to maxPrice.
func makeSamples(n int, minPrice, maxPrice float64) []Sample {
	samples := make([]Sample, n)
	if n == 1 {
		samples[0] = Sample{Route: "HEL-BCN", Season: "Q2", Date: "2026-05-01", Price: minPrice, Kind: "flight"}
		return samples
	}
	for i := 0; i < n; i++ {
		price := minPrice + float64(i)*(maxPrice-minPrice)/float64(n-1)
		samples[i] = Sample{
			Route:  "HEL-BCN",
			Season: "Q2",
			Date:   "2026-05-01",
			Price:  price,
			Kind:   "flight",
		}
	}
	return samples
}

func TestSeasonOf(t *testing.T) {
	tests := []struct {
		date string
		want string
	}{
		{"2026-01-15", "Q1"},
		{"2026-03-31", "Q1"},
		{"2026-04-01", "Q2"},
		{"2026-06-30", "Q2"},
		{"2026-07-01", "Q3"},
		{"2026-09-30", "Q3"},
		{"2026-10-01", "Q4"},
		{"2026-12-31", "Q4"},
		{"bad-date", "Q1"}, // fallback
	}
	for _, tc := range tests {
		got := SeasonOf(tc.date)
		if got != tc.want {
			t.Errorf("SeasonOf(%q) = %q, want %q", tc.date, got, tc.want)
		}
	}
}

func TestRouteKey(t *testing.T) {
	if RouteKey("BCN", "HEL") != "BCN-HEL" {
		t.Error("expected BCN-HEL (sorted)")
	}
	if RouteKey("HEL", "BCN") != "BCN-HEL" {
		t.Error("expected BCN-HEL (sorted)")
	}
	if RouteKey("hel", "bcn") != "BCN-HEL" {
		t.Error("expected uppercase")
	}
}

func TestSparse(t *testing.T) {
	for n := 0; n < 10; n++ {
		samples := makeSamples(n, 100, 500)
		s := ScoreAgainst(300, samples)
		if s.Total != 50 {
			t.Errorf("n=%d: expected Total=50 for sparse, got %d", n, s.Total)
		}
		if s.Reason != "insufficient_history" {
			t.Errorf("n=%d: expected reason=insufficient_history, got %s", n, s.Reason)
		}
		if s.Samples != n {
			t.Errorf("n=%d: expected Samples=%d, got %d", n, n, s.Samples)
		}
	}
}

func TestDensePercentiles(t *testing.T) {
	// 100 samples evenly distributed 100..1099
	samples := makeSamples(100, 100, 1099)
	s := ScoreAgainst(200, samples) // price well below median → high score
	if s.Total < 60 {
		t.Errorf("expected high score for low price, got %d", s.Total)
	}
	if s.P10 <= 0 || s.P20 <= 0 || s.P50 <= 0 {
		t.Errorf("expected non-zero percentiles: p10=%f p20=%f p50=%f", s.P10, s.P20, s.P50)
	}
}

func TestP10Band(t *testing.T) {
	// 20 samples: prices 100..119
	samples := makeSamples(20, 100, 119)
	// p10 ≈ 101.9, p20 ≈ 103.8, p50 ≈ 109.5
	// Price at exactly p10 → score near 95-100
	s := ScoreAgainst(100, samples)
	if s.Total < 95 {
		t.Errorf("price at p10 band: expected >=95, got %d", s.Total)
	}
}

func TestP20Band(t *testing.T) {
	samples := makeSamples(20, 100, 200)
	// For 20 samples [100..200]: p10 ≈ 110, p20 ≈ 120, p50 ≈ 150
	// Use 115 which is between p10 and p20
	s := ScoreAgainst(115, samples)
	if s.Total < 80 || s.Total > 95 {
		t.Errorf("price between p10-p20: expected 80-95, got %d", s.Total)
	}
}

func TestP50Band(t *testing.T) {
	samples := makeSamples(20, 100, 200)
	// p50 ≈ 150
	s := ScoreAgainst(135, samples) // between p20 and p50
	if s.Total < 50 || s.Total > 80 {
		t.Errorf("price between p20-p50: expected 50-80, got %d", s.Total)
	}
}

func TestAboveMedian(t *testing.T) {
	samples := makeSamples(20, 100, 200)
	// p50 ≈ 150
	s := ScoreAgainst(175, samples) // above p50 but below 2×p50=300
	if s.Total >= 50 || s.Total < 0 {
		t.Errorf("above median: expected 0-49, got %d", s.Total)
	}
	if s.Reason != "above_median" {
		t.Errorf("expected above_median reason, got %s", s.Reason)
	}
}

func TestOutlierAbove2xP50(t *testing.T) {
	samples := makeSamples(20, 100, 200) // p50 ≈ 150
	s := ScoreAgainst(400, samples)      // well above 2×150=300
	if s.Total != 0 {
		t.Errorf("outlier above 2×p50: expected 0, got %d", s.Total)
	}
}

func TestConcurrentAppends(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	var wg sync.WaitGroup
	errors := make(chan error, 50)

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := store.Append(Sample{
				Route:  "HEL-BCN",
				Season: "Q2",
				Date:   fmt.Sprintf("2026-05-%02d", (i%28)+1),
				Price:  float64(100 + i),
				Kind:   "flight",
			})
			if err != nil {
				errors <- err
			}
		}(i)
	}
	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("concurrent append error: %v", err)
	}

	samples := store.Query("HEL-BCN", "flight", "Q2")
	if len(samples) == 0 {
		t.Error("expected samples after concurrent appends")
	}
}

func TestDedup(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	sample := Sample{Route: "HEL-BCN", Season: "Q2", Date: "2026-05-01", Price: 150, Kind: "flight"}
	_ = store.Append(sample)
	_ = store.Append(sample) // duplicate
	_ = store.Append(sample) // duplicate

	samples := store.Query("HEL-BCN", "flight", "Q2")
	if len(samples) != 1 {
		t.Errorf("expected 1 sample after dedup, got %d", len(samples))
	}
}

func TestPrune(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	// Add an old sample (100 days ago) directly to the store's slice then save.
	oldDate := time.Now().AddDate(0, 0, -100).Format(historyLayout)
	recentDate := time.Now().AddDate(0, 0, -5).Format(historyLayout)

	store.mu.Lock()
	store.samples = []Sample{
		{Route: "HEL-BCN", Season: "Q2", Date: oldDate, Price: 100, Kind: "flight"},
		{Route: "HEL-BCN", Season: "Q2", Date: recentDate, Price: 200, Kind: "flight"},
	}
	_ = store.saveLocked()
	store.mu.Unlock()

	// Appending any new sample triggers prune.
	newSeason := SeasonOf(recentDate)
	_ = store.Append(Sample{Route: "HEL-BCN", Season: newSeason, Date: recentDate, Price: 300, Kind: "flight"})

	all := store.Query("HEL-BCN", "flight", SeasonOf(recentDate))
	for _, s := range all {
		if s.Date == oldDate {
			t.Error("old sample should have been pruned")
		}
	}
}

func TestQueryFilters(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	_ = store.Append(Sample{Route: "HEL-BCN", Season: "Q2", Date: "2026-05-01", Price: 150, Kind: "flight"})
	_ = store.Append(Sample{Route: "HEL-BCN", Season: "Q2", Date: "2026-05-02", Price: 200, Kind: "hotel"})
	_ = store.Append(Sample{Route: "HEL-PRG", Season: "Q2", Date: "2026-05-01", Price: 100, Kind: "flight"})

	flights := store.Query("HEL-BCN", "flight", "Q2")
	if len(flights) != 1 {
		t.Errorf("expected 1 flight sample, got %d", len(flights))
	}
}

func TestStoreDefaultPath(t *testing.T) {
	// Just verify DefaultStore() doesn't crash and returns a non-nil store.
	home, _ := os.UserHomeDir()
	expectedDir := filepath.Join(home, ".trvl")
	store, err := DefaultStore()
	if err != nil {
		t.Fatalf("DefaultStore() error: %v", err)
	}
	if store.dir != expectedDir {
		t.Errorf("expected dir %s, got %s", expectedDir, store.dir)
	}
}

func TestStoreLoad(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	// Load on empty dir should not error.
	if err := store.Load(); err != nil {
		t.Fatalf("Load() on empty dir error: %v", err)
	}

	// Append a sample, then create a new store and load it.
	_ = store.Append(Sample{Route: "HEL-BCN", Season: "Q2", Date: "2026-05-01", Price: 150, Kind: "flight"})

	store2 := NewStore(dir)
	if err := store2.Load(); err != nil {
		t.Fatalf("Load() after write error: %v", err)
	}
	samples := store2.Query("HEL-BCN", "flight", "Q2")
	if len(samples) != 1 {
		t.Errorf("expected 1 sample after load, got %d", len(samples))
	}
}

func TestPercentileEdgeCases(t *testing.T) {
	// Test percentile with single-element slice and edge p values.
	single := []float64{42.0}
	if v := percentile(single, 0); v != 42.0 {
		t.Errorf("percentile(single,0) = %f, want 42", v)
	}
	if v := percentile(single, 100); v != 42.0 {
		t.Errorf("percentile(single,100) = %f, want 42", v)
	}
	if v := percentile(single, 50); v != 42.0 {
		t.Errorf("percentile(single,50) = %f, want 42", v)
	}

	// Test percentile on empty slice.
	empty := []float64{}
	if v := percentile(empty, 50); v != 0 {
		t.Errorf("percentile(empty,50) = %f, want 0", v)
	}
}

func TestLerpBoundaries(t *testing.T) {
	// When high == low, returns maxScore.
	score := lerp(100, 50, 95, 100, 100)
	if score != 95 {
		t.Errorf("lerp with high==low: expected 95, got %d", score)
	}
	// price < low → maxScore
	score = lerp(200, 50, 95, 50, 100)
	if score != 95 {
		t.Errorf("lerp price<low: expected 95, got %d", score)
	}
}
