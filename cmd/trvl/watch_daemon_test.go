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

	store, err := watch.DefaultStore()
	if err != nil {
		t.Fatalf("DefaultStore: %v", err)
	}
	if _, err := store.Add(watch.Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "NRT",
		DepartDate:  "2099-07-01",
		LastPrice:   500,
		WebhookURL:  "http://example.test/webhook",
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
