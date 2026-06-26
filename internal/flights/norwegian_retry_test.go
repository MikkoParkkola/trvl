package flights

// #357 TRVL-RETRY.2/.3: Norwegian honours a single bounded retry on HTTP 429,
// replaying the bodyless GET after the Retry-After backoff. These tests drive
// the path entirely offline via a scripted RoundTripper and an instant sleep
// seam, so no live origin is contacted and the suite stays fast. A persistent
// 429 must surface the rate-limit error (one retry, then give up); a 403
// bot-challenge must NOT be retried.

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

// norwegianScriptedRT returns a canned response per call index, counting calls.
type norwegianScriptedRT struct {
	calls atomic.Int32
	fn    func(n int32) *http.Response
}

func (s *norwegianScriptedRT) RoundTrip(_ *http.Request) (*http.Response, error) {
	return s.fn(s.calls.Add(1)), nil
}

func norwegianResp(status int, retryAfter, body string) *http.Response {
	h := http.Header{}
	if retryAfter != "" {
		h.Set("Retry-After", retryAfter)
	}
	h.Set("Content-Type", "application/json")
	return &http.Response{
		StatusCode: status,
		Header:     h,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// installNorwegianRetryStub swaps the client transport, the sleep seam and the
// limiter for deterministic offline behaviour, restoring them on cleanup.
func installNorwegianRetryStub(t *testing.T, rt http.RoundTripper) {
	t.Helper()
	t.Setenv("NORWEGIAN_API_BASE", "https://norwegian.test")

	origTransport := norwegianClient.Transport
	origAfter := norwegianAfter
	origLimiter := norwegianLimiter
	t.Cleanup(func() {
		norwegianClient.Transport = origTransport
		norwegianAfter = origAfter
		norwegianLimiter = origLimiter
	})

	norwegianClient.Transport = rt
	norwegianAfter = func(time.Duration) <-chan time.Time {
		ch := make(chan time.Time, 1)
		ch <- time.Time{}
		return ch
	}
	norwegianLimiter = rate.NewLimiter(rate.Limit(1000), 1)
}

func TestSearchNorwegian_429RetriesThenSucceeds(t *testing.T) {
	stub := &norwegianScriptedRT{fn: func(n int32) *http.Response {
		if n == 1 {
			return norwegianResp(http.StatusTooManyRequests, "1", `{"err":"rate limited"}`)
		}
		return norwegianResp(http.StatusOK, "", `{"flights":[]}`)
	}}
	installNorwegianRetryStub(t, stub)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := SearchNorwegian(ctx, "OSL", "LGW", "2026-07-01", "EUR", SearchOptions{})
	if err != nil {
		t.Fatalf("SearchNorwegian after one 429 retry: unexpected error %v", err)
	}
	if got := stub.calls.Load(); got != 2 {
		t.Errorf("upstream calls = %d, want 2 (one 429 + one successful retry)", got)
	}
}

func TestSearchNorwegian_429ExhaustsRetries(t *testing.T) {
	stub := &norwegianScriptedRT{fn: func(int32) *http.Response {
		return norwegianResp(http.StatusTooManyRequests, "1", `{"err":"rate limited"}`)
	}}
	installNorwegianRetryStub(t, stub)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := SearchNorwegian(ctx, "OSL", "LGW", "2026-07-01", "EUR", SearchOptions{})
	if err == nil {
		t.Fatal("SearchNorwegian with persistent 429: want rate-limit error, got nil")
	}
	if !strings.Contains(err.Error(), "rate-limited") {
		t.Errorf("error = %v, want it to surface the rate limit", err)
	}
	if got := stub.calls.Load(); got != 2 {
		t.Errorf("upstream calls = %d, want 2 (initial + one bounded retry, then give up)", got)
	}
}

// TestSearchNorwegian_403NotRetried guards the bot-challenge path: a 403 is a
// Cloudflare challenge, not a transient throttle, so it must fail on the first
// attempt with NO retry.
func TestSearchNorwegian_403NotRetried(t *testing.T) {
	stub := &norwegianScriptedRT{fn: func(int32) *http.Response {
		return norwegianResp(http.StatusForbidden, "", `<html>Are you human?</html>`)
	}}
	installNorwegianRetryStub(t, stub)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := SearchNorwegian(ctx, "OSL", "LGW", "2026-07-01", "EUR", SearchOptions{})
	if err == nil {
		t.Fatal("SearchNorwegian on 403: want blocked error, got nil")
	}
	if got := stub.calls.Load(); got != 1 {
		t.Errorf("upstream calls = %d, want 1 (403 must NOT be retried)", got)
	}
}
