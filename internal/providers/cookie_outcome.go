package providers

import (
	"log/slog"
	"net/http"

	"github.com/MikkoParkkola/trvl/internal/cookies"
	"github.com/MikkoParkkola/trvl/internal/logredact"
)

// browserCookieOutcome names why a browser-cookie read produced what it did.
// A fallback that cannot work must say so instead of looking like an empty jar.
type browserCookieOutcome int

const (
	outcomeFound browserCookieOutcome = iota
	outcomeNoMatch
	outcomeDeclined
	outcomeSuppressedInTest
	outcomeBadURL
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

// browserCookiesForURL preserves the legacy nil-or-cookies API while reporting
// why a read did not return cookies. Callers that need the reason directly use
// browserCookiesForURLWithOutcome.
func browserCookiesForURL(targetURL string) []*http.Cookie {
	out, outcome := browserCookiesForURLWithOutcome(targetURL)

	switch outcome {
	case outcomeReadFailed:
		slog.Warn("browser cookie fallback unavailable: a cookie store was found but could not be read",
			"url", logredact.URL(targetURL),
			"hint", "grant Keychain access, or the browser may use app-bound encryption this build cannot read")
	case outcomeBadURL:
		slog.Warn("browser cookie lookup skipped: unusable target URL", "url", logredact.URL(targetURL))
	case outcomeSuppressedInTest:
		slog.Debug("browser cookie lookup skipped: test binary",
			"url", logredact.URL(targetURL), "override", "TRVL_ALLOW_BROWSER_COOKIES=1")
	case outcomeDeclined:
		slog.Debug("browser cookie lookup skipped: the user has declined browser access",
			"url", logredact.URL(targetURL), "env", cookies.DisableEnv)
	case outcomeNoMatch:
		slog.Debug("no browser cookies for URL", "url", logredact.URL(targetURL))
	case outcomeFound:
	}
	return out
}
