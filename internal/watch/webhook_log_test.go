package watch

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

// captureWebhookLogs redirects the default slog logger into a buffer for the
// duration of the test and returns everything written to it.
//
// The assertions below deliberately run against this buffer rather than against
// webhookLogTarget or webhookSafeErr directly. A unit test of a redaction helper
// passes whether or not the call site actually calls it, which is the exact
// caller-versus-seam mistake that four review rounds on #521 each found.
func captureWebhookLogs(t *testing.T, fn func()) string {
	t.Helper()

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	fn()
	return buf.String()
}

type failingWebhookTransport struct{ err error }

func (t failingWebhookTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return nil, t.err
}

func installFailingWebhookClient(t *testing.T, err error) {
	t.Helper()

	old := webhookHTTPClient
	webhookHTTPClient = &http.Client{Transport: failingWebhookTransport{err: err}}
	t.Cleanup(func() { webhookHTTPClient = old })
}

// TRVL.WEBHOOKLOG.1 / TRVL.WEBHOOKLOG.2 — the POST-failed path.
func TestFireWebhookPostFailureDoesNotLogTheSecret(t *testing.T) {
	const secret = "T00000000-B11111111-zzzzzzzzzzzzzzzzzzzzzzzz"
	url := "https://hooks.slack.com/services/" + secret

	installFailingWebhookClient(t, errors.New("dial tcp: connection refused"))

	out := captureWebhookLogs(t, func() {
		fireWebhook(context.Background(), CheckResult{
			Watch: Watch{ID: "w-1", WebhookURL: url},
		})
	})

	if !strings.Contains(out, "webhook: POST failed") {
		t.Fatalf("expected the POST-failed line to be emitted, got: %s", out)
	}
	if strings.Contains(out, secret) {
		t.Errorf("webhook secret was written to the log record.\nsecret: %s\nrecord: %s", secret, out)
	}
	if !strings.Contains(out, "hooks.slack.com") {
		t.Errorf("host was dropped as well as the secret, leaving the line undiagnosable: %s", out)
	}
}

// TRVL.WEBHOOKLOG.2 — the request-construction path. net/http returns a
// *url.Error here whose Error() embeds the raw URL, so this line leaks through
// the "err" attribute even with no "url" attribute present.
func TestFireWebhookCreateRequestFailureDoesNotLogTheSecret(t *testing.T) {
	const secret = "T00000000-B11111111-yyyyyyyyyyyyyyyyyyyyyyyy"
	// A DEL byte makes url.Parse fail, so NewRequestWithContext returns before
	// any transport is involved.
	url := "https://hooks.slack.com/services/" + secret + "\x7f"

	out := captureWebhookLogs(t, func() {
		fireWebhook(context.Background(), CheckResult{
			Watch: Watch{ID: "w-2", WebhookURL: url},
		})
	})

	if !strings.Contains(out, "webhook: create request") {
		t.Fatalf("expected the create-request line to be emitted, got: %s", out)
	}
	if strings.Contains(out, secret) {
		t.Errorf("webhook secret leaked through the error value.\nsecret: %s\nrecord: %s", secret, out)
	}
}

func TestWebhookLogTargetKeepsHostAndDropsEverythingElse(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://hooks.slack.com/services/T0/B0/secret", "hooks.slack.com"},
		{"https://discord.com/api/webhooks/123/tok?wait=true", "discord.com"},
		// url.URL.Host excludes userinfo, so basic-auth credentials are dropped
		// as a side effect. Asserted so a future rewrite cannot regress it.
		{"https://user:pass@example.com/x", "example.com"},
		{"not a url at all", "invalid"},
		{"", "invalid"},
	}
	for _, c := range cases {
		if got := webhookLogTarget(c.in); got != c.want {
			t.Errorf("webhookLogTarget(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
