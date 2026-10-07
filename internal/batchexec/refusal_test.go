package batchexec

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/cache"
)

const (
	wrbCode13 = ")]}'\n\n[[\"wrb.fr\",null,null,null,null,[13]],[\"di\",34],[\"af.httprm\",33,\"x\",79]]"
	wrbCode3  = ")]}'\n\n[[\"wrb.fr\",null,null,null,null,[3]]]"
)

func TestWrbStatusAndQuotaRefusal(t *testing.T) {
	code, ok := WrbStatus([]byte(wrbCode13))
	if !ok || code != 13 {
		t.Fatalf("code 13: got %d ok=%v", code, ok)
	}
	if !QuotaRefusal(200, []byte(wrbCode13)) {
		t.Fatal("code 13 should be a quota refusal")
	}

	code, ok = WrbStatus([]byte(wrbCode3))
	if !ok || code != 3 {
		t.Fatalf("code 3: got %d ok=%v", code, ok)
	}
	if QuotaRefusal(200, []byte(wrbCode3)) {
		t.Fatal("code 3 is a rejection, not a quota cooldown")
	}

	// A city success row carries JSON in slot 3 and a null status slot.
	city := []byte(`)]}'
[["wrb.fr","H028ib","[[[[3,\"Helsinki\",\"Helsinki\",\"Finland\",\"/m/01lbs\"]]]",null,null,null,"generic"]]`)
	if _, ok := WrbStatus(city); ok {
		t.Fatal("a city success row must not report a wrb status")
	}
	if QuotaRefusal(200, city) {
		t.Fatal("a city success row must not be a quota refusal")
	}

	live, err := os.ReadFile("../flights/testdata/google_flights_live_oneway.json")
	if err != nil {
		t.Fatalf("read live fare fixture: %v", err)
	}
	if code, ok := WrbStatus(live); ok {
		t.Fatalf("live fare reported wrb status %d", code)
	}
	if QuotaRefusal(200, live) || refusalBody(live) {
		t.Fatal("a real fare list must stay cacheable and must not arm a cooldown")
	}

	envelope, err := os.ReadFile("../flights/testdata/google_flights_error_response.json")
	if err != nil {
		t.Fatalf("read error fixture: %v", err)
	}
	if _, ok := WrbStatus(envelope); ok {
		t.Fatal("the rate-limit error object is not a wrb status code")
	}
	if !QuotaRefusal(200, envelope) || !refusalBody(envelope) {
		t.Fatal("rate-limit error object should be a quota refusal and not cacheable")
	}
	if !QuotaRefusal(429, []byte("rate limited")) {
		t.Fatal("HTTP 429 is a quota refusal even without a wrb body")
	}
}

func TestGoogleRefusalWait(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	if got := googleRefusalWait(429, "", nil, now); got != googleRefusalFloor {
		t.Fatalf("429 without a hint = %s, want %s", got, googleRefusalFloor)
	}
	if got := googleRefusalWait(429, "30", nil, now); got != 30*time.Second {
		t.Fatalf("429 Retry-After 30 = %s", got)
	}
	if got := googleRefusalWait(429, "999999", nil, now); got != googleRefusalCap {
		t.Fatalf("huge Retry-After = %s, want cap %s", got, googleRefusalCap)
	}
	past := now.Add(-time.Hour).Format(time.RFC1123)
	if got := googleRefusalWait(429, past, nil, now); got != googleRefusalFloor {
		t.Fatalf("past Retry-After = %s, want floor", got)
	}
	if got := googleRefusalWait(200, "", []byte(wrbCode13), now); got != googleRefusalLong {
		t.Fatalf("code 13 without a hint = %s, want %s", got, googleRefusalLong)
	}
	if got := googleRefusalWait(200, "45", []byte(wrbCode13), now); got != 45*time.Second {
		t.Fatalf("code 13 with Retry-After 45 = %s", got)
	}
	envelope, err := os.ReadFile("../flights/testdata/google_flights_error_response.json")
	if err != nil {
		t.Fatalf("read error fixture: %v", err)
	}
	if got := googleRefusalWait(200, "", envelope, now); got != 60*time.Second {
		t.Fatalf("error object retryAfterSeconds = %s, want 60s", got)
	}
}

func TestGoogle429OneUpstreamCall(t *testing.T) {
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Retry-After", "999999")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("rate limited"))
	}))
	defer ts.Close()

	dir := t.TempDir()
	c := NewTestClient(ts.URL)
	c.refusalDir = dir

	status, _, err := c.PostForm(context.Background(), FlightsURL, "f.req=one")
	if err != nil {
		t.Fatalf("first post: %v", err)
	}
	if status != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", status)
	}
	status, _, err = c.PostForm(context.Background(), FlightsURL+"&gl=FI", "f.req=two")
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("second post err = %v, want ErrRefused (status %d)", err, status)
	}
	if hits.Load() != 1 {
		t.Fatalf("upstream calls = %d, want 1", hits.Load())
	}

	deadline := readRefusalDeadline(t, dir)
	until := time.Until(deadline)
	if until < 14*time.Minute || until > 16*time.Minute {
		t.Fatalf("cooldown remaining = %s, want about 15m (capped hint)", until)
	}
}

