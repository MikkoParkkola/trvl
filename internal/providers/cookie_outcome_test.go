package providers

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// TRVL.KOOKY.2 -- the browser-cookie reader must distinguish its outcomes.
//
// It used to answer every one of these with a bare nil. That ambiguity has a
// measured cost rather than a theoretical one: #529 was FILED on a misreading
// of it. A probe returned zero cookies for seven domains, which read as "this
// build cannot decrypt the cookie stores"; the reader had actually
// short-circuited on the test-binary guard and never opened a store. The
// issue's central claim had to be withdrawn a session later.
//
// The distinction that matters most is outcomeNoMatch vs outcomeReadFailed.
// One means "you are not logged in to this site" and is fixed by logging in.
// The other means the fallback is broken and no amount of logging in will help.
// Collapsed to nil, they are the same answer.

// TRVL.KOOKY.2a -- the test-binary guard reports itself rather than looking
// like an empty cookie store. This is the exact state that was misdiagnosed.
func TestBrowserCookiesReportsTestBinarySuppression(t *testing.T) {
	// Deliberately NOT setting TRVL_ALLOW_BROWSER_COOKIES: this test is about
	// what the guard says when it fires.
	t.Setenv("TRVL_ALLOW_BROWSER_COOKIES", "")

	out, outcome := browserCookiesForURLWithOutcome("https://example.com/")
	if out != nil {
		t.Errorf("expected no cookies under the test guard, got %d", len(out))
	}
	if outcome != outcomeSuppressedInTest {
		t.Errorf("outcome = %v, want suppressed_in_test -- a skipped read must not be reported as "+
			"an empty cookie store; reading that as a decryption failure is how #529 was filed",
			outcome)
	}
}

// TRVL.KOOKY.2b -- an unusable URL is its own outcome, not "no cookies".
func TestBrowserCookiesReportsBadURL(t *testing.T) {
	t.Setenv("TRVL_ALLOW_BROWSER_COOKIES", "1")

	for _, bad := range []string{"", "://nonsense", "not a url", "/relative/only"} {
		out, outcome := browserCookiesForURLWithOutcome(bad)
		if out != nil {
			t.Errorf("%q returned %d cookies, want none", bad, len(out))
		}
		if outcome != outcomeBadURL {
			t.Errorf("%q -> outcome %v, want bad_url -- a call-site error must not be reported as a "+
				"property of the user's machine", bad, outcome)
		}
	}
}

// TRVL.KOOKY.2c -- a declined user is its own outcome. Distinct from both
// "no cookies" and "could not read", because the user chose it.
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

// TRVL.KOOKY.2d -- the outcomes a user can act on reach a log level a user
// sees. A distinction that exists only in the type system does not satisfy
// "a fallback that cannot work must say so".
//
// Asserted through the exported wrapper, because that is what callers use; a
// test on the outcome value alone would pass even if nothing were ever logged.
func TestBrowserCookieOutcomesAreReported(t *testing.T) {
	t.Setenv("TRVL_ALLOW_BROWSER_COOKIES", "1")

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(prev)

	// A bad URL is reachable without touching a real cookie store, so it is the
	// one warn-level outcome this test can drive deterministically offline.
	_ = BrowserCookiesForURL("://nonsense")

	got := buf.String()
	if !strings.Contains(got, "unusable target URL") {
		t.Errorf("an unusable URL was not reported at warn level; silence here is what made three "+
			"distinct states indistinguishable. Log was: %q", got)
	}
}

// TRVL.KOOKY.2e -- outcomes must have distinct names. A String() that
// collapsed two of them would reintroduce the ambiguity in the logs, which is
// the surface a user actually reads.
func TestBrowserCookieOutcomeNamesAreDistinct(t *testing.T) {
	all := []browserCookieOutcome{
		outcomeFound, outcomeNoMatch, outcomeDeclined,
		outcomeSuppressedInTest, outcomeBadURL, outcomeReadFailed,
	}
	seen := make(map[string]bool, len(all))
	for _, o := range all {
		name := o.String()
		if name == "" || name == "unknown" {
			t.Errorf("outcome %d has no name", int(o))
		}
		if seen[name] {
			t.Errorf("outcome name %q is used twice -- the log surface cannot tell them apart", name)
		}
		seen[name] = true
	}
}
