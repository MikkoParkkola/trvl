package watch

import (
	"strconv"
	"testing"
)

// TRVL.RETENTION.6 -- a dry run must preview what the real migration will do.
//
// MigrateDryRun built its shadow store without copying the retention config, so
// retentionOrDefault fell back to the COMPILED defaults. With the cap lowered to
// 20, the preview reported nothing to compact -- and the real migration then
// deleted the history the preview had promised to keep.
//
// That is the exact failure a dry run exists to prevent, in the command whose
// whole purpose is "show me before you touch it". A preview that under-reports
// is worse than no preview, because it is believed.
//
// Found by adversarial second-opinion review, 2026-08-06. The same review named
// this alongside the gate/prune mismatch; only the first half was fixed on the
// first pass, so this test exists as much to pin the second half as to prove it.
func TestMigrateDryRunUsesTheConfiguredCap(t *testing.T) {
	const capacity = 20
	const points = 200

	t.Setenv(EnvMaxPointsPerWatch, strconv.Itoa(capacity))
	if points <= capacity || points >= maxObservationsPerWatch {
		t.Fatalf("fixture no longer straddles the two limits: %d must be > %d and < %d",
			points, capacity, maxObservationsPerWatch)
	}

	s := NewStore(t.TempDir())
	if err := s.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	id, _, err := s.Add(Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "EUR"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	for i := 0; i < points; i++ {
		s.history = append(s.history, PricePoint{WatchID: id, Price: float64(100 + i%3), Currency: "EUR"})
	}
	if err := s.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	dry, err := s.MigrateDryRun()
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if dry.HistoryCompacted == 0 {
		t.Fatalf("dry run reported nothing to compact for %d points against a configured cap of %d. "+
			"The real migration will delete %d of them, so the preview promised to keep history it "+
			"is about to lose.", points, capacity, points-capacity)
	}

	// And the preview must match what actually happens. A dry run that reports
	// the right direction but the wrong amount is still a preview nobody can act
	// on.
	real, err := s.Migrate()
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if dry.HistoryCompacted != real.HistoryCompacted {
		t.Errorf("dry run previewed %d points compacted, real migration compacted %d",
			dry.HistoryCompacted, real.HistoryCompacted)
	}
	if got := len(s.History(id)); got != capacity {
		t.Errorf("after migration the watch holds %d points, want the configured cap %d", got, capacity)
	}
}
