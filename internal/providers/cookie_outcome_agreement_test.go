package providers

import (
	"testing"
)

// TRVL.KOOKY.2j -- a URL with no hostname is a bad URL, whatever its Host says.
//
// The check read `u.Host == ""`. "https://:443/" has a NON-EMPTY Host (":443")
// and an EMPTY Hostname, so it passed validation and the read ran against an
// empty domain suffix -- matching whatever that happens to match, rather than
// refusing a URL that names no host at all.
//
// Raised by adversarial second-opinion review, 2026-08-06.
func TestPortOnlyURLIsABadURL(t *testing.T) {
	t.Setenv("TRVL_ALLOW_BROWSER_COOKIES", "1")

	for _, bad := range []string{
		"https://:443/",
		"http://:80",
		"https://:8443/search?from=HEL",
	} {
		out, outcome, err := browserCookiesForURLWithOutcome(bad)
		if outcome != outcomeBadURL {
			t.Errorf("%q -> outcome %v, want bad_url: a URL with a port and no host names nothing "+
				"to read cookies for, and must not reach the cookie stores with an empty domain suffix",
				bad, outcome)
		}
		if out != nil {
			t.Errorf("%q returned %d cookies, want none", bad, len(out))
		}
		if err == nil {
			t.Errorf("%q reported bad_url with a nil error, so the warning it produces names a "+
				"failure and gives no reason", bad)
		}
	}
}

// TRVL.KOOKY.2k -- the outcome must agree with what is returned.
//
// permittedAfterRead re-asks for consent AFTER the seconds-long read, so it can
// empty the list at the last moment. Applying it to the cookies alone left the
// outcome saying "found" beside no cookies: a caller acting on the outcome would
// report a successful read that returned nothing. Same ambiguity #529 exists to
// remove, reintroduced by the consent gate instead of the reader.
//
// Driven through the real function with the opt-out set mid-flight is not
// possible from outside, so this asserts the invariant that matters and is
// checkable: outcomeFound is never returned with an empty cookie list, for any
// input this test can construct.
func TestFoundIsNeverReturnedWithNoCookies(t *testing.T) {
	t.Setenv("TRVL_ALLOW_BROWSER_COOKIES", "1")

	for _, u := range []string{
		"https://example.invalid/",
		"https://sub.example.invalid/path?q=1",
		"",
		"://nonsense",
	} {
		out, outcome, _ := browserCookiesForURLWithOutcome(u)
		if outcome == outcomeFound && len(out) == 0 {
			t.Errorf("%q returned outcome found with zero cookies: a caller reading the outcome "+
				"would report a successful read that produced nothing", u)
		}
	}
}

// TRVL.KOOKY.2l -- a declined read is declined, not "no cookies".
//
// The consent gate emptying the list must not be reported as outcomeNoMatch,
// which is the one outcome meaning "you are not logged in to this site". The
// stores may well have held cookies; the user withdrew permission to use them.
// Answering a question nobody asked, with a claim about their account, is the
// defect class this file exists to close.
func TestDeclinedAfterReadIsNotReportedAsNoMatch(t *testing.T) {
	t.Setenv("TRVL_ALLOW_BROWSER_COOKIES", "1")
	t.Setenv("TRVL_NO_BROWSER_COOKIES", "1")

	_, outcome, _ := browserCookiesForURLWithOutcome("https://example.invalid/")
	if outcome == outcomeNoMatch {
		t.Error("a user who declined browser access was told there are no cookies for the site, " +
			"which is a claim about their account rather than about their consent")
	}
	if outcome != outcomeDeclined {
		t.Errorf("outcome = %v, want declined", outcome)
	}
}
