package ground

// #357 TRVL-RETRY.2/.3: distribusion honours a single bounded retry on HTTP
// 429, replaying the bodyless GET after the Retry-After backoff. These tests
// drive the path entirely offline via a scripted RoundTripper and an instant
// sleep seam, so no live origin is contacted and the suite stays fast.

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

// scriptedRT returns a canned response per call index, counting invocations.
type scriptedRT struct {
	calls atomic.Int32
	fn    func(n int32) *http.Response
}

func (s *scriptedRT) RoundTrip(_ *http.Request) (*http.Response, error) {
	return s.fn(s.calls.Add(1)), nil
}

func resp429() *http.Response {
	h := http.Header{}
	h.Set("Retry-After", "1")
	return &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     h,
		Body:       io.NopCloser(strings.NewReader(`{"err":"rate limited"}`)),
	}
}

func resp200(body string) *http.Response {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     h,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// installRetryStub swaps the shared client transport, the sleep seam and the
// limiter for deterministic offline behaviour, restoring them on cleanup.
func installRetryStub(t *testing.T, rt http.RoundTripper) {
	t.Helper()
	t.Setenv("DISTRIBUSION_API_KEY", "test-key")

	origTransport := distribusionHTTPClient.Transport
	origAfter := distribusionAfter
	origLimiter := distribusionLimiter
	t.Cleanup(func() {
		distribusionHTTPClient.Transport = origTransport
		distribusionAfter = origAfter
		distribusionLimiter = origLimiter
	})

	distribusionHTTPClient.Transport = rt
	distribusionAfter = func(time.Duration) <-chan time.Time {
		ch := make(chan time.Time, 1)
		ch <- time.Time{}
		return ch
	}
	distribusionLimiter = rate.NewLimiter(rate.Limit(1000), 1)
}

func TestSearchDistribusion_429RetriesThenSucceeds(t *testing.T) {
	stub := &scriptedRT{fn: func(n int32) *http.Response {
		if n == 1 {
			return resp429()
		}
		return resp200(`{"data":[],"included":[]}`)
	}}
	installRetryStub(t, stub)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := SearchDistribusion(ctx, "helsinki", "tallinn", "2026-07-01", "EUR")
	if err != nil {
		t.Fatalf("SearchDistribusion after one 429 retry: unexpected error %v", err)
	}
	if got := stub.calls.Load(); got != 2 {
		t.Errorf("upstream calls = %d, want 2 (one 429 + one successful retry)", got)
	}
}

func TestSearchDistribusion_429ExhaustsRetries(t *testing.T) {
	stub := &scriptedRT{fn: func(int32) *http.Response { return resp429() }}
	installRetryStub(t, stub)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := SearchDistribusion(ctx, "helsinki", "tallinn", "2026-07-01", "EUR")
	if err == nil {
		t.Fatal("SearchDistribusion with persistent 429: want rate-limit error, got nil")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("error = %v, want it to surface the 429 rate limit", err)
	}
	if got := stub.calls.Load(); got != 2 {
		t.Errorf("upstream calls = %d, want 2 (initial + one bounded retry, then give up)", got)
	}
}
