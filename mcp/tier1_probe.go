package mcp

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/MikkoParkkola/trvl/internal/cfprobe"
	"github.com/MikkoParkkola/trvl/internal/hacks"
	"github.com/MikkoParkkola/trvl/internal/probecache"
	"github.com/MikkoParkkola/trvl/internal/tier1"
	"github.com/MikkoParkkola/trvl/internal/watch"
)

// installTier1Probe wires the MIK-6234 Tier-1 scheduler-amortized probe onto the
// watch scheduler. Each tick it runs at most one budget-gated counterfactual
// probe for one active flight watch (rotating), caching the result for later
// call-free reads by `trvl flights`. Gated by TRVL_TIER1_PROBE at the call site.
func installTier1Probe(sched *watch.Scheduler, dir string) {
	cache := probecache.NewStore(dir)
	_ = cache.Load()
	var tick int64
	sched.SetProbeHook(func(ctx context.Context, active []watch.Watch) {
		n := int(atomic.AddInt64(&tick, 1) - 1)
		tier1.ProbeOne(ctx, active, n, cfprobe.Default(), cache, time.Now(), hacks.DetectAll)
	})
}
