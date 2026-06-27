package mcp

import (
	"regexp"
	"strings"
	"testing"
)

func TestGeneratedHTTPTokenLogMessageIsRedacted(t *testing.T) {
	msg := generatedHTTPTokenLogMessage()
	if !strings.Contains(msg, "redacted") {
		t.Fatalf("generated token log message = %q, want redaction notice", msg)
	}
	if tokenPattern := regexp.MustCompile(`\b[A-Za-z0-9_-]{43}\b`); tokenPattern.MatchString(msg) {
		t.Fatalf("generated token log message contains token-shaped value: %q", msg)
	}
}
