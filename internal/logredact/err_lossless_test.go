package logredact

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

// TRVL.LOGLEAK.8 -- Err is lossless for errors that carry neither a URL nor
// credential-shaped text.
//
// This is what licenses wrapping every logged error rather than auditing each
// call site for whether its error could be a *url.Error. If Err mangled
// ordinary errors, uniform application would trade a privacy leak for a
// debuggability loss, and each site would need a judgement a future editor has
// to re-derive.
//
// The sweep for #531 found 34 log sites passing an error in files that make
// HTTP requests. Spot-checking showed the file-level signal over-counts --
// several are json.Marshal/Unmarshal failures that can never carry a URL. That
// is precisely why loss-free-ness matters: it makes the over-count harmless.
//
// NOTE ON THE SCOPE OF THIS CLAIM. An earlier version of this test was named
// "...WithoutAURL" and its five cases were all free of credential-shaped text,
// so it appeared to prove a broader invariant than it did: that ANY URL-free
// error survives byte-identical. It does not. Err is Text, and Text runs three
// substitutions -- URL, key=value credentials, and Authorization headers. A
// URL-free error containing `token=abc` IS rewritten. The narrower claim is the
// true one and is still sufficient to license the sweep; the broader claim was
// stated publicly in the PR justification and was wrong. Caught by adversarial
// second-opinion review, 2026-08-06. See TestErrRewritesCredentialShapedText
// below, which asserts the other half rather than leaving it undocumented.
func TestErrIsLosslessWithoutAURLOrCredential(t *testing.T) {
	for _, e := range []error{
		errors.New("unexpected end of JSON input"),
		fmt.Errorf("parse date: %w", errors.New(`cannot parse "2026-13-45"`)),
		errors.New("open /tmp/x.json: no such file or directory"),
		errors.New("context deadline exceeded"),
		fmt.Errorf("row %d: column %q missing", 7, "price"),
	} {
		if got := Err(e); got != e.Error() {
			t.Errorf("Err rewrote an error carrying neither URL nor credential:\n  in:  %q\n  out: %q", e.Error(), got)
		}
	}
}

// The other half of the invariant, asserted so the losslessness claim above
// cannot be read as unconditional. These errors carry no URL and are still
// rewritten -- deliberately, because a credential in an error message is a
// credential in a log line.
//
// The last case is a known false positive and is asserted as such: "cookie" is
// a credential-shaped key name, so a chocolate one is redacted too. That is the
// accepted cost of matching on key name rather than on value content. Anyone
// tempted to "fix" it should note that narrowing the key list is how Set-Cookie
// values start surviving into logs.
func TestErrRewritesCredentialShapedText(t *testing.T) {
	for _, tc := range []struct{ in, wantSubstring string }{
		{"upstream rejected: token=abc123 is expired", "token=" + Redacted},
		{"login failed for session_id: 99887766", "session_id=" + Redacted},
		{"config invalid: password=hunter2", "password=" + Redacted},
		{"bad header: Authorization: Bearer sk-live-1234567890", Redacted},
		{"cookie=chocolate not accepted", "cookie=" + Redacted}, // known false positive, see above
	} {
		got := Err(errors.New(tc.in))
		if got == tc.in {
			t.Errorf("Err left credential-shaped text untouched: %q", tc.in)
			continue
		}
		if !strings.Contains(got, tc.wantSubstring) {
			t.Errorf("Err rewrote %q to %q, want it to contain %q", tc.in, got, tc.wantSubstring)
		}
	}
	// And the secret value itself must be gone, not merely relabelled.
	if got := Err(errors.New("config invalid: password=hunter2")); strings.Contains(got, "hunter2") {
		t.Errorf("Err kept the secret value: %q", got)
	}
}

// And it must actually redact the case it exists for: net/http wraps every
// transport failure in a *url.Error whose Error() embeds the full request URL,
// query string included. That is how a search URL reaches a log at Warn without
// any "url" key for the key-name sweep to find.
func TestErrRedactsAURLEmbeddedInAnError(t *testing.T) {
	raw := "https://api.example.com/search?from=HEL&to=NRT&date=2026-08-01&pax=2"
	wrapped := &url.Error{Op: "Get", URL: raw, Err: errors.New("connection refused")}

	got := Err(wrapped)

	for _, leak := range []string{"HEL", "NRT", "2026-08-01", "pax=2", "api.example.com"} {
		if strings.Contains(got, leak) {
			t.Errorf("Err leaked %q from a wrapped URL error: %q", leak, got)
		}
	}
	if !strings.Contains(got, "url#") {
		t.Errorf("Err did not replace the URL with a fingerprint: %q", got)
	}
	if !strings.Contains(got, "connection refused") {
		t.Errorf("Err dropped the actual failure reason, which is the only reason to log it: %q", got)
	}
}
