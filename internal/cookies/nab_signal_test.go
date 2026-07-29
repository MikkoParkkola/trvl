//go:build unix

package cookies

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureWarnings redirects the default logger into a buffer for the duration of
// the test and returns the warning-and-above lines written to it.
//
// The assertion has to be on what a user can SEE, which is why this captures the
// default logger rather than calling a reporting helper directly. A helper test
// passes whether or not the reader consults it -- the same seam argument #534
// makes about provenance.
func captureWarnings(t *testing.T) func() []string {
	t.Helper()

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	return func() []string {
		var out []string
		for _, line := range strings.Split(buf.String(), "\n") {
			if !strings.Contains(line, "level=WARN") && !strings.Contains(line, "level=ERROR") {
				continue
			}
			// The pre-read disclosure (#521) shares the Warn level with these
			// signals because it has to be seen, but it is a different kind of
			// statement: it says what is about to happen, not that anything
			// failed. Counting it here would make every assertion below about
			// failure reporting silently also an assertion about the notice.
			// Its own tests are in announce_test.go.
			if strings.Contains(line, "about to read your browser's cookie store") {
				continue
			}
			out = append(out, line)
		}
		return out
	}
}

// hideNab puts an empty directory on PATH so the nab lookup fails, which is the
// "this reader cannot work at all on this machine" outcome.
func hideNab(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
	resetCookieCache()
}

// TestBrowserCookies_SignalsWhenNabIsMissing is the #529 case. A machine without
// the helper has a cookie fallback that can never return anything, and the old
// reader reported that identically to a domain the user simply has no cookies
// for: an empty string. A user watching a search degrade to blocked-or-empty
// results had nothing to go on.
func TestBrowserCookies_SignalsWhenNabIsMissing(t *testing.T) {
	warnings := captureWarnings(t)
	hideNab(t)

	if got := BrowserCookies(testDomain); got != "" {
		t.Fatalf("expected no cookies without the helper, got %q", got)
	}

	got := warnings()
	if len(got) == 0 {
		t.Fatal("browser cookie reads cannot work without nab and nothing was reported; a fallback that cannot work must say so")
	}
	if !strings.Contains(strings.Join(got, "\n"), "nab") {
		t.Errorf("the warning does not name the helper the user has to install: %q", got)
	}
}

// TestBrowserCookies_SignalsWhenNabFails covers the second cannot-work outcome:
// the helper exists but cannot read the store -- a Keychain denial on macOS is
// the case that prompted the ticket. It is distinct from "no cookies here" and
// must not be reported as if it were.
func TestBrowserCookies_SignalsWhenNabFails(t *testing.T) {
	warnings := captureWarnings(t)
	writeFakeNab(t, "exit 1")

	if got := BrowserCookies(testDomain); got != "" {
		t.Fatalf("expected no cookies from a failing helper, got %q", got)
	}

	if len(warnings()) == 0 {
		t.Fatal("the cookie store could not be read and nothing was reported")
	}
}

// TestBrowserCookies_SilentWhenTheStoreSimplyHasNothing is the other half of the
// same gate. An ordinary miss -- the helper ran, the user has no cookies for this
// domain -- is not a failure, and warning about it would make the signal above
// worthless within one search.
func TestBrowserCookies_SilentWhenTheStoreSimplyHasNothing(t *testing.T) {
	warnings := captureWarnings(t)
	writeFakeNab(t, "exit 0")

	if got := BrowserCookies(testDomain); got != "" {
		t.Fatalf("expected no cookies, got %q", got)
	}

	if got := warnings(); len(got) != 0 {
		t.Fatalf("an ordinary cookie miss was reported as a failure: %q", got)
	}
}

// TestBrowserCookies_ReportsEachCauseOnce is the "do not spam" half of the
// ticket. A WAF challenge fires for every property in a result set, so a signal
// emitted per domain would put one line per property on the user's terminal for
// a single machine-level fact.
func TestBrowserCookies_ReportsEachCauseOnce(t *testing.T) {
	warnings := captureWarnings(t)
	hideNab(t)

	for _, domain := range []string{"a.invalid", "b.invalid", "c.invalid"} {
		_ = BrowserCookies(domain)
	}

	if got := warnings(); len(got) != 1 {
		t.Fatalf("want exactly one report for one machine-level fact across three domains, got %d: %q", len(got), got)
	}
}

// TestBrowserCookies_SilentWhenDeclined guards the direction this signal must
// never travel. A user who set the opt-out has not hit a failure, and a warning
// on that path would tell them -- and anything reading their terminal -- that a
// read was attempted at all. That is the bypass family #507, #521 and #530 exist
// to close, so the new reporting covers cannot-work outcomes only.
func TestBrowserCookies_SilentWhenDeclined(t *testing.T) {
	warnings := captureWarnings(t)
	hideNab(t)
	t.Setenv(DisableEnv, "1")

	if got := BrowserCookies(testDomain); got != "" {
		t.Fatalf("expected no cookies after a decline, got %q", got)
	}

	if got := warnings(); len(got) != 0 {
		t.Fatalf("a decline was reported as a cookie-reader failure: %q", got)
	}
}

// TestExtractViaNab_DistinguishesItsOutcomes pins the seam itself: the three
// outcomes the old signature collapsed into one empty string. Without this, the
// reporting above could be satisfied by a reader that guesses.
func TestExtractViaNab_DistinguishesItsOutcomes(t *testing.T) {
	t.Run("missing helper", func(t *testing.T) {
		hideNab(t)
		_, err := extractViaNab(context.Background(), "brave", testDomain)
		if err == nil {
			t.Fatal("a missing helper reported success")
		}
	})

	t.Run("helper failed", func(t *testing.T) {
		writeFakeNab(t, "exit 1")
		_, err := extractViaNab(context.Background(), "brave", testDomain)
		if err == nil {
			t.Fatal("a failing helper reported success")
		}
	})

	t.Run("no cookies for this domain", func(t *testing.T) {
		writeFakeNab(t, "exit 0")
		got, err := extractViaNab(context.Background(), "brave", testDomain)
		if err != nil {
			t.Fatalf("an ordinary miss was reported as a failure: %v", err)
		}
		if got != "" {
			t.Fatalf("got %q, want no cookies", got)
		}
	})

	t.Run("cookies found", func(t *testing.T) {
		dir := t.TempDir()
		script := "#!/bin/sh\nprintf '%s\\n' \"" + testDomain + "\tTRUE\t/\tTRUE\t0\tsess\tabc\"\n"
		if err := os.WriteFile(filepath.Join(dir, "nab"), []byte(script), 0o755); err != nil {
			t.Fatalf("write fake nab: %v", err)
		}
		t.Setenv("PATH", dir)
		resetCookieCache()

		got, err := extractViaNab(context.Background(), "brave", testDomain)
		if err != nil {
			t.Fatalf("a successful read reported a failure: %v", err)
		}
		if got != "sess=abc" {
			t.Fatalf("got %q, want %q", got, "sess=abc")
		}
	})
}
