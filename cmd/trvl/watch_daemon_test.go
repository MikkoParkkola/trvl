package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/watch"
)

const daemonWebhookSignalTimeout = 5 * time.Second

type stubWatchDaemonTicker struct {
	ch      chan time.Time
	stopped bool
}

type stubDaemonPriceChecker struct {
	price    float64
	currency string
}

func (c *stubDaemonPriceChecker) CheckPrice(context.Context, watch.Watch) (float64, string, string, error) {
	return c.price, c.currency, "", nil
}

func (t *stubWatchDaemonTicker) Chan() <-chan time.Time {
	return t.ch
}

func (t *stubWatchDaemonTicker) Stop() {
	t.stopped = true
}

func TestRunWatchDaemonRunsImmediatelyAndOnTick(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ticker := &stubWatchDaemonTicker{ch: make(chan time.Time, 1)}
	var buf bytes.Buffer
	runs := 0
	done := make(chan error, 1)

	go func() {
		done <- runWatchDaemon(ctx, &buf, time.Hour, true, func(context.Context) (int, error) {
			runs++
			if runs == 2 {
				cancel()
			}
			return 1, nil
		}, func(time.Duration) watchDaemonTicker {
			return ticker
		})
	}()

	ticker.ch <- time.Now()

	if err := <-done; err != nil {
		t.Fatalf("runWatchDaemon: %v", err)
	}
	if runs != 2 {
		t.Fatalf("run count = %d, want 2", runs)
	}
	if !ticker.stopped {
		t.Fatal("expected ticker to be stopped")
	}

	out := buf.String()
	if !strings.Contains(out, "Starting watch daemon (every 1h0m0s). Press Ctrl-C to stop.") {
		t.Fatalf("missing startup message in %q", out)
	}
	if !strings.Contains(out, "Watch daemon stopped.") {
		t.Fatalf("missing shutdown message in %q", out)
	}
}

func TestRunWatchDaemonLogsErrorsAndContinues(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ticker := &stubWatchDaemonTicker{ch: make(chan time.Time, 1)}
	var buf bytes.Buffer
	runs := 0
	done := make(chan error, 1)

	go func() {
		done <- runWatchDaemon(ctx, &buf, time.Hour, true, func(context.Context) (int, error) {
			runs++
			switch runs {
			case 1:
				return 0, errors.New("boom")
			case 2:
				cancel()
				return 1, nil
			default:
				return 1, nil
			}
		}, func(time.Duration) watchDaemonTicker {
			return ticker
		})
	}()

	ticker.ch <- time.Now()

	if err := <-done; err != nil {
		t.Fatalf("runWatchDaemon: %v", err)
	}
	if runs != 2 {
		t.Fatalf("run count = %d, want 2", runs)
	}

	out := buf.String()
	if !strings.Contains(out, "Initial: watch check failed: boom") {
		t.Fatalf("missing initial error log in %q", out)
	}
	if !strings.Contains(out, "Watch daemon stopped.") {
		t.Fatalf("missing shutdown message in %q", out)
	}
}

func TestRunWatchDaemonRejectsInvalidInterval(t *testing.T) {
	err := runWatchDaemon(context.Background(), &bytes.Buffer{}, 0, true, func(context.Context) (int, error) {
		return 0, nil
	}, nil)
	if err == nil {
		t.Fatal("expected invalid interval error")
	}
	if got := err.Error(); got != "watch interval must be greater than zero" {
		t.Fatalf("unexpected error: %q", got)
	}
}

type blockingDaemonWebhookTransport struct {
	started   chan struct{}
	cancelled chan struct{}
	once      sync.Once
}

func newBlockingDaemonWebhookTransport() *blockingDaemonWebhookTransport {
	return &blockingDaemonWebhookTransport{
		started:   make(chan struct{}),
		cancelled: make(chan struct{}),
	}
}

func (t *blockingDaemonWebhookTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.once.Do(func() {
		close(t.started)
	})
	<-req.Context().Done()
	close(t.cancelled)
	return nil, req.Context().Err()
}

func installBlockingDaemonWebhookClient(t *testing.T) *blockingDaemonWebhookTransport {
	t.Helper()

	transport := newBlockingDaemonWebhookTransport()
	oldClient := watch.SetWebhookHTTPClientForTest(&http.Client{Transport: transport})
	t.Cleanup(func() {
		watch.SetWebhookHTTPClientForTest(oldClient)
	})
	return transport
}

func waitForDaemonSignal(t *testing.T, ch <-chan struct{}, msg string) {
	t.Helper()

	select {
	case <-ch:
	case <-time.After(daemonWebhookSignalTimeout):
		t.Fatal(msg)
	}
}

