package ground

// #357 TRVL-RETRY.2/.3: rome2rio honours a single bounded retry on HTTP 429
// (replaying the bodyless GET after the Retry-After backoff) and fails fast on a
// 403 Cloudflare bot wall. A persistent 429 or a 403 must also break the outer
// thin-render retry loop rather than hammer a server already refusing. These
// tests drive the path entirely offline via the shared scriptedRT/resp* helpers
// (declared in distribusion_retry_test.go) plus an instant sleep seam.

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func resp403() *http.Response {
	return &http.Response{
		StatusCode: http.StatusForbidden,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(`<html>Are you human?</html>`)),
	}
}

// installRome2RioInstantBackoff swaps the 429 sleep seam for an instant channel,
// restoring it on cleanup, so a retry never actually waits out the backoff.
func installRome2RioInstantBackoff(t *testing.T) {
	t.Helper()
	orig := rome2rioAfter
	t.Cleanup(func() { rome2rioAfter = orig })
	rome2rioAfter = func(time.Duration) <-chan time.Time {
		ch := make(chan time.Time, 1)
		ch <- time.Time{}
		return ch
	}
}

func newRome2RioReq(t *testing.T, ctx context.Context) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://rome2rio.test/s/Helsinki/Tallinn", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	return req
}

func TestRome2RioDoWithRetry_429ThenSuccess(t *testing.T) {
	installRome2RioInstantBackoff(t)
	stub := &scriptedRT{fn: func(n int32) *http.Response {
		if n == 1 {
			return resp429()
		}
		return resp200("ok")
	}}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := rome2rioDoWithRetry(ctx, stub.RoundTrip, newRome2RioReq(t, ctx))
	if err != nil {
		t.Fatalf("rome2rioDoWithRetry after one 429 retry: unexpected error %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 after the retry succeeded", resp.StatusCode)
	}
	if got := stub.calls.Load(); got != 2 {
		t.Errorf("upstream calls = %d, want 2 (one 429 + one successful retry)", got)
	}
}

func TestRome2RioDoWithRetry_429Persists(t *testing.T) {
	installRome2RioInstantBackoff(t)
	stub := &scriptedRT{fn: func(int32) *http.Response { return resp429() }}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := rome2rioDoWithRetry(ctx, stub.RoundTrip, newRome2RioReq(t, ctx))
	if err != nil {
		t.Fatalf("rome2rioDoWithRetry persistent 429: unexpected transport error %v", err)
	}
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429 surfaced to the caller", resp.StatusCode)
	}
	if got := stub.calls.Load(); got != 2 {
		t.Errorf("upstream calls = %d, want 2 (initial + one bounded retry, then give up)", got)
	}
}

func TestRome2RioDoWithRetry_403NotRetried(t *testing.T) {
	stub := &scriptedRT{fn: func(int32) *http.Response { return resp403() }}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := rome2rioDoWithRetry(ctx, stub.RoundTrip, newRome2RioReq(t, ctx))
	if err != nil {
		t.Fatalf("rome2rioDoWithRetry on 403: unexpected transport error %v", err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403 returned immediately", resp.StatusCode)
	}
	if got := stub.calls.Load(); got != 1 {
		t.Errorf("upstream calls = %d, want 1 (403 bot wall must NOT be retried)", got)
	}
}

// TestSearchRome2Rio_403FailsFastNoOuterRetry proves the bot-wall sentinel breaks
// the outer thin-render retry loop: a 403 must hit the transport exactly once,
// not rome2rioThinRenderRetries+1 times.
func TestSearchRome2Rio_403FailsFastNoOuterRetry(t *testing.T) {
	stub := &scriptedRT{fn: func(int32) *http.Response { return resp403() }}

	origTransport := httpClient.Transport
	t.Cleanup(func() { httpClient.Transport = origTransport })
	httpClient.Transport = stub

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := SearchRome2Rio(ctx, "Helsinki", "Tallinn", false)
	if err == nil {
		t.Fatal("SearchRome2Rio on 403: want blocked error, got nil")
	}
	if !errors.Is(err, errRome2RioBlocked) {
		t.Errorf("error = %v, want it to wrap errRome2RioBlocked", err)
	}
	if got := stub.calls.Load(); got != 1 {
		t.Errorf("transport calls = %d, want 1 (403 must fail fast, not spend outer thin-render retries)", got)
	}
}
