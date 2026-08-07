package flights

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
)

// trvl#531: net/http transport errors embed the complete request URL. Drive a
// real provider call site and prove its warn log keeps only a correlation id.
func TestRunWizzairProviderRedactsTransportErrorURL(t *testing.T) {
	t.Setenv("TRVL_ALLOW_LOCAL_DESTINATIONS", "1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Error("test server cannot hijack connection")
			return
		}
		conn, _, err := hijacker.Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		_ = conn.Close()
	}))
	defer srv.Close()

	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(previousLogger)

	outcome := runWizzairProvider(
		context.Background(), batchexec.SharedClient(), "BUD", "BCN", "2026-08-20", "EUR",
		SearchOptions{Adults: 1, wizzHost: srv.URL},
	)
	if outcome.err == nil {
		t.Fatal("test server connection failure produced no provider error")
	}
	logged := logs.String()
	if strings.Contains(logged, srv.URL) || strings.Contains(logged, "http://") {
		t.Fatalf("provider log disclosed the transport URL: %q", logged)
	}
	if !strings.Contains(logged, "url#") {
		t.Fatalf("provider log omitted the redacted URL correlation id: %q", logged)
	}
}
