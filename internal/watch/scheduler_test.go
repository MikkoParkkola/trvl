package watch

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// --- NoopChecker ---

func TestNoopChecker_ReturnsZeroPrice(t *testing.T) {
	t.Parallel()
	var c NoopChecker
	price, currency, date, err := c.CheckPrice(context.Background(), Watch{Currency: "EUR"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if price != 0 {
		t.Errorf("price = %f, want 0", price)
	}
	if currency != "EUR" {
		t.Errorf("currency = %q, want EUR", currency)
	}
	if date != "" {
		t.Errorf("date = %q, want empty", date)
	}
}

// --- NewScheduler defaults ---

func TestNewScheduler_DefaultInterval(t *testing.T) {
	t.Parallel()
	s := NewScheduler(t.TempDir(), 0, nil)
	if s.interval != 30*time.Minute {
		t.Errorf("interval = %v, want 30m", s.interval)
	}
}

func TestNewScheduler_DefaultChecker(t *testing.T) {
	t.Parallel()
	s := NewScheduler(t.TempDir(), time.Second, nil)
	if s.checker == nil {
		t.Fatal("checker should default to NoopChecker, not nil")
	}
	_, ok := s.checker.(NoopChecker)
	if !ok {
		t.Errorf("default checker type = %T, want NoopChecker", s.checker)
	}
}

func TestNewScheduler_CustomInterval(t *testing.T) {
	t.Parallel()
	s := NewScheduler(t.TempDir(), 5*time.Minute, nil)
	if s.interval != 5*time.Minute {
		t.Errorf("interval = %v, want 5m", s.interval)
	}
}

// --- Start / Stop lifecycle ---

func TestScheduler_StartStop_NoWatches(t *testing.T) {
	t.Parallel()
	// Large interval so the periodic tick never fires during the test.
	s := NewScheduler(t.TempDir(), time.Hour, NoopChecker{})
	s.Start()
	// Give the initial runOnce a moment to complete (it will find no watches).
	time.Sleep(20 * time.Millisecond)
	// Stop must return without hanging.
	done := make(chan struct{})
	go func() {
		s.Stop()
		close(done)
	}()
	select {
	case <-done:
		// ok
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() timed out — goroutine leaked")
	}
}

func TestScheduler_Stop_DoneAfterStop(t *testing.T) {
	t.Parallel()
	s := NewScheduler(t.TempDir(), time.Hour, NoopChecker{})
	s.Start()
	s.Stop()
	// The done channel is closed; reading it again must not block.
	select {
	case <-s.done:
		// ok
	default:
		t.Error("done channel should be closed after Stop")
	}
}

func TestScheduler_StopWithoutStart(t *testing.T) {
	t.Parallel()
	s := NewScheduler(t.TempDir(), time.Hour, NoopChecker{})

	done := make(chan struct{})
	go func() {
		s.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() timed out before Start()")
	}

	// Once stopped early, later Start/Stop calls must remain harmless.
	s.Start()
	s.Stop()
}

func TestScheduler_ConcurrentStartStop_DoesNotPanic(t *testing.T) {
	t.Parallel()

	for i := 0; i < 500; i++ {
		s := NewScheduler(t.TempDir(), time.Hour, NoopChecker{})
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			<-start
			s.Start()
		}()

		go func() {
			defer wg.Done()
			<-start
			s.Stop()
		}()

		close(start)
		wg.Wait()
	}
}

// --- CheckPrice called for active watches ---

// countingChecker counts how many times CheckPrice is called.
type countingChecker struct {
	calls atomic.Int64
	price float64
}

func (c *countingChecker) CheckPrice(_ context.Context, _ Watch) (float64, string, string, error) {
	c.calls.Add(1)
	return c.price, "EUR", "", nil
}

type recordingChecker struct {
	calls atomic.Int64
	ids   chan string
	price float64
}

func (c *recordingChecker) CheckPrice(_ context.Context, w Watch) (float64, string, string, error) {
	c.calls.Add(1)
	if c.ids != nil {
		c.ids <- w.ID
	}
	return c.price, "EUR", "", nil
}

type blockingChecker struct {
	started   chan struct{}
	cancelled chan struct{}
	startOnce sync.Once
	stopOnce  sync.Once
}

func newBlockingChecker() *blockingChecker {
	return &blockingChecker{
		started:   make(chan struct{}),
		cancelled: make(chan struct{}),
	}
}

func (c *blockingChecker) CheckPrice(ctx context.Context, _ Watch) (float64, string, string, error) {
	c.startOnce.Do(func() {
		close(c.started)
	})
	<-ctx.Done()
	c.stopOnce.Do(func() {
		close(c.cancelled)
	})
	return 0, "EUR", "", ctx.Err()
}

func TestScheduler_CallsCheckerForActiveWatches(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := NewStore(dir)

	// Add an active flight watch (future date).
	_, _, err := store.Add(Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2099-07-01",
		BelowPrice:  500,
		Currency:    "EUR",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	checker := &countingChecker{price: 300}
	// Use a short interval — the immediate runOnce on Start is what we're testing.
	s := NewScheduler(dir, time.Hour, checker)
	s.Start()

	// Wait for the initial check to complete.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if checker.calls.Load() >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	s.Stop()

	if checker.calls.Load() < 1 {
		t.Errorf("CheckPrice called %d times, want >= 1", checker.calls.Load())
	}
}

func TestScheduler_StopCancelsInflightChecks(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := NewStore(dir)

	_, _, err := store.Add(Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2099-07-01",
		BelowPrice:  500,
		Currency:    "EUR",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	checker := newBlockingChecker()
	s := NewScheduler(dir, time.Hour, checker)
	s.Start()

	select {
	case <-checker.started:
	case <-time.After(2 * time.Second):
		t.Fatal("CheckPrice did not start")
	}

	stopDone := make(chan struct{})
	go func() {
		s.Stop()
		close(stopDone)
	}()

	select {
	case <-checker.cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("CheckPrice did not observe cancellation from Stop")
	}

	select {
	case <-stopDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() timed out while a check was in flight")
	}
}

// --- Expired / past-date watches are skipped ---

func TestScheduler_SkipsPastDateWatches(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := NewStore(dir)

	// Add a watch with a date in the past.
	_, _, err := store.Add(Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2000-01-01", // well in the past
		BelowPrice:  500,
		Currency:    "EUR",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	checker := &countingChecker{price: 300}
	s := NewScheduler(dir, time.Hour, checker)
	s.Start()

	// Give the initial runOnce time to complete.
	time.Sleep(50 * time.Millisecond)
	s.Stop()

	// Past-date watch must be skipped — CheckPrice should not be called.
	if checker.calls.Load() != 0 {
		t.Errorf("CheckPrice called %d times for past-date watch, want 0", checker.calls.Load())
	}
}

func TestScheduler_SkipsPastDateRangeWatches(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := NewStore(dir)

	// Date range fully in the past.
	_, _, err := store.Add(Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "NRT",
		DepartFrom:  "2000-01-01",
		DepartTo:    "2000-01-15",
		BelowPrice:  500,
		Currency:    "EUR",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	checker := &countingChecker{price: 300}
	s := NewScheduler(dir, time.Hour, checker)
	s.Start()
	time.Sleep(50 * time.Millisecond)
	s.Stop()

	if checker.calls.Load() != 0 {
		t.Errorf("CheckPrice called %d times for past date-range watch, want 0", checker.calls.Load())
	}
}

func TestScheduler_ChecksOnlyActiveWatchesWhenStoreContainsPastEntries(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := NewStore(dir)

	activeID, _, err := store.Add(Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2099-07-01",
		BelowPrice:  500,
		Currency:    "EUR",
	})
	if err != nil {
		t.Fatalf("Add active watch: %v", err)
	}

	_, _, err = store.Add(Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "NRT",
		DepartDate:  "2000-01-01",
		BelowPrice:  500,
		Currency:    "EUR",
	})
	if err != nil {
		t.Fatalf("Add past watch: %v", err)
	}

	checker := &recordingChecker{
		ids:   make(chan string, 2),
		price: 300,
	}
	s := NewScheduler(dir, time.Hour, checker)
	s.Start()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if checker.calls.Load() >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	s.Stop()

	if checker.calls.Load() != 1 {
		t.Fatalf("CheckPrice called %d times, want 1", checker.calls.Load())
	}

	select {
	case gotID := <-checker.ids:
		if gotID != activeID {
			t.Fatalf("checked watch %q, want active watch %q", gotID, activeID)
		}
	default:
		t.Fatal("expected scheduler to record checked watch")
	}
}

// --- Route watches (no dates) are always active ---

func TestScheduler_AlwaysChecksRouteWatches(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := NewStore(dir)

	// Route watch: no dates.
	_, _, err := store.Add(Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "BCN",
		BelowPrice:  500,
		Currency:    "EUR",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	checker := &countingChecker{price: 300}
	s := NewScheduler(dir, time.Hour, checker)
	s.Start()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if checker.calls.Load() >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	s.Stop()

	if checker.calls.Load() < 1 {
		t.Errorf("route watch: CheckPrice called %d times, want >= 1", checker.calls.Load())
	}
}

// --- isActive unit tests ---

func TestIsActive_RouteWatch(t *testing.T) {
	t.Parallel()
	w := Watch{Type: "flight", Origin: "HEL", Destination: "BCN"}
	if !isActive(w, "2026-07-01") {
		t.Error("route watch should always be active")
	}
}

func TestIsActive_FutureDepartDate(t *testing.T) {
	t.Parallel()
	w := Watch{Type: "flight", Origin: "HEL", Destination: "BCN", DepartDate: "2099-01-01"}
	if !isActive(w, "2026-07-01") {
		t.Error("future depart date should be active")
	}
}

func TestIsActive_PastDepartDate(t *testing.T) {
	t.Parallel()
	w := Watch{Type: "flight", Origin: "HEL", Destination: "BCN", DepartDate: "2000-01-01"}
	if isActive(w, "2026-07-01") {
		t.Error("past depart date should not be active")
	}
}

func TestIsActive_TodayDepartDate(t *testing.T) {
	t.Parallel()
	today := time.Now().Format("2006-01-02")
	w := Watch{Type: "flight", Origin: "HEL", Destination: "BCN", DepartDate: today}
	if !isActive(w, today) {
		t.Error("today's depart date should be active")
	}
}

func TestIsActive_FutureDateRange(t *testing.T) {
	t.Parallel()
	w := Watch{Type: "flight", Origin: "HEL", Destination: "BCN", DepartFrom: "2099-01-01", DepartTo: "2099-01-15"}
	if !isActive(w, "2026-07-01") {
		t.Error("future date range should be active")
	}
}

func TestIsActive_PastDateRange(t *testing.T) {
	t.Parallel()
	w := Watch{Type: "flight", Origin: "HEL", Destination: "BCN", DepartFrom: "2000-01-01", DepartTo: "2000-01-15"}
	if isActive(w, "2026-07-01") {
		t.Error("past date range should not be active")
	}
}

func TestIsActive_DateRangeEndToday(t *testing.T) {
	t.Parallel()
	today := time.Now().Format("2006-01-02")
	w := Watch{Type: "flight", Origin: "HEL", Destination: "BCN", DepartFrom: "2000-01-01", DepartTo: today}
	if !isActive(w, today) {
		t.Error("date range ending today should be active")
	}
}

// --- activeWatches ---

func TestActiveWatches_FiltersCorrectly(t *testing.T) {
	t.Parallel()
	today := time.Now().Format("2006-01-02")

	watches := []Watch{
		{Type: "flight", Origin: "HEL", Destination: "BCN", DepartDate: "2099-01-01"}, // future
		{Type: "flight", Origin: "HEL", Destination: "BCN", DepartDate: "2000-01-01"}, // past — skip
		{Type: "flight", Origin: "HEL", Destination: "BCN"},                           // route — always
		{Type: "flight", Origin: "HEL", Destination: "BCN", DepartDate: today},        // today
	}

	active := activeWatches(watches)
	if len(active) != 3 {
		t.Errorf("got %d active watches, want 3", len(active))
	}
}

// countingRoomChecker records how many times the scheduler's own round
// actually invoked CheckRooms, so tests can prove the wiring runs through
// runOnce itself rather than calling a checker directly.
type countingRoomChecker struct {
	calls   atomic.Int64
	matches []RoomMatch
}

func (c *countingRoomChecker) CheckRooms(_ context.Context, _ Watch) ([]RoomMatch, error) {
	c.calls.Add(1)
	return c.matches, nil
}

// TestScheduler_SetRoomCheckerIsUsedByRunOnce guards against the gap an
// adversarial review found on 2026-07-28: before SetRoomChecker existed,
// runOnce hardcoded a nil room checker passed to
// checkWatchesWithRoomsAndWebhookContext, so a Scheduler had NO way to
// check room-type watches at all -- harmless before the cross-process
// scheduler singleton (the standalone daemon's own scheduler always had a
// real room checker, and before the lock both ran independently, so the
// daemon covered room watches regardless of what the embedded scheduler
// could do), but a real gap once the singleton lock means only ONE
// scheduler runs and it might be the MCP-embedded one, which mcp/server.go
// used to construct with nothing to set a room checker with.
func TestScheduler_SetRoomCheckerIsUsedByRunOnce(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := NewStore(dir)

	if _, _, err := store.Add(Watch{
		Type:         "room",
		HotelName:    "Test Hotel",
		DepartDate:   "2099-07-01",
		ReturnDate:   "2099-07-05",
		RoomKeywords: []string{"king"},
		BelowPrice:   200,
		Currency:     "EUR",
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	room := &countingRoomChecker{matches: []RoomMatch{{Name: "King Room", Price: 150, Currency: "EUR"}}}
	s := NewScheduler(dir, time.Hour, &countingChecker{price: 300})
	s.SetRoomChecker(room)
	s.Start()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if room.calls.Load() >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	s.Stop()

	if room.calls.Load() < 1 {
		t.Error("scheduler's own check round never invoked the room checker set via SetRoomChecker")
	}
}

// TestScheduler_WithNoRoomCheckerSetReportsNotConfigured is the control:
// a Scheduler.roomChecker field left at its zero value (nil) -- the
// pre-fix default, and still the default for any caller that never calls
// SetRoomChecker -- must produce exactly the same "not configured" error
// runOnce always passed through, unchanged by this fix.
func TestScheduler_WithNoRoomCheckerSetReportsNotConfigured(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := NewStore(dir)

	if _, _, err := store.Add(Watch{
		Type:         "room",
		HotelName:    "Test Hotel",
		DepartDate:   "2099-07-01",
		ReturnDate:   "2099-07-05",
		RoomKeywords: []string{"king"},
		BelowPrice:   200,
		Currency:     "EUR",
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	results := CheckAllWithRooms(t.Context(), store, &countingChecker{price: 300}, nil)
	if len(results) != 1 {
		t.Fatalf("expected 1 check result, got %d", len(results))
	}
	if results[0].Error == nil {
		t.Error("expected an error for an unconfigured room checker")
	}
}

// TestScheduler_LoserReacquiresLockAfterWinnerStops guards against the gap
// an adversarial review found on 2026-07-28: the original singleton-lock
// attempt in Start() was one-shot -- a process that lost the lock race
// gave up forever, with no retry of any kind. TryLockScheduler's own doc
// comment promises the OS releases the lock automatically when the holder
// dies, "so a crashed or SIGKILLed process cannot wedge scheduling for
// everyone else" -- but nothing ever re-attempted the lock on the loser's
// side, so that promise went unfulfilled: if the winner later died while
// a loser stayed alive (the exact multi-process-coexistence scenario this
// singleton exists to handle), scheduling stopped for good, system-wide,
// until some unrelated THIRD process happened to start.
//
// This proves failover actually works: scheduler A wins, scheduler B
// (same directory) loses and must retry, A stops (releasing the lock, the
// same as a crash from the OS's point of view -- flock releases on
// process exit either way), and B's own check round must then fire.
func TestScheduler_LoserReacquiresLockAfterWinnerStops(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := NewStore(dir)
	if _, _, err := store.Add(Watch{
		Type: "flight", Origin: "HEL", Destination: "BCN",
		DepartDate: "2099-07-01", BelowPrice: 500, Currency: "EUR",
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	checkerA := &countingChecker{price: 300}
	a := NewScheduler(dir, time.Hour, checkerA)
	a.Start()

	// Wait for A to actually win the lock and run its first round, so B's
	// upcoming Start() deterministically loses the race rather than
	// racing A for it.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && checkerA.calls.Load() < 1 {
		time.Sleep(10 * time.Millisecond)
	}
	if checkerA.calls.Load() < 1 {
		t.Fatal("setup: scheduler A never won the lock")
	}

	checkerB := &countingChecker{price: 300}
	b := NewScheduler(dir, time.Hour, checkerB)
	b.SetLockRetryIntervalForTest(50 * time.Millisecond)
	b.Start()
	defer b.Stop()

	// B must not have won anything yet -- A still holds the lock.
	time.Sleep(100 * time.Millisecond)
	if checkerB.calls.Load() != 0 {
		t.Fatal("setup: scheduler B ran a check before A released the lock")
	}

	// A stops, releasing the lock -- the same OS-level event as a crash.
	a.Stop()

	// B's retry loop (50ms interval) must notice within a couple of
	// intervals and take over.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && checkerB.calls.Load() < 1 {
		time.Sleep(10 * time.Millisecond)
	}
	if checkerB.calls.Load() < 1 {
		t.Fatal("scheduler B never reacquired the lock after A stopped -- failover did not happen")
	}
}
