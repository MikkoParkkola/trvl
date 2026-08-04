package waf

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/grafana/sobek"
)

// captureLogs installs a raw JSON handler at Debug level as the default logger.
// It scrubs nothing itself: a scrubbing handler in the record path could not
// detect the defect this file is named for.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

func assertAbsentFromRecords(t *testing.T, buf *bytes.Buffer, sentinels ...string) {
	t.Helper()
	raw := buf.String()
	if raw == "" {
		t.Fatal("no log records were emitted; the test did not exercise the logging path")
	}
	for _, s := range sentinels {
		if strings.Contains(raw, s) {
			t.Errorf("sentinel %q reached a log record", s)
		}
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
			for _, sentinel := range sentinels {
				if strings.Contains(s, sentinel) {
					t.Errorf("sentinel %q reached log attribute %q", sentinel, k)
				}
			}
			if strings.Contains(s, "://") {
				t.Errorf("full URL reached log attribute %q: %q", k, s)
			}
		}
	}
}

// TestConsoleLogFromChallengeScriptIsRedacted drives the real host bridge:
// stubs.js wires console.log/warn/error to __goLog, so anything the challenge
// script prints lands in a log record.
//
// This is attacker-controlled text. The WAF vendor's script runs with the
// session cookie and the full challenge URL in scope, and prints both during
// its own debugging. Whatever it prints must be scrubbed on the way out.
func TestConsoleLogFromChallengeScriptIsRedacted(t *testing.T) {
	const (
		urlToken   = "SENTINEL-WAF-URLTOKEN"
		bareSecret = "SENTINEL-WAF-BEARER-abcdefgh"
	)

	vm := sobek.New()
	loop := newEventLoop(vm)
	host := newVMHost(vm, loop, http.DefaultClient, "https://target.example", "test-ua")
	if err := host.install(); err != nil {
		t.Fatalf("install: %v", err)
	}
	if host.logger != nil {
		t.Fatal("logger must be nil so the slog branch under test runs")
	}

	buf := captureLogs(t)
	_, err := vm.RunString(`
		console.log("challenge at https://waf.example/verify?token=` + urlToken + `");
		console.error("authorization: Bearer ` + bareSecret + `");
	`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	assertAbsentFromRecords(t, buf, urlToken, bareSecret)
}
