package providers

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestProviderBreaker_ConcurrentSearchAndMark stresses the circuit-breaker
// read path in SearchHotels against concurrent MarkSuccess/MarkError writes on
// the shared provider configs, under the race detector.
//
// This is a REGRESSION GUARD for the #144 class: an unsynchronized read of the
// breaker fields (ErrorCount/LastError/LastErrorAt/LastSuccess) in the
// SearchHotels breaker loop, racing the lock-protected writes in
// MarkSuccess/MarkError, reached concurrently via singleflight in production.
//
// On the fixed code (SearchHotels reads via Registry.BreakerSnapshot under
// RLock) this test passes clean under `go test -race`. On the pre-#144 code
// (direct cfg.ErrorCount read in the loop) the race detector trips. The point
// of the test is to make that class CI-catchable instead of escaping to
// production: -race only sees races a test actually drives concurrently, and
// no prior test drove Search x Mark on the same providers.
func TestProviderBreaker_ConcurrentSearchAndMark(t *testing.T) {
	var current, peak atomic.Int64
	dir := t.TempDir()
	reg, err := NewRegistryAt(dir)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	const nprov = 8
	ids := make([]string, nprov)
	for i := 0; i < nprov; i++ {
		srv := fakeHotelServer(t, time.Millisecond, &current, &peak)
		id := fmt.Sprintf("fake-%02d", i)
		ids[i] = id
		registerFake(t, reg, srv, id)
	}
	rt := NewRuntime(reg)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Writers: hammer the lock-protected breaker mutations on shared configs.
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				for _, id := range ids {
					reg.MarkError(id, "boom")
					reg.MarkSuccess(id)
				}
			}
		}()
	}

	// Readers: hammer SearchHotels, whose breaker loop reads the same fields.
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_, _, _ = rt.SearchHotels(ctx, "Helsinki", 60.17, 24.94,
					"2026-06-01", "2026-06-02", "EUR", 2, nil)
			}
		}()
	}

	// Hammer for long enough that any racy interleave is observed under -race.
	time.Sleep(1500 * time.Millisecond)
	close(stop)
	wg.Wait()
}

// TestProviderBreaker_ConcurrentStatusReportAndMark stresses BuildStatusReport
// (the MCP provider_health tool + HTTP dashboard path) against concurrent
// MarkSuccess/MarkError writes on the shared provider configs, under the race
// detector.
//
// This is the MIK-5858 companion guard to the SearchHotels case above: before
// the fix, BuildStatusReport iterated Registry.List() (live shared pointers)
// and passed them to CircuitBreakerHealth, which read ErrorCount/LastErrorAt/
// LastSuccess directly — a data race against the lock-protected breaker writers
// reachable concurrently from an in-flight search. On the fixed code
// (BuildStatusReport iterates Registry.ListSafe(), which value-copies each
// config under RLock) this passes clean under `go test -race`.
func TestProviderBreaker_ConcurrentStatusReportAndMark(t *testing.T) {
	dir := t.TempDir()
	reg, err := NewRegistryAt(dir)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	const nprov = 8
	ids := make([]string, nprov)
	for i := 0; i < nprov; i++ {
		id := fmt.Sprintf("fake-%02d", i)
		ids[i] = id
		cfg := &ProviderConfig{
			ID:       id,
			Name:     id,
			Category: "hotels",
			Endpoint: "https://example.invalid/search",
			Method:   "GET",
		}
		if err := reg.Save(cfg); err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Writers: hammer the lock-protected breaker mutations on shared configs.
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				for _, id := range ids {
					reg.MarkError(id, "boom")
					reg.MarkSuccess(id)
				}
			}
		}()
	}

	// Readers: hammer BuildStatusReport, whose circuit-state derivation reads
	// the same breaker fields via CircuitBreakerHealth.
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = BuildStatusReport(reg, dir, time.Now())
			}
		}()
	}

	// Hammer for long enough that any racy interleave is observed under -race.
	time.Sleep(1500 * time.Millisecond)
	close(stop)
	wg.Wait()
}