func TestRunWatchCheckCycleWithRooms_WebhookUsesDaemonContext(t *testing.T) {
	transport := installBlockingDaemonWebhookClient(t)

	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	// USERPROFILE too: os.UserHomeDir reads HOME on unix but USERPROFILE on
	// Windows, so redirecting only HOME left this test sharing the
	// package-wide store from TestMain with every sibling test. On Windows it
	// saw watches those siblings had added and failed with count=3 -- while
	// passing on Linux and macOS the whole time.
	t.Setenv("USERPROFILE", tmp)

	store, err := watch.DefaultStore()
	if err != nil {
		t.Fatalf("DefaultStore: %v", err)
	}
	if _, _, err := store.Add(watch.Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "NRT",
		DepartDate:  "2099-07-01",
		LastPrice:   500,
		// Round 19 (internal/watch): w.Currency=="" with a prior LastPrice
		// is now treated as untrustworthy history and triggers a
		// currency-change reset (skipping threshold/webhook-trigger
		// checks) -- not what this test exercises. Known matching
		// currency required.
		Currency:   "EUR",
		WebhookURL: "http://example.test/webhook",
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		_, _ = runWatchCheckCycleWithRooms(ctx, &stubDaemonPriceChecker{price: 450, currency: "EUR"}, nil, &watch.Notifier{Out: &bytes.Buffer{}})
		close(done)
	}()

	waitForDaemonSignal(t, transport.started, "daemon webhook request did not start")
	waitForDaemonSignal(t, done, "runWatchCheckCycleWithRooms did not return")

	select {
	case <-transport.cancelled:
		t.Fatal("daemon webhook request was cancelled when the cycle timeout finished")
	case <-time.After(100 * time.Millisecond):
	}

	cancel()
	waitForDaemonSignal(t, transport.cancelled, "daemon webhook request did not observe daemon cancellation")
}

// recordingDaemonPriceChecker records which watch (by Origin+Destination)
// CheckPrice was actually invoked for. Used to prove which watches the
// daemon's check cycle really touched, independent of the count it reports.
type recordingDaemonPriceChecker struct {
	mu      sync.Mutex
	checked []string
}

func (c *recordingDaemonPriceChecker) CheckPrice(_ context.Context, w watch.Watch) (float64, string, string, error) {
	c.mu.Lock()
	c.checked = append(c.checked, w.Origin+"->"+w.Destination)
	c.mu.Unlock()
	return 100, "EUR", "", nil
}

func (c *recordingDaemonPriceChecker) calledFor() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.checked))
	copy(out, c.checked)
	return out
}

// TestRunWatchCheckCycleWithRooms_SkipsInactiveWatches guards against the
// gap an adversarial review found on 2026-07-28: runWatchCheckCycleWithRooms
// computed watch.ActiveWatches(store.List()) to report a count and decide
// whether to run at all, but the actual check call
// (CheckAllWithRoomsAndWebhookContext) re-derived its own UNFILTERED list
// from store.List() internally, ignoring the filtered slice entirely. Every
// stored watch -- active or not -- got checked (and paid the 3-second
// inter-check pause), and with enough expired watches ahead of the one
// active watch in store order, the 60-second check-cycle timeout could
// exhaust itself before the daemon ever reached the active watch, which
// then went unchecked for that cycle with no error surfaced.
//
// This asserts the structural fix directly and deterministically -- that
// the actual checker is invoked for the active watch and NEVER for the
// inactive one -- rather than trying to reproduce the real 60-second
// starvation race, which would make this test either flaky or minutes
// slow.
func TestRunWatchCheckCycleWithRooms_SkipsInactiveWatches(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	store, err := watch.DefaultStore()
	if err != nil {
		t.Fatalf("DefaultStore: %v", err)
	}

	// Inactive: travel date already passed.
	if _, _, err := store.Add(watch.Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "EXPIRED",
		DepartDate:  "2020-01-01",
	}); err != nil {
		t.Fatalf("Add expired watch: %v", err)
	}
	// Active: travel date far in the future.
	if _, _, err := store.Add(watch.Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "ACTIVE",
		DepartDate:  "2099-01-01",
	}); err != nil {
		t.Fatalf("Add active watch: %v", err)
	}

	checker := &recordingDaemonPriceChecker{}
	count, err := runWatchCheckCycleWithRooms(context.Background(), checker, nil, &watch.Notifier{Out: &bytes.Buffer{}})
	if err != nil {
		t.Fatalf("runWatchCheckCycleWithRooms: %v", err)
	}

	if count != 1 {
		t.Errorf("count = %d, want 1 (only the active watch)", count)
	}

	got := checker.calledFor()
	if len(got) != 1 || got[0] != "HEL->ACTIVE" {
		t.Errorf("checker was called for %v, want exactly [HEL->ACTIVE] -- "+
			"the expired watch must never reach the checker", got)
	}
}