func TestGoogle429OverlappingSearchesOneCall(t *testing.T) {
	// The first upstream response stays open until the other two searches have
	// had time to dial. The test client's limiter refills in about a millisecond,
	// so a lock that stops before the request lets both of them in.
	var hits atomic.Int32
	firstInHandler := make(chan struct{})
	releaseHandler := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseHandler) }) }
	t.Cleanup(release)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			close(firstInHandler)
		}
		<-releaseHandler
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer ts.Close()

	c := NewTestClient(ts.URL)
	c.refusalDir = t.TempDir()

	type result struct {
		status int
		err    error
	}
	results := make(chan result, 3)
	var wg sync.WaitGroup
	start := func() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, _, err := c.PostForm(context.Background(), FlightsURL, "f.req=overlap")
			results <- result{status, err}
		}()
	}
	start()
	select {
	case <-firstInHandler:
	case <-time.After(2 * time.Second):
		t.Fatal("first request never reached the handler")
	}
	start()
	start()
	time.Sleep(300 * time.Millisecond)
	if got := hits.Load(); got != 1 {
		release()
		wg.Wait()
		t.Fatalf("upstream calls while the first response is held = %d, want 1", got)
	}
	release()
	wg.Wait()
	close(results)

	var refused, limited int
	for res := range results {
		switch {
		case errors.Is(res.err, ErrRefused):
			refused++
		case res.err != nil:
			t.Fatalf("unexpected error: %v", res.err)
		case res.status == http.StatusTooManyRequests:
			limited++
		default:
			t.Fatalf("status %d with no error", res.status)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("upstream calls = %d, want 1", hits.Load())
	}
	if limited != 1 || refused != 2 {
		t.Fatalf("got %d HTTP 429 and %d refused, want 1 and 2", limited, refused)
	}
}

func TestGoogleRefusalSharedAcrossClients(t *testing.T) {
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer ts.Close()

	dir := t.TempDir()
	first := NewTestClient(ts.URL)
	first.refusalDir = dir
	if _, _, err := first.PostForm(context.Background(), FlightsURL, "f.req=first"); err != nil {
		t.Fatalf("first client: %v", err)
	}

	second := NewTestClient(ts.URL)
	second.refusalDir = dir
	_, _, err := second.PostForm(context.Background(), FlightsURL, "f.req=second")
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("second client err = %v, want ErrRefused", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("upstream calls = %d, want 1", hits.Load())
	}
}

func TestGoogleCode13NotCached(t *testing.T) {
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(wrbCode13))
	}))
	defer ts.Close()

	dir := t.TempDir()
	c := NewTestClient(ts.URL)
	c.refusalDir = dir
	c.cache = cache.New()

	status, _, err := c.SearchFlights(context.Background(), "payload")
	if err != nil || status != http.StatusOK {
		t.Fatalf("first search status=%d err=%v", status, err)
	}
	c.refuseUntil = time.Time{}
	if err := os.Remove(filepath.Join(dir, googleRefusalFilename)); err != nil {
		t.Fatalf("clear cooldown: %v", err)
	}
	if _, _, err := c.SearchFlights(context.Background(), "payload"); err != nil {
		t.Fatalf("second search: %v", err)
	}
	if hits.Load() != 2 {
		t.Fatalf("upstream calls = %d, want 2 (refusal must not be served from cache)", hits.Load())
	}
}

func TestGoogleCode3DoesNotArmCooldown(t *testing.T) {
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(wrbCode3))
	}))
	defer ts.Close()

	dir := t.TempDir()
	c := NewTestClient(ts.URL)
	c.refusalDir = dir
	for range 2 {
		status, _, err := c.PostForm(context.Background(), FlightsURL, "f.req=bad")
		if err != nil || status != http.StatusOK {
			t.Fatalf("status=%d err=%v", status, err)
		}
	}
	if hits.Load() != 2 {
		t.Fatalf("upstream calls = %d, want 2", hits.Load())
	}
	if _, err := os.Stat(filepath.Join(dir, googleRefusalFilename)); !os.IsNotExist(err) {
		t.Fatalf("code 3 wrote a cooldown file: %v", err)
	}
}

func TestGoogle5xxStillRetries(t *testing.T) {
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("recovered"))
	}))
	defer ts.Close()

	dir := t.TempDir()
	c := NewTestClient(ts.URL)
	c.refusalDir = dir
	status, body, err := c.PostForm(context.Background(), FlightsURL, "f.req=flake")
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if status != http.StatusOK || string(body) != "recovered" {
		t.Fatalf("status=%d body=%q", status, body)
	}
	if hits.Load() != 2 {
		t.Fatalf("upstream calls = %d, want 2", hits.Load())
	}
	if _, err := os.Stat(filepath.Join(dir, googleRefusalFilename)); !os.IsNotExist(err) {
		t.Fatalf("5xx wrote a cooldown file: %v", err)
	}
}

func TestNonGoogle429StillRetries(t *testing.T) {
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer ts.Close()

	dir := t.TempDir()
	c := newTestClient(ts.URL)
	c.refusalDir = dir
	status, body, err := c.Get(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if status != http.StatusOK || string(body) != "ok" {
		t.Fatalf("status=%d body=%q", status, body)
	}
	if hits.Load() != 3 {
		t.Fatalf("upstream calls = %d, want 3", hits.Load())
	}
	if _, err := os.Stat(filepath.Join(dir, googleRefusalFilename)); !os.IsNotExist(err) {
		t.Fatalf("non-Google 429 wrote a cooldown file: %v", err)
	}
}

func readRefusalDeadline(t *testing.T, dir string) time.Time {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, googleRefusalFilename))
	if err != nil {
		t.Fatalf("read cooldown: %v", err)
	}
	deadline, err := time.Parse(time.RFC3339Nano, string(raw))
	if err != nil {
		t.Fatalf("parse cooldown %q: %v", raw, err)
	}
	return deadline
}
