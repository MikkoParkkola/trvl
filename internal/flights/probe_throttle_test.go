package flights

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/testutil"
)

// probeThrottle remembers that Google throttled this probe run. Once one call
// is refused, the client sits in a ~15 minute cooldown and every further call
// would fail with "cooling down", so later subtests skip immediately instead of
// adding more requests from an already-throttled runner IP (trvl#720).
type probeThrottle struct{ hit atomic.Bool }

// skipIfThrottled skips t when an earlier call in this run was throttled.
func (p *probeThrottle) skipIfThrottled(t *testing.T) {
	t.Helper()
	if p.hit.Load() {
		t.Skip("skipping: Google throttled an earlier call in this probe run (not a regression)")
	}
}

// check records and skips on transient errors; a nil or real error falls
// through to the caller's own assertions.
func (p *probeThrottle) check(t *testing.T, err error) {
	t.Helper()
	if testutil.IsTransientProbeErr(err) {
		p.hit.Store(true)
		testutil.SkipIfTransient(t, err)
	}
}

// probeBaseline runs the production-path sanity search with one jittered retry
// on throttling, skipping the test when Google is still throttling and failing
// it on a real error or an empty result.
func (p *probeThrottle) probeBaseline(t *testing.T, client *batchexec.Client, origin, dest, date string, opts SearchOptions) int {
	t.Helper()
	p.skipIfThrottled(t)
	var count int
	err := testutil.RetryTransient(t, func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		res, err := SearchFlightsWithClient(ctx, client, origin, dest, date, opts)
		if err == nil {
			count = res.Count
		}
		return err
	})
	p.check(t, err)
	if err != nil {
		t.Fatalf("baseline search %s->%s failed: %v", origin, dest, err)
	}
	if count == 0 {
		t.Fatalf("baseline %s->%s returned 0 flights -- route/date unusable", origin, dest)
	}
	t.Logf("Baseline %s->%s (SearchFlightsWithClient): %d flights", origin, dest, count)
	return count
}
