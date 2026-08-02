package batchexec

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// captureLogs installs a raw JSON handler at Debug level as the default logger
// and restores the previous one on cleanup.
//
// The handler deliberately performs no scrubbing of its own. The assertion must
// fail when a call site logs a secret, not pass because a handler cleaned up
// after it; a scrubbing handler in the record path would make this test unable
// to detect the defect it is named for.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

// assertNoSecretInRecords fails if the sentinel appears anywhere in any emitted
// record, checking the decoded attribute values rather than only the raw byte
// stream so a JSON-escaped leak still trips the assertion.
func assertNoSecretInRecords(t *testing.T, buf *bytes.Buffer, sentinel string) {
	t.Helper()
	raw := buf.String()
	if raw == "" {
		t.Fatal("no log records were emitted; the test did not exercise the logging path")
	}
	if strings.Contains(raw, sentinel) {
		t.Errorf("sentinel %q reached a log record", sentinel)
	}
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		for k, v := range rec {
			s, ok := v.(string)
			if !ok {
				continue
			}
			if strings.Contains(s, sentinel) {
				t.Errorf("sentinel %q reached log attribute %q", sentinel, k)
			}
			if strings.Contains(s, "://") {
				t.Errorf("full URL reached log attribute %q: %q", k, s)
			}
		}
	}
}

// The request URL is logged on every attempt. A query string can carry a
// credential, so the emitted record must not contain it.
func TestRequestLogDoesNotLeakURL(t *testing.T) {
	const sentinel = "SENTINEL-QUERY-SECRET"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	buf := captureLogs(t)
	c := NewTestClient(srv.URL)
	if _, _, err := c.Get(context.Background(), "https://www.google.com/travel?api_key="+sentinel); err != nil {
		t.Fatalf("Get: %v", err)
	}
	assertNoSecretInRecords(t, buf, sentinel)
}

// net/http wraps transport failures in *url.Error, whose Error() embeds the
// full request URL including the query string. That error is logged at Warn,
// which survives the default log level, so the leak is not Debug-only.
func TestRetryWarnDoesNotLeakURLInsideError(t *testing.T) {
	const sentinel = "SENTINEL-RETRY-SECRET"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Cancel first so the retry backoff returns immediately, then break the
		// connection so the round-trip fails with a *url.Error.
		cancel()
		if hj, ok := w.(http.Hijacker); ok {
			conn, _, err := hj.Hijack()
			if err == nil {
				_ = conn.Close()
				return
			}
		}
		panic(http.ErrAbortHandler)
	}))
	defer srv.Close()

	buf := captureLogs(t)
	c := NewTestClient(srv.URL)
	_, _, _ = c.Get(ctx, "https://www.google.com/travel?token="+sentinel)
	assertNoSecretInRecords(t, buf, sentinel)
}
