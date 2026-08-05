package watch

import (
	"strings"
	"testing"
)

// TRVL.RETENTION.3 -- the store can report what it holds against its limits.
//
// The three retention numbers shipped with no usage data behind them, and this
// ticket is explicitly about making them visible rather than changing them:
// "revisit only once that data exists" needs somewhere for the data to come
// from.
//
// So the assertions below are about whether an operator could ACT on the
// output, not about formatting.
func TestRetentionStatsDescribesTheDistributionNotJustTheTotal(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	// Deliberately lopsided: one heavy watch and several light ones. A total
	// alone would look healthy here, which is the case the distribution exists
	// to expose.
	heavy, _, err := s.Add(Watch{Type: "flight", Origin: "HEL", Destination: "NRT", Currency: "EUR"})
	if err != nil {
		t.Fatalf("add heavy: %v", err)
	}
	for i := 0; i < 40; i++ {
		if err := s.RecordPrice(heavy, float64(100+i), "EUR"); err != nil {
			t.Fatalf("record heavy %d: %v", i, err)
		}
	}
	for j := 0; j < 3; j++ {
		id, _, err := s.Add(Watch{Type: "flight", Origin: "HEL", Destination: string(rune('A' + j)), Currency: "EUR"})
		if err != nil {
			t.Fatalf("add light %d: %v", j, err)
		}
		if err := s.RecordPrice(id, 200, "EUR"); err != nil {
			t.Fatalf("record light %d: %v", j, err)
		}
	}

	st := s.RetentionStats()

	if st.Watches != 4 {
		t.Errorf("Watches = %d, want 4", st.Watches)
	}
	if st.WatchPoints == 0 {
		t.Fatal("no watch-keyed points counted; the report would describe an empty store")
	}
	if st.MaxPerWatch <= st.MedianPerWatch {
		t.Errorf("max (%d) is not above median (%d) on a deliberately lopsided store -- the "+
			"distribution is not being measured, and a total alone hides exactly this shape",
			st.MaxPerWatch, st.MedianPerWatch)
	}
	if st.StoreBytes <= 0 {
		t.Error("StoreBytes = 0; the on-disk size is half of what makes a retention limit judgeable")
	}
}

// The summary must name the limits AND how each was set, so an operator reading
// it knows whether a value is a default or something they configured.
func TestRetentionStatsSummaryNamesTheLimitsAndTheirSource(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	got := s.RetentionStats().Summary()

	for _, want := range []string{
		"per-watch cap", "global cap", "route watch TTL",
		EnvMaxPointsPerWatch, EnvMaxPointsTotal, EnvRouteTTLDays,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("summary omits %q, so a reader cannot tell what the limit is or how to "+
				"change it:\n%s", want, got)
		}
	}
}

// A configured value must be reported as configured. Showing a default when the
// operator set something is the reporting equivalent of silently clamping.
func TestRetentionStatsSummaryDistinguishesConfiguredFromDefault(t *testing.T) {
	t.Setenv(EnvMaxPointsTotal, "9000")

	s := NewStore(t.TempDir())
	if err := s.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	got := s.RetentionStats().Summary()

	if !strings.Contains(got, "9000") {
		t.Errorf("summary does not show the configured global cap:\n%s", got)
	}
	if !strings.Contains(got, "set via "+EnvMaxPointsTotal) {
		t.Errorf("summary does not mark the global cap as configured rather than default; an "+
			"operator cannot tell their setting took effect:\n%s", got)
	}
}
