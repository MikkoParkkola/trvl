//go:build unix

package cookies

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testDomain = "cookies-test.invalid"

// writeFakeNab installs a fake `nab` first on PATH with the given body.
func writeFakeNab(t *testing.T, body string) (dir string) {
	t.Helper()
	dir = t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "nab"), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatalf("write fake nab: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	resetCookieCache()
	return dir
}

// TestBrowserCookies_SharesOneBudgetAcrossBrowsers proves a wedged helper is
// paid for once per search, not once per browser. BrowserCookies tries Brave
// then Chrome; with a per-browser deadline and no shared budget, a hung nab cost
// the search twice over.
func TestBrowserCookies_SharesOneBudgetAcrossBrowsers(t *testing.T) {
	writeFakeNab(t, "sleep 30")

	start := time.Now()
	got := BrowserCookies(testDomain)
	elapsed := time.Since(start)

	if got != "" {
		t.Fatalf("expected no cookies from a hung helper, got %q", got)
	}
	if elapsed > nabCookieBudget+3*time.Second {
		t.Fatalf("took %v across two browsers; the whole attempt shares one budget of %v", elapsed, nabCookieBudget)
	}
}

// TestBrowserCookies_SuppressesRepeatAfterFailure covers the shape that makes
// this expensive in practice: a WAF challenge repeats for every property in a
// result set, so without suppression each one re-pays the full budget for the
// same answer.
func TestBrowserCookies_SuppressesRepeatAfterFailure(t *testing.T) {
	dir := writeFakeNab(t, "echo ran >> \""+filepath.Join(t.TempDir(), "x")+"\"; exit 1")
	marker := filepath.Join(dir, "invoked")
	if err := os.WriteFile(filepath.Join(dir, "nab"),
		[]byte("#!/bin/sh\necho ran >> \""+marker+"\"\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("rewrite fake nab: %v", err)
	}

	for range 3 {
		_ = BrowserCookies(testDomain)
	}

	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("nab was never invoked: %v", err)
	}
	// Two browsers on the first attempt, then suppressed.
	if got := len(strings.Fields(string(data))); got != 2 {
		t.Fatalf("nab ran %d times across 3 calls; after the first failure the domain should be suppressed", got)
	}
}

// TestBrowserCookiesContext_HonoursCancellation proves a caller that has gone
// away neither waits nor causes work. Returning quickly is not enough on its own
// — the point is that no helper is launched for a request nobody wants, so the
// test asserts nab was never invoked.
func TestBrowserCookiesContext_HonoursCancellation(t *testing.T) {
	dir := writeFakeNab(t, "echo ran >> \""+filepath.Join(t.TempDir(), "unused")+"\"; sleep 30")
	marker := filepath.Join(dir, "invoked")
	if err := os.WriteFile(filepath.Join(dir, "nab"),
		[]byte("#!/bin/sh\necho ran >> \""+marker+"\"\nsleep 30\n"), 0o755); err != nil {
		t.Fatalf("rewrite fake nab: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	got := BrowserCookiesContext(ctx, testDomain)
	elapsed := time.Since(start)

	if got != "" {
		t.Fatalf("expected no cookies for a cancelled caller, got %q", got)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("a cancelled caller waited %v; it must return at once, not sit out the %v budget", elapsed, nabCookieBudget)
	}

	// Give a flight that should never have started time to launch its helper.
	// Checking the marker immediately would pass by luck: the caller returns
	// instantly either way, and the race would hide the defect.
	time.Sleep(time.Second)

	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a helper was launched for an already-cancelled request; nobody is waiting for its answer")
	}
}

// TestExtractViaNab_BoundedAndDetached is the regression test for the sibling of
// issue #507 found in the same audit.
//
// Cookie extraction is reached from ordinary hotel and rail searches — a
// Booking.com WAF challenge triggers it — and unlike the rail browser fallbacks
// it sits behind no opt-in. It shells out to nab, which needs macOS Keychain
// access to decrypt Chrome or Brave cookies and can therefore raise a
// permission prompt of its own. Unbounded, a wedged nab stalled the search
// forever and the next search started another one: the exact shape of #507.
//
// The fake nab hangs for 30s. A correct build gives up at nabCookieTimeout and
// returns no cookies rather than blocking the caller.
func TestExtractViaNab_BoundedAndDetached(t *testing.T) {
	writeFakeNab(t, "sleep 30")

	start := time.Now()
	got := extractViaNab(context.Background(), "brave", testDomain)
	elapsed := time.Since(start)

	if got != "" {
		t.Fatalf("expected no cookies from a hung helper, got %q", got)
	}
	if elapsed > nabCookieTimeout+3*time.Second {
		t.Fatalf("cookie extraction took %v; a search must not wait on a wedged helper (bound is %v)", elapsed, nabCookieTimeout)
	}
}
