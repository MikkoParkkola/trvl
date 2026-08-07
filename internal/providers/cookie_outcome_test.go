package providers

import (
	"bytes"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

func TestBrowserCookiesReportsTestBinarySuppression(t *testing.T) {
	t.Setenv("TRVL_ALLOW_BROWSER_COOKIES", "")

	out, outcome := browserCookiesForURLWithOutcome("https://example.com/")
	if out != nil {
		t.Errorf("expected no cookies under the test guard, got %d", len(out))
	}
	if outcome != outcomeSuppressedInTest {
		t.Errorf("outcome = %v, want suppressed_in_test", outcome)
	}
}

func TestBrowserCookiesReportsBadURL(t *testing.T) {
	t.Setenv("TRVL_ALLOW_BROWSER_COOKIES", "1")

	for _, bad := range []string{"", "://nonsense", "not a url", "/relative/only"} {
		out, outcome := browserCookiesForURLWithOutcome(bad)
		if out != nil {
			t.Errorf("%q returned %d cookies, want none", bad, len(out))
		}
		if outcome != outcomeBadURL {
			t.Errorf("%q -> outcome %v, want bad_url", bad, outcome)
		}
	}
}

func TestBrowserCookiesReportsDeclined(t *testing.T) {
	t.Setenv("TRVL_ALLOW_BROWSER_COOKIES", "1")
	t.Setenv("TRVL_NO_BROWSER_COOKIES", "1")

	out, outcome := browserCookiesForURLWithOutcome("https://example.com/")
	if out != nil {
		t.Errorf("expected no cookies when declined, got %d", len(out))
	}
	if outcome != outcomeDeclined {
		t.Errorf("outcome = %v, want declined", outcome)
	}
}

func TestBrowserCookieBadURLLogDoesNotLeakInput(t *testing.T) {
	t.Setenv("TRVL_ALLOW_BROWSER_COOKIES", "1")

	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(previous)

	const secret = "do-not-log-this-token"
	_ = BrowserCookiesForURL("://nonsense?token=" + secret)

	got := buf.String()
	if !strings.Contains(got, "unusable target URL") {
		t.Errorf("unusable URL was not reported at warn level: %q", got)
	}
	if strings.Contains(got, secret) {
		t.Errorf("bad URL warning leaked caller input: %q", got)
	}
}

func TestBrowserCookiesEmptyWarmCacheIsNoMatch(t *testing.T) {
	t.Setenv("TRVL_ALLOW_BROWSER_COOKIES", "1")
	const targetURL = "https://example.com/"
	key := warmCacheKey(targetURL, "")
	done := make(chan struct{})
	close(done)

	warmCache.mu.Lock()
	warmCache.entries[key] = &warmCacheEntry{cookies: []*http.Cookie{}, done: done}
	warmCache.mu.Unlock()
	t.Cleanup(func() { InvalidateWarmCache(targetURL, "") })

	out, outcome := browserCookiesForURLWithOutcome(targetURL)
	if out != nil {
		t.Fatalf("empty warm cache returned %d cookies", len(out))
	}
	if outcome != outcomeNoMatch {
		t.Fatalf("empty warm cache outcome = %v, want no_match", outcome)
	}
}

func TestBrowserCookieOutcomeNamesAreDistinct(t *testing.T) {
	all := []browserCookieOutcome{
		outcomeFound, outcomeNoMatch, outcomeDeclined,
		outcomeSuppressedInTest, outcomeBadURL, outcomeReadFailed,
	}
	seen := make(map[string]bool, len(all))
	for _, outcome := range all {
		name := outcome.String()
		if name == "" || name == "unknown" {
			t.Errorf("outcome %d has no name", int(outcome))
		}
		if seen[name] {
			t.Errorf("outcome name %q is used twice", name)
		}
		seen[name] = true
	}
}
