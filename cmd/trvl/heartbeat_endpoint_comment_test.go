package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestStartupCommentNamesCompiledHeartbeatURL keeps the comment above
// HeartbeatInBackground on the URL compiled in internal/telemetry. That
// comment is what a reader of main trusts. It has named a host the binary
// did not dial.
func TestStartupCommentNamesCompiledHeartbeatURL(t *testing.T) {
	mainSrc, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	heartSrc, err := os.ReadFile(filepath.Join("..", "..", "internal", "telemetry", "heartbeat.go"))
	if err != nil {
		t.Fatalf("read heartbeat.go: %v", err)
	}
	match := regexp.MustCompile(`defaultEndpoint\s*=\s*"([^"]+)"`).FindSubmatch(heartSrc)
	if match == nil {
		t.Fatal("heartbeat.go has no defaultEndpoint string")
	}
	compiled := string(match[1])
	host := compiled
	if i := strings.Index(compiled, "://"); i >= 0 {
		host = compiled[i+3:]
	}
	if slash := strings.IndexByte(host, '/'); slash >= 0 {
		host = host[:slash]
	}
	if host == "" {
		t.Fatalf("compiled default %q has no host", compiled)
	}

	text := string(mainSrc)
	call := strings.Index(text, "telemetry.HeartbeatInBackground(")
	if call < 0 {
		t.Fatal("main.go does not call telemetry.HeartbeatInBackground")
	}
	start := call - 500
	if start < 0 {
		start = 0
	}
	comment := text[start:call]
	if !strings.Contains(comment, host) {
		t.Fatalf("startup comment does not name compiled host %q", host)
	}
	if !strings.Contains(comment, "TRVL_TELEMETRY_ENDPOINT replaces that URL") {
		t.Fatal("startup comment must say TRVL_TELEMETRY_ENDPOINT replaces that URL")
	}
	if strings.Contains(text, "trvl.app") {
		t.Fatal("main.go still names trvl.app")
	}
}
