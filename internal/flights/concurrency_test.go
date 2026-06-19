package flights

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestRunProviderTasks_PreservesOrder verifies outcomes come back in task order
// regardless of completion order: task 0 finishes last, task 2 first.
func TestRunProviderTasks_PreservesOrder(t *testing.T) {
	tasks := []providerTask{
		{name: "a", run: func(ctx context.Context) providerOutcome {
			return providerOutcome{status: models.ProviderStatus{ID: "a"}, succeeded: true}
		}},
		{name: "b", run: func(ctx context.Context) providerOutcome {
			return providerOutcome{status: models.ProviderStatus{ID: "b"}, succeeded: true}
		}},
		{name: "c", run: func(ctx context.Context) providerOutcome {
			return providerOutcome{status: models.ProviderStatus{ID: "c"}, succeeded: true}
		}},
	}
	out := runProviderTasks(t.Context(), tasks, 6, time.Second)
	if len(out) != 3 {
		t.Fatalf("got %d outcomes, want 3", len(out))
	}
	for i, want := range []string{"a", "b", "c"} {
		if out[i].status.ID != want {
			t.Errorf("outcome[%d].ID = %q, want %q (order not preserved)", i, out[i].status.ID, want)
		}
	}
}

// TestRunProviderTasks_FailureIsolation verifies one task's error never cancels
// its peers: the failing task returns its error outcome, the others still run
// to completion with their normal outcomes.
func TestRunProviderTasks_FailureIsolation(t *testing.T) {
	var peerRan atomic.Bool
	tasks := []providerTask{
		{name: "boom", run: func(ctx context.Context) providerOutcome {
			return providerOutcome{err: errors.New("kaboom"), status: models.ProviderStatus{ID: "boom", Status: models.StatusFailed}}
		}},
		{name: "peer", run: func(ctx context.Context) providerOutcome {
			peerRan.Store(true)
			return providerOutcome{succeeded: true, status: models.ProviderStatus{ID: "peer", Status: models.StatusOK}}
		}},
	}
	out := runProviderTasks(t.Context(), tasks, 6, time.Second)
	if out[0].err == nil {
		t.Error("failing task should retain its error")
	}
	if !peerRan.Load() {
		t.Error("peer task must run despite the other task failing (no cancellation cascade)")
	}
	if !out[1].succeeded {
		t.Error("peer outcome should be a success")
	}
}

// TestRunProviderTasks_PerTaskTimeout verifies a task exceeding its slice of the
// budget is reported as a typed timeout outcome (outcome unknown), not silently
// dropped, and does not block the harness.
func TestRunProviderTasks_PerTaskTimeout(t *testing.T) {
	tasks := []providerTask{
		{name: "slow", run: func(ctx context.Context) providerOutcome {
			select {
			case <-ctx.Done():
				return providerOutcome{err: ctx.Err()}
			case <-time.After(5 * time.Second):
				return providerOutcome{succeeded: true}
			}
		}},
		{name: "fast", run: func(ctx context.Context) providerOutcome {
			return providerOutcome{succeeded: true, status: models.ProviderStatus{ID: "fast", Status: models.StatusOK}}
		}},
	}
	start := time.Now()
	out := runProviderTasks(t.Context(), tasks, 6, 50*time.Millisecond)
	elapsed := time.Since(start)
	if elapsed > 2*time.Second {
		t.Fatalf("harness blocked on slow task for %v; per-task timeout not enforced", elapsed)
	}
	if out[0].err == nil {
		t.Error("timed-out task should carry a deadline error")
	}
	if out[0].status.Status != models.StatusTimeout {
		t.Errorf("timed-out task status = %q, want timeout (outcome unknown, not empty)", out[0].status.Status)
	}
	if !out[1].succeeded {
		t.Error("fast task should still succeed alongside a slow peer")
	}
}

// TestRunProviderTasks_RespectsConcurrencyLimit verifies no more than `limit`
// tasks run simultaneously.
func TestRunProviderTasks_RespectsConcurrencyLimit(t *testing.T) {
	const limit = 2
	var inFlight, maxSeen atomic.Int32
	mk := func(id string) providerTask {
		return providerTask{name: id, run: func(ctx context.Context) providerOutcome {
			cur := inFlight.Add(1)
			for {
				m := maxSeen.Load()
				if cur <= m || maxSeen.CompareAndSwap(m, cur) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			inFlight.Add(-1)
			return providerOutcome{status: models.ProviderStatus{ID: id}}
		}}
	}
	tasks := []providerTask{mk("a"), mk("b"), mk("c"), mk("d"), mk("e")}
	runProviderTasks(t.Context(), tasks, limit, time.Second)
	if maxSeen.Load() > limit {
		t.Errorf("observed %d concurrent tasks, limit was %d", maxSeen.Load(), limit)
	}
}

// TestRunProviderTasks_Empty verifies the empty-task fast path.
func TestRunProviderTasks_Empty(t *testing.T) {
	if out := runProviderTasks(t.Context(), nil, 6, time.Second); len(out) != 0 {
		t.Errorf("empty tasks should yield empty outcomes, got %d", len(out))
	}
}
