package hotels

// #357 TRVL-RETRY.2/.3: the Trivago MCP tools-call POST honours a single bounded
// retry on HTTP 429, replaying the request (rebuilding its JSON-RPC body) after
// the Retry-After backoff. A persistent 429 surfaces after exactly one retry; a
// non-429 response is never retried. These tests drive the seam entirely offline
// via a scripted RoundTripper on the dedicated trivago client plus an instant
// sleep seam, so no live MCP endpoint is contacted.

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// trivagoScriptedRT returns a canned response per call index, counting calls.
type trivagoScriptedRT struct {
	calls atomic.Int32
	fn    func(n int32) *http.Response
}

func (s *trivagoScriptedRT) RoundTrip(*http.Request) (*http.Response, error) {
	return s.fn(s.calls.Add(1)), nil
}

func trivagoResp(status int, retryAfter, body string) *http.Response {
	h := http.Header{}
	if retryAfter != "" {
		h.Set("Retry-After", retryAfter)
	}
	return &http.Response{
		StatusCode: status,
		Header:     h,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// installTrivagoRetryStub swaps the client transport and the sleep seam for
// deterministic offline behaviour, restoring them on cleanup.
func installTrivagoRetryStub(t *testing.T, rt http.RoundTripper) {
	t.Helper()
	origTransport := trivagoHTTPClient.Transport
	origAfter := trivagoAfter
	t.Cleanup(func() {
		trivagoHTTPClient.Transport = origTransport
		trivagoAfter = origAfter
	})
	trivagoHTTPClient.Transport = rt
	trivagoAfter = func(time.Duration) <-chan time.Time {
		ch := make(chan time.Time, 1)
		ch <- time.Time{}
		return ch
	}
}

func makeTrivagoTestReq(ctx context.Context) func() (*http.Request, error) {
	return func() (*http.Request, error) {
		return http.NewRequestWithContext(
			ctx, http.MethodPost,
			"https://trivago.test/mcp",
			strings.NewReader(`{"jsonrpc":"2.0","method":"tools/call"}`),
		)
	}
}

func TestTrivagoDoWithRetry_429ThenSuccess(t *testing.T) {
	stub := &trivagoScriptedRT{fn: func(n int32) *http.Response {
		if n == 1 {
			return trivagoResp(http.StatusTooManyRequests, "1", `{"err":"rate limited"}`)
		}
		return trivagoResp(http.StatusOK, "", `{"ok":true}`)
	}}
	installTrivagoRetryStub(t, stub)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := trivagoDoWithRetry(ctx, makeTrivagoTestReq(ctx))
	if err != nil {
		t.Fatalf("trivagoDoWithRetry after one 429 retry: unexpected error %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 after the retry succeeded", resp.StatusCode)
	}
	if got := stub.calls.Load(); got != 2 {
		t.Errorf("upstream calls = %d, want 2 (one 429 + one successful retry)", got)
	}
}

func TestTrivagoDoWithRetry_429Persists(t *testing.T) {
	stub := &trivagoScriptedRT{fn: func(int32) *http.Response {
		return trivagoResp(http.StatusTooManyRequests, "1", `{"err":"rate limited"}`)
	}}
	installTrivagoRetryStub(t, stub)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := trivagoDoWithRetry(ctx, makeTrivagoTestReq(ctx))
	if err != nil {
		t.Fatalf("trivagoDoWithRetry persistent 429: unexpected transport error %v", err)
	}
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429 surfaced to the caller", resp.StatusCode)
	}
	if got := stub.calls.Load(); got != 2 {
		t.Errorf("upstream calls = %d, want 2 (initial + one bounded retry, then give up)", got)
	}
}

// TestTrivagoDoWithRetry_200NotRetried proves a healthy response is returned on
// the first attempt with no spurious replay.
func TestTrivagoDoWithRetry_200NotRetried(t *testing.T) {
	stub := &trivagoScriptedRT{fn: func(int32) *http.Response {
		return trivagoResp(http.StatusOK, "", `{"ok":true}`)
	}}
	installTrivagoRetryStub(t, stub)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := trivagoDoWithRetry(ctx, makeTrivagoTestReq(ctx))
	if err != nil {
		t.Fatalf("trivagoDoWithRetry on 200: unexpected error %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if got := stub.calls.Load(); got != 1 {
		t.Errorf("upstream calls = %d, want 1 (a healthy response must not be retried)", got)
	}
}
