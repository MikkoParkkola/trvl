package providers

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestProviderLimiterRegistry_GetOrCreate(t *testing.T) {
	r := NewProviderLimiterRegistry(0, 0) // falls back to defaults
	l1 := r.Limiter("ryanair")
	l2 := r.Limiter("ryanair")
	if l1 != l2 {
		t.Fatal("expected same limiter instance for same provider")
	}
	if r.Limiter("wizzair") == l1 {
		t.Fatal("expected distinct limiter for distinct provider")
	}
}

func TestProviderLimiterRegistry_SetLimit(t *testing.T) {
	r := NewProviderLimiterRegistry(time.Second, 1)
	r.SetLimit("fast", time.Millisecond, 5)
	l := r.Limiter("fast")
	if l.Burst() != 5 {
		t.Fatalf("burst = %d, want 5", l.Burst())
	}
	// rate.Every(1ms) => 1000 events/sec.
	if got := float64(l.Limit()); got < 900 || got > 1100 {
		t.Fatalf("limit = %v, want ~1000/s", got)
	}
}

func TestRunParallelAcrossProviders_Empty(t *testing.T) {
	res := RunParallelAcrossProviders(context.Background(), nil, nil)
	if len(res) != 0 {
		t.Fatalf("expected empty results, got %d", len(res))
	}
}

func TestRunParallelAcrossProviders_ResultsAligned(t *testing.T) {
	r := NewProviderLimiterRegistry(time.Nanosecond, 1000)
	var got [4]string
	tasks := []ProviderTask{
		{Provider: "a", Fn: func(context.Context) error { got[0] = "a0"; return nil }},
		{Provider: "b", Fn: func(context.Context) error { got[1] = "b0"; return nil }},
		{Provider: "a", Fn: func(context.Context) error { got[2] = "a1"; return nil }},
		{Provider: "b", Fn: func(context.Context) error { got[3] = "b1"; return nil }},
	}
	res := RunParallelAcrossProviders(context.Background(), r, tasks)
	if len(res) != 4 {
		t.Fatalf("len(res) = %d, want 4", len(res))
	}
	for i, e := range res {
		if e != nil {
			t.Fatalf("task %d error: %v", i, e)
		}
	}
	want := [4]string{"a0", "b0", "a1", "b1"}
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestRunParallelAcrossProviders_SerializesSameProvider verifies that two tasks
// for the SAME provider never overlap.
func TestRunParallelAcrossProviders_SerializesSameProvider(t *testing.T) {
	r := NewProviderLimiterRegistry(time.Nanosecond, 1000)
	var active int32
	var maxActive int32
	mk := func() func(context.Context) error {
		return func(context.Context) error {
			n := atomic.AddInt32(&active, 1)
			for {
				m := atomic.LoadInt32(&maxActive)
				if n <= m || atomic.CompareAndSwapInt32(&maxActive, m, n) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			atomic.AddInt32(&active, -1)
			return nil
		}
	}
	tasks := []ProviderTask{
		{Provider: "same", Fn: mk()},
		{Provider: "same", Fn: mk()},
		{Provider: "same", Fn: mk()},
	}
	RunParallelAcrossProviders(context.Background(), r, tasks)
	if maxActive != 1 {
		t.Fatalf("max concurrent same-provider tasks = %d, want 1", maxActive)
	}
}

// TestRunParallelAcrossProviders_ParallelAcrossProviders verifies that distinct
// providers run concurrently. Each provider's task blocks on a shared barrier;
// if execution were serial the barrier would never release and the test times
// out.
func TestRunParallelAcrossProviders_ParallelAcrossProviders(t *testing.T) {
	r := NewProviderLimiterRegistry(time.Nanosecond, 1000)
	var wg sync.WaitGroup
	wg.Add(2)
	barrier := func(context.Context) error {
		wg.Done() // signal arrival
		wg.Wait() // block until the other provider also arrives
		return nil
	}
	tasks := []ProviderTask{
		{Provider: "p1", Fn: barrier},
		{Provider: "p2", Fn: barrier},
	}

	done := make(chan struct{})
	go func() {
		RunParallelAcrossProviders(context.Background(), r, tasks)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("providers did not run in parallel (barrier deadlock)")
	}
}

func TestRunParallelAcrossProviders_ContextCancelled(t *testing.T) {
	r := NewProviderLimiterRegistry(time.Second, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	var ran int32
	tasks := []ProviderTask{
		{Provider: "a", Fn: func(context.Context) error { atomic.AddInt32(&ran, 1); return nil }},
		{Provider: "b", Fn: func(context.Context) error { atomic.AddInt32(&ran, 1); return nil }},
	}
	res := RunParallelAcrossProviders(ctx, r, tasks)
	for i, e := range res {
		if e == nil {
			t.Fatalf("task %d: expected context error, got nil", i)
		}
	}
	if atomic.LoadInt32(&ran) != 0 {
		t.Fatalf("expected no task bodies to run, ran=%d", ran)
	}
}

func TestProviderLimiterRegistry_WaitRespectsRate(t *testing.T) {
	r := NewProviderLimiterRegistry(50*time.Millisecond, 1)
	ctx := context.Background()
	start := time.Now()
	// First Wait consumes the burst token immediately; second must wait ~50ms.
	if err := r.Wait(ctx, "p"); err != nil {
		t.Fatal(err)
	}
	if err := r.Wait(ctx, "p"); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < 40*time.Millisecond {
		t.Fatalf("second Wait returned too fast: %v", elapsed)
	}
}

// ensure rate import is used even if the rest changes.
var _ = rate.Every
