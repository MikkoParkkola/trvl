package watch

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestSchedulerProbeHookInvoked verifies the Tier-1 probe hook fires with the
// active watches after a check round, and that a panicking hook does not crash
// the scheduler.
func TestSchedulerProbeHookInvoked(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	if _, _, err := store.Add(Watch{Type: "flight", Origin: "AMS", Destination: "VLC", Currency: "EUR"}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	s := NewScheduler(dir, time.Hour, NoopChecker{})

	var mu sync.Mutex
	var gotActive int
	called := make(chan struct{}, 1)
	s.SetProbeHook(func(_ context.Context, active []Watch) {
		mu.Lock()
		gotActive = len(active)
		mu.Unlock()
		select {
		case called <- struct{}{}:
		default:
		}
		panic("hook panics must be isolated") // must not crash the scheduler
	})

	s.Start()
	select {
	case <-called:
	case <-time.After(5 * time.Second):
		t.Fatal("probe hook was never invoked")
	}
	s.Stop() // must return cleanly despite the panicking hook

	mu.Lock()
	defer mu.Unlock()
	if gotActive != 1 {
		t.Fatalf("hook should see 1 active watch, got %d", gotActive)
	}
}

// TestSchedulerNilProbeHookNoop verifies a scheduler with no hook runs normally.
func TestSchedulerNilProbeHookNoop(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_, _, _ = store.Add(Watch{Type: "flight", Origin: "AMS", Destination: "VLC", Currency: "EUR"})

	s := NewScheduler(dir, time.Hour, NoopChecker{})
	s.Start()
	time.Sleep(50 * time.Millisecond)
	s.Stop() // clean shutdown with nil hook
}
