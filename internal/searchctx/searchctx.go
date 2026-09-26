// Package searchctx keeps request values after the caller's cancel signal stops.
package searchctx

import (
	"context"
	"time"
)

// DetachedWithin preserves values from ctx while detaching cancellation fan-out.
// The returned context is bounded only by max so one short-lived caller cannot
// shrink a shared singleflight execution for every joined caller.
func DetachedWithin(ctx context.Context, max time.Duration) (context.Context, context.CancelFunc) {
	detached := context.WithoutCancel(ctx)
	return context.WithTimeout(detached, max)
}
