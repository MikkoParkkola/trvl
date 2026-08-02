package watch

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

// fixedStatusWebhookTransport returns a canned status code for every request
// without touching the network, so these tests don't depend on httptest's
// loopback bind (which newSafeWebhookClient's SSRF dialer refuses) or on
// CheckRedirect policy (covered separately in webhook_redirect_round25_test.go).
type fixedStatusWebhookTransport struct {
	status int
}

func (t *fixedStatusWebhookTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: t.status,
		Body:       http.NoBody,
		Header:     make(http.Header),
	}, nil
}

func installFixedStatusWebhookClient(t *testing.T, status int) {
	t.Helper()
	oldClient := webhookHTTPClient
	webhookHTTPClient = &http.Client{Transport: &fixedStatusWebhookTransport{status: status}}
	t.Cleanup(func() { webhookHTTPClient = oldClient })
}

// captureSlog redirects the default slog logger to a buffer for the duration
// of the test and restores the prior default on cleanup.
func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &buf
}

// TestFireWebhook_4xx5xxLogsUndelivered: round 27 follow-up (CODEX-PUSH-REVIEW
// gate + Grok round-26 optional note). Round 25 added the 4xx/5xx branch in
// fireWebhook (check.go) but shipped with no direct test proving it actually
// logs -- only the 3xx redirect branch had dedicated coverage. Prove each
// status class logs (or doesn't) the expected "undelivered" warning, and that
// no response body or full URL (where a webhook token could live) is echoed.
func TestFireWebhook_4xx5xxLogsUndelivered(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		wantLog    bool
		wantSubstr string
	}{
		{name: "400 bad request logs undelivered", status: http.StatusBadRequest, wantLog: true, wantSubstr: "receiver returned an error status"},
		{name: "429 rate limited logs undelivered", status: http.StatusTooManyRequests, wantLog: true, wantSubstr: "receiver returned an error status"},
		{name: "500 server error logs undelivered", status: http.StatusInternalServerError, wantLog: true, wantSubstr: "receiver returned an error status"},
		{name: "200 OK logs nothing", status: http.StatusOK, wantLog: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			installFixedStatusWebhookClient(t, tc.status)
			buf := captureSlog(t)

			r := CheckResult{
				Watch: Watch{
					ID:         "watch-round27",
					WebhookURL: "http://example.test/hook?token=super-secret",
				},
			}
			fireWebhook(context.Background(), r)

			out := buf.String()
			if tc.wantLog {
				if !strings.Contains(out, tc.wantSubstr) {
					t.Fatalf("expected log containing %q, got: %s", tc.wantSubstr, out)
				}
				if !strings.Contains(out, "watch-round27") {
					t.Fatalf("expected log to include watch_id, got: %s", out)
				}
				// Round 24 hardening: never echo the full URL (path/query,
				// where a webhook token lives) or a response body.
				if strings.Contains(out, "token=super-secret") {
					t.Fatalf("log leaked webhook URL query/token: %s", out)
				}
			} else if out != "" {
				t.Fatalf("expected no log output for status %d, got: %s", tc.status, out)
			}
		})
	}
}
