package watch

import (
	"strconv"
	"testing"
)

// TRVL.RETENTION.5 -- migration must compact to the CONFIGURED cap, not the
// compiled default.
//
// compactHistoryLocked decided whether to compact by comparing against the
// compiled maxObservationsPerWatch, then compacted down to the configured limit.
// An operator who LOWERED the cap therefore got the gate of the default and the
// pruning of their setting: with the cap at 20, a watch holding 500 points asked
// "500 > 1000?", answered no, and kept all 500. The setting was accepted,
// validated, and reported by `retention stats` -- and silently not applied on the
// one path whose job is bringing an old store into line with policy.
//
// Only lowering was affected, and only on migration, which is why nothing caught
// it. Found by adversarial second-opinion review of trvl#585, 2026-08-06.
func TestMigrateCompactsToTheConfiguredCapNotTheDefault(t *testing.T) {
	const capacity = 20
	// Comfortably above the configured cap and far below the compiled default, so
	// the old gate provably declines to act and the new one provably acts.
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

	// Seeded directly rather than through RecordPrice: RecordPrice prunes on every
	// call, so it could never build the over-cap history that migration exists to
	// clean up. The store under test is one written by an older build, or by this
	// build before the cap was lowered.
	for i := 0; i < points; i++ {
		s.history = append(s.history, PricePoint{WatchID: id, Price: float64(100 + i%3), Currency: "EUR"})
	}
	if err := s.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	if _, err := s.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	if got := len(s.History(id)); got != capacity {
		t.Errorf("migration left %d points for a watch whose configured cap is %d.\n"+
			"The operator lowered the cap, migration compared the count against the compiled "+
			"default (%d) instead, and their setting was silently not applied.",
			got, capacity, maxObservationsPerWatch)
	}
}
