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
		// All three are honest, typed live conditions — not undiagnosed
		// failures — so they are acceptable probe outcomes. In particular
		// ErrWizzBlocked is the expected result from a datacenter/CI egress IP
		// that Wizz's CloudFront edge treats as non-human (MIK-6167): the probe
		// reports the actionable status rather than red-flagging a transport bug.
		switch {
		case errors.Is(err, ErrWizzVersionRotated):
			st := wizzairFailureStatus(err)
			t.Logf("Wizz API version rotated (expected, actionable): code=%s hint=%q",
				st.FixHintCode, st.FixHint)
			if st.FixHintCode != "WIZZ_VERSION_ROTATED" {
				t.Fatalf("rotation should carry WIZZ_VERSION_ROTATED, got %q", st.FixHintCode)
			}
			return
		case errors.Is(err, ErrWizzBlocked):
			st := wizzairFailureStatus(err)
			t.Logf("Wizz edge-blocked this IP (expected from datacenter/CI; honest typed status): code=%s hint=%q err=%v",
				st.FixHintCode, st.FixHint, err)
			if st.FixHintCode != "WIZZ_BLOCKED" {
				t.Fatalf("block should carry WIZZ_BLOCKED, got %q", st.FixHintCode)
			}
			return
		case errors.Is(err, ErrWizzRejected):
			st := wizzairFailureStatus(err)
			t.Logf("Wizz declined the route (validationCodes; honest typed status): code=%s hint=%q err=%v",
				st.FixHintCode, st.FixHint, err)
			if st.FixHintCode != "WIZZ_MARKET_REJECTED" {
				t.Fatalf("rejection should carry WIZZ_MARKET_REJECTED, got %q", st.FixHintCode)
			}
			return
		default:
			t.Fatalf("unexpected Wizz error class: %v", err)
		}
	}
	t.Logf("Wizz returned %d result(s)", len(out))
	for _, f := range out {
		if f.Provider != "wizzair" {
			t.Errorf("provider = %q, want wizzair", f.Provider)
		}
	}
}
