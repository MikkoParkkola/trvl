package ground

// #357 TRVL-RETRY.2/.3: the Trainline journey-search POST honours a single
// bounded retry on HTTP 429, replaying the request (rebuilding its body) after
// the Retry-After backoff. A persistent 429 surfaces after exactly one retry; a
// 403 bot wall is NOT retried by this helper (SearchTrainline's own fallback
// ladder owns the 403 path). These tests drive the seam entirely offline via the
// shared scriptedRT/resp* helpers (declared in distribusion_retry_test.go /
// rome2rio_retry_test.go) plus an instant sleep seam.

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// installTrainlineInstantBackoff swaps the 429 sleep seam for an instant channel,
// restoring it on cleanup, so a retry never actually waits out the backoff.
func installTrainlineInstantBackoff(t *testing.T) {
	t.Helper()
	orig := trainlineAfter
	t.Cleanup(func() { trainlineAfter = orig })
	trainlineAfter = func(time.Duration) <-chan time.Time {
		ch := make(chan time.Time, 1)
		ch <- time.Time{}
		return ch
	}
}

// stubTrainlineDo points the trainlineDo seam at a scripted RoundTripper,
// restoring the original on cleanup.
func stubTrainlineDo(t *testing.T, stub *scriptedRT) {
	t.Helper()
	orig := trainlineDo
	t.Cleanup(func() { trainlineDo = orig })
	trainlineDo = func(req *http.Request) (*http.Response, error) { return stub.RoundTrip(req) }
}

// makeTrainlineReq builds a fresh POST request (with body) per attempt, matching
// the production newTrainlineRequest closure shape.
func makeTrainlineReq(ctx context.Context) func() (*http.Request, error) {
	return func() (*http.Request, error) {
		return http.NewRequestWithContext(
			ctx, http.MethodPost,
			"https://trainline.test/api/journey-search/",
			strings.NewReader(`{"q":"x"}`),
		)
	}
}

func TestTrainlineDoWithRetry_429ThenSuccess(t *testing.T) {
	installTrainlineInstantBackoff(t)
	stub := &scriptedRT{fn: func(n int32) *http.Response {
		if n == 1 {
			return resp429()
		}
		return resp200("ok")
	}}
	stubTrainlineDo(t, stub)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := trainlineDoWithRetry(ctx, makeTrainlineReq(ctx))
	if err != nil {
		t.Fatalf("trainlineDoWithRetry after one 429 retry: unexpected error %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 after the retry succeeded", resp.StatusCode)
	}
	if got := stub.calls.Load(); got != 2 {
		t.Errorf("upstream calls = %d, want 2 (one 429 + one successful retry)", got)
	}
}

func TestTrainlineDoWithRetry_429Persists(t *testing.T) {
	installTrainlineInstantBackoff(t)
	stub := &scriptedRT{fn: func(int32) *http.Response { return resp429() }}
	stubTrainlineDo(t, stub)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := trainlineDoWithRetry(ctx, makeTrainlineReq(ctx))
	if err != nil {
		t.Fatalf("trainlineDoWithRetry persistent 429: unexpected transport error %v", err)
	}
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429 surfaced to the caller", resp.StatusCode)
	}
	if got := stub.calls.Load(); got != 2 {
		t.Errorf("upstream calls = %d, want 2 (initial + one bounded retry, then give up)", got)
	}
}

// TestTrainlineDoWithRetry_403NotRetried guards the bot-wall path: a 403 is a
// Datadome challenge, not a transient throttle, so the helper must return it on
// the first attempt with NO retry and let the caller's fallback ladder run.
func TestTrainlineDoWithRetry_403NotRetried(t *testing.T) {
	stub := &scriptedRT{fn: func(int32) *http.Response { return resp403() }}
	stubTrainlineDo(t, stub)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := trainlineDoWithRetry(ctx, makeTrainlineReq(ctx))
	if err != nil {
		t.Fatalf("trainlineDoWithRetry on 403: unexpected transport error %v", err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403 returned immediately", resp.StatusCode)
	}
	if got := stub.calls.Load(); got != 1 {
		t.Errorf("upstream calls = %d, want 1 (403 bot wall must NOT be retried)", got)
	}
}
