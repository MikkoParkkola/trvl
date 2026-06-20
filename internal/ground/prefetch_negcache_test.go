package ground

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/cache"
)

func TestGroundNegKey_FilteredSearchesIneligible(t *testing.T) {
	if _, ok := groundNegKey("Berlin", "Prague", "2026-07-15", SearchOptions{}, "all"); !ok {
		t.Error("unfiltered search should be negative-cache eligible")
	}
	if _, ok := groundNegKey("Berlin", "Prague", "2026-07-15", SearchOptions{MaxPrice: 50}, "all"); ok {
		t.Error("MaxPrice filter must make a search ineligible (filtered-empty != no service)")
	}
	if _, ok := groundNegKey("Berlin", "Prague", "2026-07-15", SearchOptions{Type: "train"}, "all"); ok {
		t.Error("Type filter must make a search ineligible")
	}
}

func TestGroundNegCache_ServesCleanNoResult(t *testing.T) {
	t.Setenv("TRVL_NEGCACHE", "1")
	t.Setenv("TRVL_PREFETCH", "0")
	groundNegCache = cache.NewNegativeCache(groundNegCacheTTL)
	t.Cleanup(func() { groundNegCache = cache.NewNegativeCache(groundNegCacheTTL) })

	key, ok := groundNegKey("Atlantis", "El Dorado", "2026-07-15", SearchOptions{Currency: "EUR"}, "all")
	if !ok {
		t.Fatal("expected eligible key")
	}
	groundNegCache.Mark(key)

	// A negatively-cached route must short-circuit to a clean empty without
	// running the provider fan-out (which would hit the network).
	res, err := SearchByName(context.Background(), "Atlantis", "El Dorado", "2026-07-15", SearchOptions{Currency: "EUR"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil || res.Success || res.Count != 0 {
		t.Errorf("expected clean empty result, got %+v", res)
	}
}

func TestGroundNegCache_DisabledDoesNotShortCircuit(t *testing.T) {
	// With negcache disabled, Seen must never be consulted. We assert the gate
	// directly to avoid a live fan-out.
	t.Setenv("TRVL_NEGCACHE", "0")
	if negCacheEnabled() {
		t.Fatal("negCacheEnabled() must be false when TRVL_NEGCACHE=0")
	}
	t.Setenv("TRVL_NEGCACHE", "")
	if !negCacheEnabled() {
		t.Fatal("negCacheEnabled() must default to true when unset")
	}
}

func TestGroundPrefetchTargets(t *testing.T) {
	targets := groundPrefetchTargets("Berlin", "Prague", "2026-07-15")
	if len(targets) != 3 {
		t.Fatalf("got %d targets, want 3", len(targets))
	}
	if targets[0].from != "Prague" || targets[0].to != "Berlin" || targets[0].date != "2026-07-22" {
		t.Errorf("return-leg target wrong: %+v", targets[0])
	}
	if targets[1].date != "2026-07-16" || targets[2].date != "2026-07-14" {
		t.Errorf("adjacent-day targets wrong: %+v %+v", targets[1], targets[2])
	}
	if got := groundPrefetchTargets("Berlin", "Prague", "bad"); got != nil {
		t.Errorf("unparseable date should yield no targets, got %v", got)
	}
}

func TestRunGroundPrefetch_BestEffortAndBounded(t *testing.T) {
	orig := groundPrefetchFn
	t.Cleanup(func() { groundPrefetchFn = orig })

	var calls atomic.Int32
	var inFlight, maxInFlight atomic.Int32
	groundPrefetchFn = func(ctx context.Context, target groundPrefetchTarget, opts SearchOptions) {
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
		panic("ground prefetch boom") // must be swallowed
	}

	runGroundPrefetch(context.Background(), groundPrefetchTargets("Berlin", "Prague", "2026-07-15"), SearchOptions{})

	if calls.Load() != 3 {
		t.Errorf("ran %d warms, want 3", calls.Load())
	}
	if maxInFlight.Load() > int32(groundPrefetchConcurrency) {
		t.Errorf("max concurrency %d exceeded cap %d", maxInFlight.Load(), groundPrefetchConcurrency)
	}
}

func TestMaybePrefetchGround_GatedOffByDefault(t *testing.T) {
	orig := groundPrefetchFn
	t.Cleanup(func() { groundPrefetchFn = orig })

	var calls atomic.Int32
	var wg sync.WaitGroup
	groundPrefetchFn = func(ctx context.Context, target groundPrefetchTarget, opts SearchOptions) {
		defer wg.Done()
		calls.Add(1)
	}

	t.Setenv("TRVL_PREFETCH", "")
	maybePrefetchGround(context.Background(), "Berlin", "Prague", "2026-07-15", SearchOptions{})
	time.Sleep(20 * time.Millisecond)
	if calls.Load() != 0 {
		t.Fatalf("prefetch must be OFF by default, got %d warms", calls.Load())
	}

	t.Setenv("TRVL_PREFETCH", "1")
	wg.Add(3)
	maybePrefetchGround(context.Background(), "Berlin", "Prague", "2026-07-15", SearchOptions{})
	wg.Wait()
	if calls.Load() != 3 {
		t.Errorf("enabled prefetch ran %d warms, want 3", calls.Load())
	}
}

func TestMaybePrefetchGround_NoRecursion(t *testing.T) {
	orig := groundPrefetchFn
	t.Cleanup(func() { groundPrefetchFn = orig })
	t.Setenv("TRVL_PREFETCH", "1")

	groundPrefetchFn = func(ctx context.Context, target groundPrefetchTarget, opts SearchOptions) {
		if groundPrefetchDisabled(ctx) {
			return
		}
		t.Error("prefetch context should have prefetch disabled")
	}
	runGroundPrefetch(context.Background(), groundPrefetchTargets("Berlin", "Prague", "2026-07-15"), SearchOptions{})
}
