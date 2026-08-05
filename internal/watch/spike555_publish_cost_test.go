package watch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// Design spike for trvl#555. Measures what a COMBINED publish costs against the
// current split publish, on a store the size of the observed worst case
// (320,028 history points / ~39MB), so the fail-fast in that ticket can be
// answered with a number instead of an intuition:
//
//	"If a design spike shows a combined snapshot costs more than ~10ms at p95 on
//	a realistic store, stop and reconsider -- the current split write is at least
//	fast, and a slow store write on the scheduler path would be a worse
//	regression than the gap it closes."
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

	const points = 320028
	dir := t.TempDir()
	s := NewStore(dir)

	// One watch per route so history keys are realistic, and a history slice at
	// the observed worst case.
	s.watches = make([]Watch, 0, 40)
	for i := 0; i < 40; i++ {
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
			RouteKey:  fmt.Sprintf("flight:HEL:D%02d:", i%40),
			Price:     float64(80 + i%400),
			Currency:  "EUR",
			Timestamp: base.Add(time.Duration(i) * time.Minute),
		})
	}

	const runs = 12
	split := make([]time.Duration, 0, runs)
	combined := make([]time.Duration, 0, runs)

	for i := 0; i < runs; i++ {
		start := time.Now()
		if err := s.persistLocked(); err != nil {
			t.Fatalf("split publish: %v", err)
		}
		split = append(split, time.Since(start))

		// The combined shape under evaluation: ONE document, ONE atomic publish.
		// Same encoder and same atomic write the split path uses, so the delta is
		// the shape rather than the machinery.
		combo := struct {
			Watches []Watch      `json:"watches"`
			History []PricePoint `json:"history"`
		}{Watches: s.watches, History: s.history}

		start = time.Now()
		if err := saveJSON(filepath.Join(dir, "store.json"), combo); err != nil {
			t.Fatalf("combined publish: %v", err)
		}
		combined = append(combined, time.Since(start))
	}

	wi, _ := os.Stat(filepath.Join(dir, "watches.json"))
	hi, _ := os.Stat(filepath.Join(dir, "price-history.json"))
	ci, _ := os.Stat(filepath.Join(dir, "store.json"))

	blob, _ := json.Marshal(struct {
		W []Watch      `json:"watches"`
		H []PricePoint `json:"history"`
	}{s.watches, s.history})

	t.Logf("history points   : %d", points)
	t.Logf("watches.json      : %.1f MB", mb(wi))
	t.Logf("price-history.json: %.1f MB", mb(hi))
	t.Logf("combined store.json: %.1f MB (marshalled %.1f MB)", mb(ci), float64(len(blob))/(1<<20))
	t.Logf("split    p50=%v p95=%v", pct(split, 50), pct(split, 95))
	t.Logf("combined p50=%v p95=%v", pct(combined, 50), pct(combined, 95))
	t.Logf("VERDICT: fail-fast budget is 10ms p95 for the combined publish")
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
