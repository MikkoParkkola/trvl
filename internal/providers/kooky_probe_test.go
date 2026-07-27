package providers

import (
	"os"
	"testing"
)

// TestKookyReadsWhatNabReads is the deciding evidence for whether trvl still
// needs the nab helper to read browser cookies.
//
// Two readers exist today. This package reads cookie stores in-process with
// kooky; internal/cookies shells out to nab. The stated reason for nab is in
// internal/cookies/browser.go:1-4 — keeping "CGO and keychain dependencies" out
// of the main binary — which no longer holds, because kooky is linked into that
// same binary and does the same decryption. If kooky can read the domains nab
// reads, nab is redundant and one external process dependency can go.
//
// Skipped by default: it reads the developer's own live cookie stores, so it is
// a diagnostic to run deliberately, not something CI should do. Run with
// TRVL_COOKIE_PROBE=1.
//
// Reports counts and cookie NAMES only, never values. A cookie value is a live
// session; printing one into a terminal or a CI log is the leak this test would
// otherwise create.
func TestKookyReadsWhatNabReads(t *testing.T) {
	if os.Getenv("TRVL_COOKIE_PROBE") == "" {
		t.Skip("diagnostic: reads your own browser cookie stores; set TRVL_COOKIE_PROBE=1")
	}

	// The domains nab demonstrably returned cookies for while #521 was being
	// built, so a fair comparison rather than a friendly one.
	for _, target := range []string{
		"https://www.thetrainline.com/",
		"https://www.eurostar.com/",
		"https://www.sncf-connect.com/",
		"https://www.booking.com/",
		"https://www.rome2rio.com/",
		"https://www.google.com/",
		"https://github.com/",
	} {
		got := BrowserCookiesForURL(target)
		names := make([]string, 0, len(got))
		for _, c := range got {
			names = append(names, c.Name)
		}
		t.Logf("%-34s kooky returned %2d cookies %v", target, len(got), names)
	}
}
