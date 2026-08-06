package providers

import (
	"log/slog"
	"net/http"
	"net/url"

	"github.com/MikkoParkkola/trvl/internal/cookies"
	"github.com/MikkoParkkola/trvl/internal/logredact"
)

// Browser-cookie read outcomes and their reporting (trvl#529).
//
// Split out of cookies.go, which the 800-line ceiling caught at 901. The seam
// is a real one rather than a line-count convenience: everything here is about
// naming WHY a read produced what it did and saying so at a level a user sees.
// The reading itself stays next to the cookie machinery.

// browserCookieOutcome names why a browser-cookie read produced what it did.
//
// The reader used to answer every one of these with a bare nil. Three of them
// are indistinguishable to a caller and to a user, and that ambiguity has a
// measured cost: a whole session was spent believing the in-process reader
// could not decrypt this machine's cookie stores, when it had actually
// short-circuited on the test-binary guard and never opened one (trvl#529). The
// issue was filed on that misreading and its central claim had to be withdrawn.
//
// "A fallback that cannot work must say so rather than return nil."
type browserCookieOutcome int

const (
	// outcomeUnknown is the zero value, and it exists ONLY so that the zero
	// value is not a claim.
	//
	// This enum previously began at outcomeFound, which meant a bare `return
	// nil` from any future branch reported "cookies were found" while returning
	// none. That is the identical shape as the bug this type was introduced to
	// kill (#529: one silent nil standing in for five distinct outcomes), just
	// relocated into the fix. An unset outcome must read as "nobody said", never
	// as the happy path.
	outcomeUnknown browserCookieOutcome = iota
	// outcomeFound: cookies matched and are being returned.
	outcomeFound
	// outcomeNoMatch: the stores were read and hold nothing for this domain.
	// The ordinary, uninteresting case -- and the ONLY one that means "you are
	// not logged in to this site".
	outcomeNoMatch
	// outcomeDeclined: the user set TRVL_NO_BROWSER_COOKIES. Not a failure.
	outcomeDeclined
	// outcomeSuppressedInTest: the test-binary guard skipped the read to avoid
	// repeated macOS Keychain prompts. Expected under `go test`, and the exact
	// state that was misread as a decryption failure.
	outcomeSuppressedInTest
	// outcomeBadURL: the target URL could not be parsed or had no host. A
	// programming error at the call site, not a property of the machine.
	outcomeBadURL
	// outcomeTimedOut: the read did not finish inside browserCookieLookupTimeout.
	// Split out of outcomeReadFailed because the two have opposite advice: a
	// timeout says nothing about whether the store is readable, and telling a
	// user to grant Keychain access because their disk was slow is a confident
	// wrong answer -- which is how #529 came to be filed in the first place.
	outcomeTimedOut
	// outcomeReadFailed: a cookie store existed but could not be read --
	// Keychain access denied, or a value this build cannot decrypt. This is the
	// one that must reach a user: the fallback is not merely empty, it is
	// broken, and no amount of logging into the site in their browser will fix
	// it.
	outcomeReadFailed
)

func (o browserCookieOutcome) String() string {
	switch o {
	case outcomeFound:
		return "found"
	case outcomeNoMatch:
		return "no_match"
	case outcomeDeclined:
		return "declined"
	case outcomeSuppressedInTest:
		return "suppressed_in_test"
	case outcomeBadURL:
		return "bad_url"
	case outcomeTimedOut:
		return "timed_out"
	case outcomeReadFailed:
		return "read_failed"
	default:
		return "unknown"
	}
}

// browserCookiesForURL reads cookies from the user's browsers matching the
// given URL's domain. Iterates all registered browser cookie stores and
// returns every cookie whose domain matches the URL host (or is a parent
// domain of it).
//
// Returns nil for any of the outcomes above. Callers that need to tell them
// apart use browserCookiesForURLWithOutcome; this wrapper reports them instead,
// with outcomeReadFailed at warn level because a fallback that cannot work is
// something the user has to be able to see (trvl#529, TRVL.KOOKY.2).
//
// This is used as a fallback when standard HTTP preflight gets blocked by
// JavaScript bot-detection challenges (HTTP 202/403). The user's actual
// browser has already solved any JS challenges and has valid session
// cookies, which we can read directly from their disk-backed cookie jars.
func browserCookiesForURL(targetURL string) []*http.Cookie {
	out, outcome, readErr := browserCookiesForURLWithOutcome(targetURL)
	reportOutcome(targetURL, outcome, readErr)
	return out
}

// reportOutcome says, at a level matching how much the user can do about it,
// why a browser-cookie read produced what it did.
//
// Separated from the read so a test can drive every branch directly. The
// alternative -- provoking a real permissions denial or a real timeout on a CI
// runner -- is the "generate the condition and hope" testing that leaves
// branches unverified, which is how this package shipped a warning nobody had
// ever seen fire.
func reportOutcome(targetURL string, outcome browserCookieOutcome, readErr error) {
	switch outcome {
	case outcomeReadFailed:
		// The only outcome a user can act on, and the only one where staying
		// silent is a lie: the site fallback is unavailable, not empty.
		//
		// The underlying error is logged rather than summarised. The hint names
		// the causes we know of, but this branch fires on ANY read error, and a
		// message that asserts a cause it has not established is how #529 was
		// filed on a misdiagnosis. The hint suggests; the error says.
		slog.Warn("browser cookie fallback unavailable: a cookie store was found but could not be read",
			"host", hostForLog(targetURL),
			"err", logredact.Err(readErr),
			"hint", "often a permissions prompt that was denied, or a browser storing values this build cannot decrypt; the error above is authoritative")
	case outcomeTimedOut:
		// Deliberately does NOT mention permissions or decryption. A timeout
		// says nothing about whether the store is readable, and advice about
		// permissions would send the user to fix something that is not broken.
		slog.Warn("browser cookie lookup timed out",
			"host", hostForLog(targetURL),
			"timeout", browserCookieLookupTimeout,
			"err", logredact.Err(readErr),
			"hint", "the cookie stores may be large or the disk busy; this does not indicate a permissions problem")
	case outcomeBadURL:
		// The URL is reduced to a fingerprint like every other logged URL: a
		// target URL carries the user's journey (origin, destination, dates,
		// passengers) in its query string, and being unparseable does not make
		// it less sensitive. The parse error is what actually diagnoses this.
		slog.Warn("browser cookie lookup skipped: unusable target URL",
			"url", logredact.URL(targetURL), "err", logredact.Err(readErr))
	case outcomeSuppressedInTest:
		slog.Debug("browser cookie lookup skipped: test binary",
			"host", hostForLog(targetURL), "override", "TRVL_ALLOW_BROWSER_COOKIES=1")
	case outcomeDeclined:
		slog.Debug("browser cookie lookup skipped: the user has declined browser access",
			"host", hostForLog(targetURL), "env", cookies.DisableEnv)
	case outcomeNoMatch:
		slog.Debug("no browser cookies for host", "host", hostForLog(targetURL))
	case outcomeFound:
	case outcomeUnknown:
		// Unreachable today: every return path names an outcome. Logged rather
		// than ignored because the zero value reaching here means a new branch
		// forgot to say what happened, which is the defect #529 was filed on.
		slog.Debug("browser cookie lookup returned no outcome", "host", hostForLog(targetURL))
	}
}

// hostForLog returns just the host of a URL for logging. Full URLs can carry
// query parameters, and a cookie-related log line is the wrong place to risk
// echoing one.
func hostForLog(targetURL string) string {
	u, err := url.Parse(targetURL)
	if err != nil || u.Host == "" {
		return "?"
	}
	return u.Hostname()
}
