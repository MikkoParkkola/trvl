package watch

import (
	"fmt"
	"os"
	"sort"
	"testing"
	"time"
)

// Design spike for trvl#555/#575. Measures the steady-state transactional
// point append at the configured retention boundary. The operation updates one
// watch row, appends one history row and evicts one row; it must not encode or
// rewrite the complete retained corpus.
//
// Not a regression test. It reports and never fails on timing, because a timing
// assertion here would be exactly the load-dependent flake this repo spent #533
// and #540 removing.
//
// Run: go test ./internal/watch/ -run TestSpike555 -v
func TestSpike555PublishCost(t *testing.T) {
	if testing.Short() {
		t.Skip("spike, not a gate")
	}

	const points = maxWatchObservations
	dir := t.TempDir()
	s := NewStore(dir)

	// One thousand points per watch reaches both the per-series and global caps.
	s.watches = make([]Watch, 0, points/maxObservationsPerWatch)
	for i := 0; i < points/maxObservationsPerWatch; i++ {
		s.watches = append(s.watches, Watch{
			ID:          fmt.Sprintf("w%03d", i),
			Type:        "flight",
			Origin:      "HEL",
			Destination: fmt.Sprintf("D%02d", i),
			Currency:    "EUR",
			BelowPrice:  float64(100 + i),
		})
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.history = make([]PricePoint, 0, points)
	for i := 0; i < points; i++ {
		s.history = append(s.history, PricePoint{
			WatchID:   fmt.Sprintf("w%03d", i/(maxObservationsPerWatch)),
			Price:     float64(80 + i%400),
			Currency:  "EUR",
			Timestamp: base.Add(time.Duration(i) * time.Minute),
		})
	}

	const runs = 12
	if err := s.Save(); err != nil {
		t.Fatalf("seed database: %v", err)
	}
	appendCost := make([]time.Duration, 0, runs)

	for i := 0; i < runs; i++ {
		start := time.Now()
		if err := s.RecordPrice("w000", 90+float64(i), "EUR"); err != nil {
			t.Fatalf("transactional append: %v", err)
		}
		appendCost = append(appendCost, time.Since(start))
	}

	dbInfo, _ := os.Stat(s.databasePath())

	t.Logf("history points   : %d", points)
	t.Logf("watch.db          : %.1f MB", mb(dbInfo))
	t.Logf("append   p50=%v p95=%v", pct(appendCost, 50), pct(appendCost, 95))
	t.Logf("VERDICT: fail-fast budget is 10ms p95 for the transactional append")
}

func mb(fi os.FileInfo) float64 {
	if fi == nil {
		return 0
	}
	return float64(fi.Size()) / (1 << 20)
}

func pct(d []time.Duration, p int) time.Duration {
	if len(d) == 0 {
		return 0
	}
	s := append([]time.Duration(nil), d...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	i := (p * len(s)) / 100
	if i >= len(s) {
		i = len(s) - 1
	}
	return s[i]
}
