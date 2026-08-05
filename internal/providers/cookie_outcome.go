package providers

import (
	"log/slog"
	"net/http"
	"net/url"

	"github.com/MikkoParkkola/trvl/internal/cookies"
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
	// outcomeFound: cookies matched and are being returned.
	outcomeFound browserCookieOutcome = iota
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
	out, outcome := browserCookiesForURLWithOutcome(targetURL)

	switch outcome {
	case outcomeReadFailed:
		// The only outcome a user can act on, and the only one where staying
		// silent is a lie: the site fallback is unavailable, not empty.
		slog.Warn("browser cookie fallback unavailable: a cookie store was found but could not be read",
			"host", hostForLog(targetURL),
			"hint", "grant Keychain access, or the browser may use app-bound encryption this build cannot read")
	case outcomeBadURL:
		slog.Warn("browser cookie lookup skipped: unusable target URL", "url", targetURL)
	case outcomeSuppressedInTest:
		slog.Debug("browser cookie lookup skipped: test binary",
			"host", hostForLog(targetURL), "override", "TRVL_ALLOW_BROWSER_COOKIES=1")
	case outcomeDeclined:
		slog.Debug("browser cookie lookup skipped: the user has declined browser access",
			"host", hostForLog(targetURL), "env", cookies.DisableEnv)
	case outcomeNoMatch:
		slog.Debug("no browser cookies for host", "host", hostForLog(targetURL))
	case outcomeFound:
	}
	return out
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

// browserCookiesForURLWithOutcome is browserCookiesForURL plus the reason.
