package flights

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/cache"
	"github.com/MikkoParkkola/trvl/internal/models"
)

func TestFlightNegCache_StoresAndServesCleanNoResult(t *testing.T) {
	t.Setenv("TRVL_NEGCACHE", "1")

	key := "ryanair|HEL|AGP|2026-07"
	flightNegCache.Mark(key) // simulate a prior clean no-result
	t.Cleanup(func() { flightNegCache = cache.NewNegativeCache(flightNegCacheTTL) })

	var ran atomic.Int32
	run := withFlightNegCache("ryanair", "HEL", "AGP", "2026-07-15", func(ctx context.Context) providerOutcome {
		ran.Add(1)
		return providerOutcome{succeeded: true, flights: []models.FlightResult{{Provider: "ryanair"}}}
	})

	out := run(context.Background())
	if ran.Load() != 0 {
		t.Fatal("provider should NOT be queried when route is negatively cached")
	}
	if out.status.Status != models.StatusCheckedNoHit {
		t.Errorf("served status = %q, want %q", out.status.Status, models.StatusCheckedNoHit)
	}
	if !out.succeeded || len(out.flights) != 0 {
		t.Error("served outcome should be a clean empty (succeeded, 0 flights)")
	}
}

func TestFlightNegCache_MarksOnlyCleanNoResult(t *testing.T) {
	t.Setenv("TRVL_NEGCACHE", "1")

	tests := []struct {
		name     string
		outcome  providerOutcome
		wantMark bool
	}{
		{"clean no result", providerOutcome{succeeded: true, flights: nil}, true},
		{"has results", providerOutcome{succeeded: true, flights: []models.FlightResult{{}}}, false},
		{"error", providerOutcome{succeeded: false, err: context.DeadlineExceeded}, false},
		{"failed empty", providerOutcome{succeeded: false}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flightNegCache = cache.NewNegativeCache(flightNegCacheTTL)
			key := cache.NegativeKey("wizzair", "HEL", "TLL", "2026-09-01")
			run := withFlightNegCache("wizzair", "HEL", "TLL", "2026-09-01", func(ctx context.Context) providerOutcome {
				return tt.outcome
			})
			run(context.Background())
			if got := flightNegCache.Seen(key); got != tt.wantMark {
				t.Errorf("Seen() = %v, want %v", got, tt.wantMark)
			}
		})
	}
}

func TestFlightNegCache_Disabled(t *testing.T) {
	t.Setenv("TRVL_NEGCACHE", "0")
	flightNegCache = cache.NewNegativeCache(flightNegCacheTTL)
	t.Cleanup(func() { flightNegCache = cache.NewNegativeCache(flightNegCacheTTL) })

	var ran atomic.Int32
	flightNegCache.Mark("ryanair|HEL|AGP|2026-07")
	run := withFlightNegCache("ryanair", "HEL", "AGP", "2026-07-15", func(ctx context.Context) providerOutcome {
		ran.Add(1)
		return providerOutcome{succeeded: true}
	})
	run(context.Background())
	if ran.Load() != 1 {
		t.Fatal("provider MUST run when negative cache is disabled")
	}
}

func TestFlightPrefetchTargets(t *testing.T) {
	targets := flightPrefetchTargets("HEL", "AGP", "2026-07-15")
	if len(targets) != 3 {
		t.Fatalf("got %d targets, want 3", len(targets))
	}
	// Return leg: reversed route, +7 days.
	if targets[0].origin != "AGP" || targets[0].destination != "HEL" || targets[0].date != "2026-07-22" {
		t.Errorf("return-leg target wrong: %+v", targets[0])
	}
	// Adjacent days, same route.
	if targets[1].date != "2026-07-16" || targets[2].date != "2026-07-14" {
		t.Errorf("adjacent-day targets wrong: %+v %+v", targets[1], targets[2])
	}

	if got := flightPrefetchTargets("HEL", "AGP", "not-a-date"); got != nil {
		t.Errorf("unparseable date should yield no targets, got %v", got)
	}
}

func TestRunFlightPrefetch_BestEffortAndBounded(t *testing.T) {
	orig := flightPrefetchFn
	t.Cleanup(func() { flightPrefetchFn = orig })

	var calls atomic.Int32
	var inFlight, maxInFlight atomic.Int32
	flightPrefetchFn = func(ctx context.Context, client *batchexec.Client, target flightPrefetchTarget, opts SearchOptions) {
		n := inFlight.Add(1)
		for {
			old := maxInFlight.Load()
			if n <= old || maxInFlight.CompareAndSwap(old, n) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		calls.Add(1)
		inFlight.Add(-1)
		panic("prefetch boom") // must be swallowed
	}

	targets := flightPrefetchTargets("HEL", "AGP", "2026-07-15")
	// Must return cleanly despite every warm panicking.
	runFlightPrefetch(context.Background(), nil, targets, SearchOptions{})

	if calls.Load() != 3 {
		t.Errorf("ran %d warms, want 3", calls.Load())
	}
	if maxInFlight.Load() > int32(flightPrefetchConcurrency) {
		t.Errorf("max concurrency %d exceeded cap %d", maxInFlight.Load(), flightPrefetchConcurrency)
	}
}

func TestMaybePrefetchFlights_GatedOffByDefault(t *testing.T) {
	orig := flightPrefetchFn
	t.Cleanup(func() { flightPrefetchFn = orig })

	var calls atomic.Int32
	var wg sync.WaitGroup
	flightPrefetchFn = func(ctx context.Context, client *batchexec.Client, target flightPrefetchTarget, opts SearchOptions) {
		defer wg.Done()
		calls.Add(1)
	}

	// Default (env unset) => prefetch disabled, no warms.
	t.Setenv("TRVL_PREFETCH", "")
	maybePrefetchFlights(context.Background(), nil, "HEL", "AGP", "2026-07-15", SearchOptions{})
	time.Sleep(20 * time.Millisecond)
	if calls.Load() != 0 {
		t.Fatalf("prefetch must be OFF by default, got %d warms", calls.Load())
	}

	// Enabled => warms dispatched.
	t.Setenv("TRVL_PREFETCH", "1")
	wg.Add(3)
	maybePrefetchFlights(context.Background(), nil, "HEL", "AGP", "2026-07-15", SearchOptions{})
	wg.Wait()
	if calls.Load() != 3 {
		t.Errorf("enabled prefetch ran %d warms, want 3", calls.Load())
	}
}

func TestMaybePrefetchFlights_NoRecursion(t *testing.T) {
	orig := flightPrefetchFn
	t.Cleanup(func() { flightPrefetchFn = orig })
	t.Setenv("TRVL_PREFETCH", "1")

	flightPrefetchFn = func(ctx context.Context, client *batchexec.Client, target flightPrefetchTarget, opts SearchOptions) {
		// A prefetch-spawned search must not itself dispatch more prefetch.
		if prefetchDisabled(ctx) {
			return
		}
		t.Error("prefetch context should have prefetch disabled")
	}
	runFlightPrefetch(context.Background(), nil, flightPrefetchTargets("HEL", "AGP", "2026-07-15"), SearchOptions{})
}
