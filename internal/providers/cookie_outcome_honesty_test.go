package providers

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

// TRVL.KOOKY.2f -- the zero value must not be a claim.
//
// The enum began at outcomeFound, so a bare `return nil` from any future branch
// reported "cookies were found" while returning none. That is the same shape as
// the bug this type exists to kill, relocated into the fix. Asserted rather than
// commented, because the next person to add a branch will not read the comment.
func TestZeroOutcomeIsNotFound(t *testing.T) {
	var zero browserCookieOutcome

	if zero == outcomeFound {
		t.Fatal("the zero value of browserCookieOutcome is outcomeFound: any future branch that " +
			"returns without naming an outcome silently claims cookies were found")
	}
	if zero != outcomeUnknown {
		t.Errorf("zero value = %v, want outcomeUnknown", zero)
	}
	if got := zero.String(); got != "unknown" {
		t.Errorf("zero value renders as %q, want \"unknown\" -- an unset outcome must read as "+
			"\"nobody said\" in a log, never as a result", got)
	}
}

// TRVL.KOOKY.2g -- a bad URL reports the reason, not just the verdict.
//
// The reporting wrapper logs this error. When url.Parse accepts a string that
// simply has no host ("/relative/only"), it returns a nil error, so passing it
// through unchanged would leave the log naming a failure and giving no reason.
func TestBadURLCarriesAnError(t *testing.T) {
	t.Setenv("TRVL_ALLOW_BROWSER_COOKIES", "1")

	for _, bad := range []string{"", "://nonsense", "not a url", "/relative/only"} {
		_, outcome, err := browserCookiesForURLWithOutcome(bad)
		if outcome != outcomeBadURL {
			t.Fatalf("%q -> outcome %v, want bad_url", bad, outcome)
		}
		if err == nil {
			t.Errorf("%q reported bad_url with a nil error; the warning it produces would name a "+
				"failure and give no reason", bad)
		}
	}
}

// captureLogs runs fn with the default logger redirected, and returns what it
// wrote.
func captureLogs(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)
	fn()
	return buf.String()
}

// TRVL.KOOKY.2h -- the failure warning must not assert a cause it has not
// established.
//
// One branch used to answer EVERY read error with "grant Keychain access, or
// the browser may use app-bound encryption this build cannot read". A timeout
// therefore produced confident advice about permissions, sending the user to
// fix something that was not broken. That is the misdiagnosis #529 was filed
// on, pointed at the user instead of at us.
//
// Driven through reportOutcome by outcome rather than by provoking a real slow
// read, which would be timing-dependent and would not reliably reach the branch.
func TestTimeoutWarningDoesNotBlamePermissions(t *testing.T) {
	got := captureLogs(t, func() {
		reportOutcome("https://booking.invalid/search?from=HEL&to=NRT", outcomeTimedOut,
			errors.New("context deadline exceeded"))
	})

	for _, forbidden := range []string{"Keychain", "keychain", "app-bound", "decrypt", "permissions problem"} {
		if strings.Contains(got, forbidden) && !strings.Contains(got, "does not indicate a permissions problem") {
			t.Errorf("the timeout warning mentions %q: a timeout is not evidence of a permissions or "+
				"decryption problem, and saying so sends the user to fix something that is not broken.\n%s",
				forbidden, got)
		}
	}
	if !strings.Contains(got, "timed out") {
		t.Errorf("the timeout warning does not say it timed out:\n%s", got)
	}
}

// And the read-failure branch must carry the underlying error, so its hint is a
// suggestion rather than the only information present.
func TestReadFailureWarningCarriesTheUnderlyingError(t *testing.T) {
	got := captureLogs(t, func() {
		reportOutcome("https://booking.invalid/", outcomeReadFailed, errors.New("cookie store item not found"))
	})

	if !strings.Contains(got, "cookie store item not found") {
		t.Errorf("the read-failure warning dropped the actual error, leaving only a guess at the cause:\n%s", got)
	}
}

// TRVL.KOOKY.2i -- the bad-URL warning must not echo the URL it is complaining
// about.
//
// This branch logged the raw target URL, three lines below hostForLog, whose
// stated purpose is that "a cookie-related log line is the wrong place to risk
// echoing a query parameter". A URL being unparseable does not make the journey
// it describes less sensitive.
func TestBadURLWarningDoesNotEchoTheURL(t *testing.T) {
	got := captureLogs(t, func() {
		reportOutcome("://nonsense?from=HEL&to=NRT&date=2026-08-01&pax=2", outcomeBadURL,
			errors.New("parse error"))
	})

	for _, leak := range []string{"HEL", "NRT", "2026-08-01", "pax=2"} {
		if strings.Contains(got, leak) {
			t.Errorf("the bad-URL warning leaked %q from the target URL:\n%s", leak, got)
		}
	}
	if !strings.Contains(got, "url#") {
		t.Errorf("the bad-URL warning did not fingerprint the URL, so the line cannot be correlated:\n%s", got)
	}
}
