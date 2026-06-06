package providers

import "strings"

// FixHintCode is a typed enum identifying the root cause of a provider failure.
// It is surfaced in MCP search responses and the health log so an orchestrating
// LLM can autonomously diagnose and repair broken providers.
type FixHintCode string

const (
	FixHintAkamaiBlock           FixHintCode = "AKAMAI_BLOCK"
	FixHintDNSFail               FixHintCode = "DNS_FAIL"
	FixHintTLSTimeout            FixHintCode = "TLS_TIMEOUT"
	FixHintCookieExpired         FixHintCode = "COOKIE_EXPIRED"
	FixHintBrowserCookiesMissing FixHintCode = "BOOKING_COOKIES_MISSING"
	FixHintRateLimited           FixHintCode = "RATE_LIMITED"
	FixHintResponseShapeChanged  FixHintCode = "RESPONSE_SHAPE_CHANGED"
	FixHintPreflightFailed       FixHintCode = "PREFLIGHT_FAILED"
	FixHintUnclassified          FixHintCode = "UNCLASSIFIED"
)

// classifyProviderError maps a provider error to a typed FixHintCode and a
// one-line, LLM-readable hint with a documented remediation step.
// It is a pure function — no I/O, safe to call from any goroutine.
//
// Ordering matters: more specific patterns are checked before broader ones so
// that ambiguous errors (e.g. a 403 that also contains "preflight") are
// assigned the most actionable code.
func classifyProviderError(err error) (FixHintCode, string) {
	if err == nil {
		return FixHintUnclassified, "No error — check caller logic."
	}
	msg := strings.ToLower(err.Error())

	switch {
	// Missing browser cookies beats the generic cookie-expired branch: the
	// user has no usable browser session at all (vs. a stale/expired one).
	// kooky auto-detects cookies from an installed browser, so the fix is to
	// have a logged-in browser available rather than to re-import.
	case strings.Contains(msg, "browser cookies missing") ||
		strings.Contains(msg, "no browser cookies"):
		return FixHintBrowserCookiesMissing,
			"No browser cookies found — open and log in to Booking.com in an installed browser (Chrome/Firefox/Safari/Edge); trvl auto-detects them via kooky on the next search."

	// Rate-limit beats WAF: a 429 is more specific than a generic 403.
	case strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "429") ||
		strings.Contains(msg, "too many requests"):
		return FixHintRateLimited,
			"Rate limited — wait before retrying, or reduce request frequency in provider config."

	// WAF / Akamai block (403, 202 challenge, or explicit "akamai" token).
	case strings.Contains(msg, "akamai") ||
		strings.Contains(msg, "http 403") ||
		strings.Contains(msg, "http 202") ||
		strings.Contains(msg, "403 forbidden") ||
		strings.Contains(msg, "access denied"):
		return FixHintAkamaiBlock,
			"WAF/Akamai block detected — call test_provider; if it fails, refresh browser cookies via configure_provider."

	// Cookie auth failure (expired session / CSRF token mismatch).
	case strings.Contains(msg, "cookie") ||
		strings.Contains(msg, "csrf") ||
		strings.Contains(msg, "401") ||
		strings.Contains(msg, "unauthorized"):
		return FixHintCookieExpired,
			"Session cookie expired — call configure_provider to re-import fresh browser cookies."

	// Preflight step failed (URL construction or WAF during auth phase).
	case strings.Contains(msg, "preflight"):
		return FixHintPreflightFailed,
			"Preflight auth step failed — call test_provider to diagnose; WAF may need a cookie refresh."

	// Response shape mismatch (results_path or JSON extraction error).
	case strings.Contains(msg, "results_path") ||
		strings.Contains(msg, "response shape") ||
		strings.Contains(msg, "unmarshal") ||
		strings.Contains(msg, "unexpected end of json"):
		return FixHintResponseShapeChanged,
			"API response structure changed — call test_provider to inspect the current response, then update results_path via configure_provider."

	// TLS handshake / connection timeout / network-layer failure.
	// DNS is checked here first (subset of network errors).
	case strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "dns") ||
		strings.Contains(msg, "lookup"):
		return FixHintDNSFail,
			"DNS resolution failed — verify the provider base_url is correct and network connectivity is intact."

	case strings.Contains(msg, "tls") ||
		strings.Contains(msg, "handshake") ||
		strings.Contains(msg, "deadline exceeded") ||
		strings.Contains(msg, "context deadline") ||
		strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "connection refused"):
		return FixHintTLSTimeout,
			"TLS/connection timeout — check network connectivity; the provider endpoint may be temporarily unreachable."

	default:
		return FixHintUnclassified,
			"Call test_provider with this provider's id to diagnose the issue."
	}
}
