package flights

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/testutil"
)

// TestWizzairProbe hits Wizz Air's live timetable endpoint for a CEE route that
// Wizz actually flies (BUD -> BCN). Opt-in via TRVL_TEST_LIVE_PROBES=1.
//
// The assertion is honest about the version-rotation failure mode documented in
// GH #115: Wizz either returns results, or — if the {version} path segment has
// rotated since the last-known-good — surfaces the typed ErrWizzVersionRotated
// sentinel that the aggregate renders as an actionable status. Both are PASS;
// the test only fails on an unexpected error class (which would mean a new,
// undiagnosed failure mode worth investigating).
func TestWizzairProbe(t *testing.T) {
	testutil.RequireLiveProbe(t)

	date := time.Now().AddDate(0, 0, 45).Format("2006-01-02")
	t.Logf("searching BUD -> BCN on %s (one-way, economy) via Wizz Air", date)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	out, err := SearchWizzair(ctx, "BUD", "BCN", date, "EUR", SearchOptions{Adults: 1})
	if err != nil {
		if errors.Is(err, ErrWizzVersionRotated) {
			st := wizzairFailureStatus(err)
			t.Logf("Wizz API version rotated (expected, actionable): code=%s hint=%q",
				st.FixHintCode, st.FixHint)
			if st.FixHintCode != "WIZZ_VERSION_ROTATED" {
				t.Fatalf("rotation should carry WIZZ_VERSION_ROTATED, got %q", st.FixHintCode)
			}
			return // actionable status is an acceptable live outcome
		}
		t.Fatalf("unexpected Wizz error class: %v", err)
	}
	t.Logf("Wizz returned %d result(s)", len(out))
	for _, f := range out {
		if f.Provider != "wizzair" {
			t.Errorf("provider = %q, want wizzair", f.Provider)
		}
	}
}
