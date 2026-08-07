package logredact

import (
	"strings"
	"testing"
)

// TRVL.LOGLEAK.10 -- a JSON-escaped URL must be redacted like any other.
//
// urlRe required a literal "://". An upstream JSON body echoed into an error --
// {"link":"https:\/\/host\/search?from=HEL&to=NRT"} -- contains no such
// sequence, so nothing matched and the entire journey reached the log: host,
// path, origin, destination, dates. Every guard in the repository reported clean
// on it, because the leak was inside the redactor rather than at a call site.
//
// Found by adversarial second-opinion review, 2026-08-06, and confirmed by
// measurement before it was believed.
func TestTextRedactsJSONEscapedURLs(t *testing.T) {
	for _, in := range []string{
		`upstream said {"link":"https:\/\/api.example.test\/search?from=HEL&to=NRT"}`,
		`{"redirect":"http:\/\/host.example.test\/x?pax=2&date=2026-08-01"}`,
	} {
		got := Text(in)
		for _, leak := range []string{"HEL", "NRT", "pax=2", "2026-08-01", "api.example.test", "host.example.test"} {
			if strings.Contains(got, leak) {
				t.Errorf("escaped URL leaked %q:\n  in:  %s\n  out: %s", leak, in, got)
			}
		}
		if !strings.Contains(got, "url#") {
			t.Errorf("escaped URL was not fingerprinted, so the line cannot be correlated:\n  out: %s", got)
		}
	}
}

// The plain form must keep working -- widening the pattern must not trade one
// shape for another.
func TestTextStillRedactsPlainURLs(t *testing.T) {
	got := Text(`failed GET https://api.example.test/search?from=HEL&to=NRT`)
	if strings.Contains(got, "HEL") || strings.Contains(got, "api.example.test") {
		t.Errorf("plain URL leaked after widening the pattern for escaped ones: %s", got)
	}
	if !strings.Contains(got, "url#") {
		t.Errorf("plain URL was not fingerprinted: %s", got)
	}
}

// TRVL.LOGLEAK.11 -- a cookie header loses its WHOLE value, not its first pair.
//
// A key-name allowlist cannot decide which cookie pairs are sensitive: whatever
// is not on the list survives. `Cookie: theme=dark; booking_session=<token>`
// redacted on the leading `cookie` key and left the session token in place,
// because `booking_session` matched nothing in the list. The token was the only
// thing on that line worth protecting.
//
// The third case is the general one: a pair whose name nobody anticipated. It is
// the reason this rule takes the whole value rather than growing the allowlist,
// which can only ever cover names someone already thought of.
func TestTextRedactsTheWholeCookieHeader(t *testing.T) {
	for _, in := range []string{
		`Cookie: theme=dark; booking_session=SECRETVALUE`,
		`set-cookie: sid=SECRETVALUE; Path=/; HttpOnly`,
		`cookie=first; a_name_nobody_listed=SECRETVALUE`,
	} {
		got := Text(in)
		if strings.Contains(got, "SECRETVALUE") {
			t.Errorf("a cookie pair survived redaction:\n  in:  %s\n  out: %s", in, got)
		}
		if !strings.Contains(got, Redacted) {
			t.Errorf("cookie header was not redacted at all:\n  out: %s", got)
		}
	}
}

// TRVL.LOGLEAK.12 -- the cookie rule must stop at the end of the cookie.
//
// The first version of that rule consumed to end-of-line, so everything after a
// cookie mention was destroyed:
//
//	request failed cookie=abc status=500 elapsed=12ms
//	  ->  request failed cookie=<redacted>
//
//	{"Cookie":"sid=x","status":500,"error":"denied"}
//	  ->  {"Cookie=<redacted>
//
// The status, the timing and the failure reason all vanished, and the JSON was
// left unbalanced. That is a debuggability regression traded for a privacy fix,
// introduced hours earlier by the commit that closed the leak, and a redactor
// nobody can debug around is one that gets removed.
//
// Both halves are asserted together deliberately: the secret must go AND the
// neighbours must stay. Testing only the first is what shipped the over-reach.
func TestCookieRedactionDoesNotEatNeighbouringFields(t *testing.T) {
	for _, tc := range []struct {
		in   string
		keep []string
	}{
		{`request failed cookie=abc status=500 elapsed=12ms`, []string{"status=500", "elapsed=12ms"}},
		{`{"Cookie":"sid=SECRETVALUE","status":500,"error":"denied"}`, []string{`"status":500`, `"error":"denied"`}},
		{`cookie=a; b=c retry_after=30 provider=trainline`, []string{"retry_after=30", "provider=trainline"}},
	} {
		got := Text(tc.in)
		if strings.Contains(got, "SECRETVALUE") {
			t.Errorf("secret survived:\n  in:  %s\n  out: %s", tc.in, got)
		}
		for _, keep := range tc.keep {
			if !strings.Contains(got, keep) {
				t.Errorf("redaction ate %q, which is not part of the cookie and is what someone "+
					"debugging this line actually needs:\n  in:  %s\n  out: %s", keep, tc.in, got)
			}
		}
	}
}
