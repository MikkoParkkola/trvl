package watch

import (
	"context"
	"sync"
	"testing"
	"time"
)

// boundedBlockChecker blocks until the per-watch context is cancelled, then returns
// the context error. It is used to exercise the timeout path of CheckAllBounded.
type boundedBlockChecker struct{}

func (boundedBlockChecker) CheckPrice(ctx context.Context, _ Watch) (float64, string, string, error) {
	<-ctx.Done()
	return 0, "", "", ctx.Err()
}

// boundedCountChecker records the maximum number of concurrently in-flight checks so
// a test can assert the concurrency cap is honoured.
type boundedCountChecker struct {
	mu       sync.Mutex
	inFlight int
	maxSeen  int
	delay    time.Duration
}

func (c *boundedCountChecker) CheckPrice(ctx context.Context, _ Watch) (float64, string, string, error) {
	c.mu.Lock()
	c.inFlight++
	if c.inFlight > c.maxSeen {
		c.maxSeen = c.inFlight
	}
	c.mu.Unlock()

	select {
	case <-time.After(c.delay):
	case <-ctx.Done():
	}

	c.mu.Lock()
	c.inFlight--
	c.mu.Unlock()
	return 150, "EUR", "", nil
}

func addFlightWatch(t *testing.T, store *Store, origin, dest string) {
	t.Helper()
	if _, err := store.Add(Watch{
		Type:        "flight",
		Origin:      origin,
		Destination: dest,
		DepartDate:  "2026-09-01",
		Currency:    "EUR",
		BelowPrice:  200,
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
}

func TestCheckAllBounded_PricesAllWatchesInOrder(t *testing.T) {
	t.Parallel()
	store := NewStore(t.TempDir())
	addFlightWatch(t, store, "HEL", "CDG")
	addFlightWatch(t, store, "HEL", "BCN")
	addFlightWatch(t, store, "HEL", "LIS")

	checker := &stubPriceChecker{price: 150, currency: "EUR"}
	results := CheckAllBounded(context.Background(), store, checker, nil, BoundedOptions{})

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	wantDest := []string{"CDG", "BCN", "LIS"} // store.List() order preserved
	for i, r := range results {
		if r.Error != nil {
			t.Errorf("result[%d] unexpected error: %v", i, r.Error)
		}
		if r.NewPrice != 150 {
			t.Errorf("result[%d] NewPrice = %f, want 150", i, r.NewPrice)
		}
		if r.Watch.Destination != wantDest[i] {
			t.Errorf("result[%d] dest = %q, want %q (order not preserved)", i, r.Watch.Destination, wantDest[i])
		}
		if !r.BelowGoal {
			t.Errorf("result[%d] expected BelowGoal (150 <= 200)", i)
		}
	}
}

// TestCheckAllBounded_TimeoutIsHonest verifies a watch whose search exceeds the
// per-watch timeout is reported with an explicit Error and a zero price — never
// a fabricated price. This is the regression guard for the original bug where
// check_watches silently reported price 0 as if it were real data.
func TestCheckAllBounded_TimeoutIsHonest(t *testing.T) {
	t.Parallel()
	store := NewStore(t.TempDir())
	addFlightWatch(t, store, "HEL", "NRT")

	start := time.Now()
	results := CheckAllBounded(context.Background(), store, boundedBlockChecker{}, nil, BoundedOptions{
		PerWatchTimeout: 40 * time.Millisecond,
	})
	elapsed := time.Since(start)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Error == nil {
		t.Fatal("expected timeout error, got nil (a timed-out check must not look like a real result)")
	}
	if results[0].NewPrice != 0 {
		t.Errorf("NewPrice = %f, want 0 on timeout", results[0].NewPrice)
	}
	if elapsed > 2*time.Second {
		t.Errorf("bounded check took %v; per-watch timeout was not enforced", elapsed)
	}
}

func TestCheckAllBounded_RespectsConcurrencyCap(t *testing.T) {
	t.Parallel()
	store := NewStore(t.TempDir())
	for i := 0; i < 8; i++ {
		addFlightWatch(t, store, "HEL", "DST")
	}

	checker := &boundedCountChecker{delay: 30 * time.Millisecond}
	_ = CheckAllBounded(context.Background(), store, checker, nil, BoundedOptions{Concurrency: 2})

	if checker.maxSeen > 2 {
		t.Errorf("max concurrent checks = %d, want <= 2", checker.maxSeen)
	}
	if checker.maxSeen == 0 {
		t.Error("checker never ran")
	}
}

func TestCheckAllBounded_ParentCancel(t *testing.T) {
	t.Parallel()
	store := NewStore(t.TempDir())
	addFlightWatch(t, store, "HEL", "AMS")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled before the call

	results := CheckAllBounded(ctx, store, boundedBlockChecker{}, nil, BoundedOptions{})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Error == nil {
		t.Error("expected error when parent context already cancelled")
	}
}
