// Package consent owns the questions "did the user decline this?" for the
// browser-access opt-outs, and nothing else.
//
// It exists because the answer has to be readable from packages that already
// depend on each other. internal/cookies imports internal/nab, so the cookie
// decline could not live in internal/cookies and be read from internal/nab
// without closing an import cycle; for a while it was written out twice, in
// both packages, held together by a cross-package test that failed if the two
// copies ever disagreed. That test was a compensating control for a problem
// that did not need to exist.
//
// This package imports nothing but the standard library, so anything can read
// it. Adding a dependency here would recreate the cycle it was made to avoid.
//
// The decline rules themselves are the SSOT: the wrappers left behind in
// internal/cookies, internal/nab and internal/providers keep their old names
// for their callers, but all of them answer from here.
package consent

import (
	"errors"
	"os"
	"strings"
)

// CookiesEnv is TRVL_NO_BROWSER_COOKIES: decline every read of the user's
// browser cookie stores, in this process and in any helper it would start.
//
// Reading those stores is what makes rail search work against operators that
// challenge non-browser traffic, so it is on by default. It is also a read of a
// local credential store -- on macOS it reaches the Keychain -- that the user
// did not ask for, which is the kind of thing someone is entitled to decline.
const CookiesEnv = "TRVL_NO_BROWSER_COOKIES"

// Tier2Env is TRVL_NO_TIER2_CDP: decline the headless browser that starts from
// an empty profile and keeps whatever cookies the site hands it. It never
// touches the user's own profile, which is why it is a separate decline from
// CookiesEnv rather than the same one.
const Tier2Env = "TRVL_NO_TIER2_CDP"

// Tier2LegacyEnv is TRVL_TIER2_CDP, which used to be the opt-IN before the
// headless path became the default. An explicit 0/false here still declines: a
// user who set it to keep the browser off meant it, and flipping the default
// must not quietly overrule them.
const Tier2LegacyEnv = "TRVL_TIER2_CDP"

// ErrTier2Declined is returned when the headless path is invoked after the user
// declined it. Nothing suppresses it -- a declined search fails rather than
// quietly returning worse results.
var ErrTier2Declined = errors.New("tier2 cdp cookie-refresh declined (unset TRVL_NO_TIER2_CDP to enable)")

// CookiesDeclined reports whether the user has declined browser cookie reads.
func CookiesDeclined() bool { return declined(os.Getenv(CookiesEnv)) }

// Tier2Declined reports whether the user has EXPLICITLY asked for no headless
// browser, by either of the two variables that can say so.
//
// The question is "did the user say no?", not "is the default on?". Its
// predecessor asked the latter, which a caller-supplied force option was
// allowed to overrule -- and every production caller passed that option, which
// left the opt-out with no effect on any real search. A setting that reads as a
// privacy control and silently is not.
func Tier2Declined() bool {
	if declined(os.Getenv(Tier2Env)) {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv(Tier2LegacyEnv))) {
	case "0", "false", "no":
		return true
	}
	return false
}

// declined is the whole rule: any non-empty value other than an explicit denial
// counts as a decline.
//
// Someone setting one of these is expressing a preference about their own
// credentials, and the least surprising reading of TRVL_NO_BROWSER_COOKIES=yes
// is that they meant it. Being liberal here can only ever refuse an access the
// user gestured at refusing; being strict would silently ignore them, which is
// the failure this whole opt-out exists to stop.
func declined(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "0", "false":
		return false
	default:
		return true
	}
}
