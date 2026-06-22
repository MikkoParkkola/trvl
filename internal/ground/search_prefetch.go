package ground

import (
	"context"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/cache"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/searchctx"
)

// --- Negative-result cache + speculative prefetch (innovation #4) ---------

// groundNegCacheTTL bounds how long a clean "no ground service" for a route+month
// suppresses re-running the full provider fan-out for that route.
const groundNegCacheTTL = 30 * time.Minute

// groundNegCache records routes for which the whole provider fan-out returned no
// routes CLEANLY (every provider that ran was not-applicable or definitively
// empty — no hard errors, timeouts, rate-limits). That is a genuine "nothing
// runs here", safe to cache briefly. A run with any error is never cached, so
// transient failures always retry. On by default; set TRVL_NEGCACHE=0 to disable.
var groundNegCache = cache.NewNegativeCache(groundNegCacheTTL)

func negCacheEnabled() bool {
	return groundEnvBool("TRVL_NEGCACHE", true)
}

// groundPrefetchEnabled gates speculative prefetch. Off by default: prefetch adds
// provider load (extra background searches), so it is opt-in via TRVL_PREFETCH=1.
func groundPrefetchEnabled() bool {
	return groundEnvBool("TRVL_PREFETCH", false)
}

func groundEnvBool(name string, def bool) bool {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return def
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return def
	}
	return v
}

// groundNegKey returns the negative-cache key for a search and whether the search
// is eligible for negative caching. Only unfiltered searches qualify: a MaxPrice
// or Type filter can make a served route look empty, which is NOT a "no service"
// fact and must never be negative-cached. The provider set is folded into the key
// so restricting providers cannot poison the all-providers result.
func groundNegKey(from, to, date string, opts SearchOptions, providerKey string) (string, bool) {
	if opts.MaxPrice > 0 || opts.Type != "" {
		return "", false
	}
	return cache.NegativeKey("ground|"+providerKey, from, to, date), true
}

// groundPrefetchCtxKey marks a context as a prefetch (or nested) search so
// speculative prefetch does not recurse.
type groundPrefetchCtxKey struct{}

func groundPrefetchDisabled(ctx context.Context) bool {
	return ctx.Value(groundPrefetchCtxKey{}) != nil
}

// groundPrefetchTarget is one likely-next search to warm.
type groundPrefetchTarget struct {
	from string
	to   string
	date string
}

// groundPrefetchTargets computes the likely-next searches to warm: the return
// leg (reverse route, +7 days) plus the two adjacent days. Pure and
// deterministic; an unparseable date yields no targets.
func groundPrefetchTargets(from, to, date string) []groundPrefetchTarget {
	t, err := models.ParseDate(date)
	if err != nil {
		return nil
	}
	plus1 := t.AddDate(0, 0, 1).Format("2006-01-02")
	minus1 := t.AddDate(0, 0, -1).Format("2006-01-02")
	ret := t.AddDate(0, 0, 7).Format("2006-01-02")
	return []groundPrefetchTarget{
		{from: to, to: from, date: ret}, // return leg
		{from: from, to: to, date: plus1},
		{from: from, to: to, date: minus1},
	}
}

// groundPrefetchConcurrency bounds simultaneous prefetch searches.
const groundPrefetchConcurrency = 2

// groundPrefetchFn warms a single target. Seam for tests; the default calls the
// normal search with prefetch disabled to prevent recursion.
var groundPrefetchFn func(ctx context.Context, t groundPrefetchTarget, opts SearchOptions)

func init() {
	groundPrefetchFn = func(ctx context.Context, t groundPrefetchTarget, opts SearchOptions) {
		_, _ = SearchByName(ctx, t.from, t.to, t.date, opts)
	}
}

// maybePrefetchGround dispatches best-effort speculative prefetch. It returns
// immediately; a prefetch failure (panic included) can never affect the primary
// search.
func maybePrefetchGround(ctx context.Context, from, to, date string, opts SearchOptions) {
	if !groundPrefetchEnabled() || groundPrefetchDisabled(ctx) {
		return
	}
	targets := groundPrefetchTargets(from, to, date)
	if len(targets) == 0 {
		return
	}
	go runGroundPrefetch(ctx, targets, opts)
}

// runGroundPrefetch warms targets with bounded concurrency. Each warm is
// isolated; a panic in one never affects its peers nor the caller.
func runGroundPrefetch(parent context.Context, targets []groundPrefetchTarget, opts SearchOptions) {
	detached, cancel := searchctx.DetachedWithin(parent, sharedGroundSearchTimeout)
	defer cancel()
	prefetchCtx := context.WithValue(detached, groundPrefetchCtxKey{}, struct{}{})

	sem := make(chan struct{}, groundPrefetchConcurrency)
	var wg sync.WaitGroup
	for _, t := range targets {
		wg.Add(1)
		sem <- struct{}{}
		go func(t groundPrefetchTarget) {
			defer wg.Done()
			defer func() { <-sem }()
			defer func() { _ = recover() }()
			groundPrefetchFn(prefetchCtx, t, opts)
		}(t)
	}
	wg.Wait()
}
