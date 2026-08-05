package logredact

import (
	"errors"
	"fmt"
	"net/url"
	"testing"
)

// TRVL.LOGLEAK.8 -- Err must be lossless for errors that carry no URL.
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
func TestErrIsLosslessWithoutAURL(t *testing.T) {
	for _, e := range []error{
		errors.New("unexpected end of JSON input"),
		fmt.Errorf("parse date: %w", errors.New(`cannot parse "2026-13-45"`)),
		errors.New("open /tmp/x.json: no such file or directory"),
		errors.New("context deadline exceeded"),
		fmt.Errorf("row %d: column %q missing", 7, "price"),
	} {
		if got := Err(e); got != e.Error() {
			t.Errorf("Err rewrote an error carrying no URL:\n  in:  %q\n  out: %q", e.Error(), got)
		}
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
		if contains(got, leak) {
			t.Errorf("Err leaked %q from a wrapped URL error: %q", leak, got)
		}
	}
	if !contains(got, "url#") {
		t.Errorf("Err did not replace the URL with a fingerprint: %q", got)
	}
	if !contains(got, "connection refused") {
		t.Errorf("Err dropped the actual failure reason, which is the only reason to log it: %q", got)
	}
}

func contains(h, n string) bool {
	return len(n) > 0 && len(h) >= len(n) && (func() bool {
		for i := 0; i+len(n) <= len(h); i++ {
			if h[i:i+len(n)] == n {
				return true
			}
		}
		return false
	})()
}
