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

	text := string(mainSrc)
	call := strings.Index(text, "telemetry.HeartbeatInBackground(")
	if call < 0 {
		t.Fatal("main.go does not call telemetry.HeartbeatInBackground")
	}
	comment, ok := commentImmediatelyAbove(text, call)
	if !ok {
		t.Fatal("no comment immediately above HeartbeatInBackground")
	}
	if !commentNamesEndpoint(comment, compiled) {
		t.Fatalf("startup comment does not name compiled endpoint %q", compiled)
	}
	if !strings.Contains(comment, "TRVL_TELEMETRY_ENDPOINT replaces that URL") {
		t.Fatal("startup comment must say TRVL_TELEMETRY_ENDPOINT replaces that URL")
	}
	if strings.Contains(text, "trvl.app") {
		t.Fatal("main.go still names trvl.app")
	}
}

// commentNamesEndpoint reports whether comment names compiled as its own
// token. A shorter address that only sits inside the documented URL does
// not count.
func commentNamesEndpoint(comment, compiled string) bool {
	for _, line := range strings.Split(comment, "\n") {
		for _, field := range strings.Fields(line) {
			if strings.TrimRight(field, ".,;)") == compiled {
				return true
			}
		}
	}
	return false
}

// commentImmediatelyAbove returns the contiguous // lines directly above the
// call at index call. A blank line or a code line ends the block.
func commentImmediatelyAbove(text string, call int) (string, bool) {
	lineStart := strings.LastIndex(text[:call], "\n") + 1
	lines := strings.Split(text[:lineStart], "\n")
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	start := end
	for start > 0 && strings.HasPrefix(strings.TrimSpace(lines[start-1]), "//") {
		start--
	}
	if start == end {
		return "", false
	}
	return strings.Join(lines[start:end], "\n"), true
}
