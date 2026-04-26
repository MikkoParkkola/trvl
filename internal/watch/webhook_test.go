package watch

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"
)

type blockingWebhookTransport struct {
	started   chan struct{}
	cancelled chan struct{}
	once      sync.Once
}

func newBlockingWebhookTransport() *blockingWebhookTransport {
	return &blockingWebhookTransport{
		started:   make(chan struct{}),
		cancelled: make(chan struct{}),
	}
}

func (t *blockingWebhookTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.once.Do(func() {
		close(t.started)
	})
	<-req.Context().Done()
	close(t.cancelled)
	return nil, req.Context().Err()
}

func installBlockingWebhookClient(t *testing.T) *blockingWebhookTransport {
	t.Helper()

	transport := newBlockingWebhookTransport()
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: transport}
	t.Cleanup(func() {
		http.DefaultClient = oldClient
	})
	return transport
}

func waitForSignal(t *testing.T, ch <-chan struct{}, msg string) {
	t.Helper()

	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal(msg)
	}
}

type contextRecordingPriceChecker struct {
	ctx context.Context
}

func (c *contextRecordingPriceChecker) CheckPrice(ctx context.Context, _ Watch) (float64, string, string, error) {
	c.ctx = ctx
	return 450, "EUR", "", nil
}

type contextRecordingRoomChecker struct {
	ctx context.Context
}

func (c *contextRecordingRoomChecker) CheckRooms(ctx context.Context, _ Watch) ([]RoomMatch, error) {
	c.ctx = ctx
	return []RoomMatch{{Name: "Ocean Suite", Price: 180, Currency: "EUR"}}, nil
}

func TestCheckOne_WebhookUsesCallerContext(t *testing.T) {
	transport := installBlockingWebhookClient(t)

	dir := t.TempDir()
	store := NewStore(dir)
	w := Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "NRT",
		DepartDate:  "2099-07-01",
		LastPrice:   500,
		WebhookURL:  "http://example.test/webhook",
	}
	id, err := store.Add(w)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	w.ID = id

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		_ = checkOne(ctx, store, &stubPriceChecker{price: 450, currency: "EUR"}, w)
		close(done)
	}()

	waitForSignal(t, transport.started, "webhook request did not start")
	cancel()
	waitForSignal(t, transport.cancelled, "webhook request did not observe caller cancellation")
	waitForSignal(t, done, "checkOne did not return")
}

func TestCheckRoom_WebhookUsesCallerContext(t *testing.T) {
	transport := installBlockingWebhookClient(t)

	dir := t.TempDir()
	store := NewStore(dir)
	w := Watch{
		Type:         "room",
		HotelName:    "Test Hotel",
		RoomKeywords: []string{"suite"},
		DepartDate:   "2099-07-01",
		ReturnDate:   "2099-07-08",
		LastPrice:    250,
		WebhookURL:   "http://example.test/webhook",
	}
	id, err := store.Add(w)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	w.ID = id

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		_ = checkRoom(ctx, store, &stubRoomChecker{
			matches: []RoomMatch{{Name: "Ocean Suite", Price: 180, Currency: "EUR"}},
		}, w)
		close(done)
	}()

	waitForSignal(t, transport.started, "room webhook request did not start")
	cancel()
	waitForSignal(t, transport.cancelled, "room webhook request did not observe caller cancellation")
	waitForSignal(t, done, "checkRoom did not return")
}

func TestSchedulerRunOnce_WebhookUsesSchedulerContext(t *testing.T) {
	transport := installBlockingWebhookClient(t)

	dir := t.TempDir()
	store := NewStore(dir)
	_, err := store.Add(Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "NRT",
		DepartDate:  "2099-07-01",
		LastPrice:   500,
		WebhookURL:  "http://example.test/webhook",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	s := NewScheduler(dir, 100*time.Millisecond, &stubPriceChecker{price: 450, currency: "EUR"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		s.runOnce(ctx)
		close(done)
	}()

	waitForSignal(t, transport.started, "scheduler webhook request did not start")
	waitForSignal(t, done, "runOnce did not return")

	select {
	case <-transport.cancelled:
		t.Fatal("webhook request was cancelled when the check timeout finished")
	case <-time.After(100 * time.Millisecond):
	}

	cancel()
	waitForSignal(t, transport.cancelled, "scheduler webhook request did not observe scheduler cancellation")
}

func TestCheckAllWithRooms_NilContextFallsBackToBackgroundForPriceChecks(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_, err := store.Add(Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "NRT",
		DepartDate:  "2099-07-01",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	checker := &contextRecordingPriceChecker{}
	results := CheckAllWithRooms(context.TODO(), store, checker, nil)
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].Error != nil {
		t.Fatalf("unexpected error: %v", results[0].Error)
	}
	if checker.ctx == nil {
		t.Fatal("expected price checker to receive a non-nil context")
	}
}

func TestCheckAllWithRooms_NilContextFallsBackToBackgroundForRoomChecks(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_, err := store.Add(Watch{
		Type:         "room",
		HotelName:    "Test Hotel",
		RoomKeywords: []string{"suite"},
		DepartDate:   "2099-07-01",
		ReturnDate:   "2099-07-08",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	roomChecker := &contextRecordingRoomChecker{}
	results := CheckAllWithRooms(context.TODO(), store, &contextRecordingPriceChecker{}, roomChecker)
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].Error != nil {
		t.Fatalf("unexpected error: %v", results[0].Error)
	}
	if roomChecker.ctx == nil {
		t.Fatal("expected room checker to receive a non-nil context")
	}
}
