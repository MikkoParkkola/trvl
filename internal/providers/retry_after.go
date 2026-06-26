package providers

// MIK-3071: parse the HTTP Retry-After header and provide a small helper
// that bounds the result to a sensible range. Two forms are supported per
// RFC 7231 §7.1.3:
//
//	delta-seconds:  "120"
//	HTTP-date:      "Wed, 21 Oct 2026 07:28:00 GMT"
//
// Anything else (empty, malformed, in the past) returns 0.

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"
)

const (
	// retryAfterMaxDelay caps how long we will sleep on a single Retry-After
	// hint. Some providers return absurdly long values (hours, days) that we
	// should NOT honour blindly inside a single MCP tool call. 60s gives the
	// caller a chance to recover within the typical 90s MCP client deadline.
	retryAfterMaxDelay = 60 * time.Second

	// retryAfterDefaultDelay is used when the response carried a 429 status
	// but no usable Retry-After header. Conservative enough to give the
	// provider time to forget us; short enough to fit the retry budget.
	retryAfterDefaultDelay = 2 * time.Second

	// rateLimitConsecutiveThreshold is the number of consecutive 429s after
	// which the provider's adaptive rate limiter halves its rps.
	rateLimitConsecutiveThreshold = 3

	// rateLimitCooldown is the no-429 window after which the adaptive
	// limiter resets to the provider's configured default rps.
	rateLimitCooldown = 1 * time.Hour

	// rateLimitFloorRPS is the lowest rate we will throttle a provider down
	// to under repeated 429s. Below this the searches become useless.
	rateLimitFloorRPS = 0.05
)

// ParseRetryAfter inspects the value of an HTTP Retry-After header and
// returns the duration the caller should sleep before retrying. `now` is
// injected for deterministic testing of the HTTP-date form.
//
// This is the single shared Retry-After parser for the codebase (MIK-3071,
// #357 TRVL-RETRY.1); providers honour 429 backoff through it rather than
// re-implementing RFC 7231 §7.1.3 parsing per provider.
//
// Behaviour:
//   - empty or whitespace-only → 0 (caller falls back to default)
//   - integer seconds → that many seconds, capped at retryAfterMaxDelay
//   - HTTP-date in the future → time until that date, capped
//   - HTTP-date in the past → 0
//   - any other input → 0
func ParseRetryAfter(value string, now time.Time) time.Duration {
	v := strings.TrimSpace(value)
	if v == "" {
		return 0
	}

	// delta-seconds form first — cheaper to check.
	if secs, err := strconv.Atoi(v); err == nil {
		if secs <= 0 {
			return 0
		}
		d := time.Duration(secs) * time.Second
		if d > retryAfterMaxDelay {
			return retryAfterMaxDelay
		}
		return d
	}

	// HTTP-date form. http.ParseTime accepts RFC 1123, RFC 850, and the
	// asctime forms required by RFC 7231.
	t, err := http.ParseTime(v)
	if err != nil {
		return 0
	}
	d := t.Sub(now)
	if d <= 0 {
		return 0
	}
	if d > retryAfterMaxDelay {
		return retryAfterMaxDelay
	}
	return d
}

// RetryAfterOrDefault returns ParseRetryAfter's result if non-zero,
// otherwise retryAfterDefaultDelay. Convenience wrapper for the retry
// loop in the search request path.
func RetryAfterOrDefault(value string, now time.Time) time.Duration {
	d := ParseRetryAfter(value, now)
	if d <= 0 {
		return retryAfterDefaultDelay
	}
	return d
}

// recordRateLimit records a 429 response for this provider. Every
// rateLimitConsecutiveThreshold consecutive 429s halves the limiter rate,
// but never below rateLimitFloorRPS.
func (pc *providerClient) recordRateLimit(now time.Time) {
	pc.rl429Mu.Lock()
	defer pc.rl429Mu.Unlock()
	pc.consecutive429++
	pc.last429 = now
	if pc.consecutive429%rateLimitConsecutiveThreshold == 0 {
		current := float64(pc.limiter.Limit())
		next := current / 2.0
		if next < rateLimitFloorRPS {
			next = rateLimitFloorRPS
		}
		pc.limiter.SetLimit(rate.Limit(next))
	}
}

// recordRateLimitSuccess records a successful (non-429) response. It resets
// the consecutive counter and, if rateLimitCooldown has elapsed since the last
// 429, restores the limiter to the provider's configured default rate.
func (pc *providerClient) recordRateLimitSuccess(now time.Time) {
	pc.rl429Mu.Lock()
	defer pc.rl429Mu.Unlock()
	pc.consecutive429 = 0
	if !pc.last429.IsZero() && now.Sub(pc.last429) >= rateLimitCooldown {
		pc.limiter.SetLimit(rate.Limit(pc.defaultRPS))
		pc.last429 = time.Time{}
	}
}
