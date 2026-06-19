package testutil

import (
	"strings"
	"testing"
)

// transientMarkers are substrings that identify an error as transient network
// noise rather than a genuine provider regression. These come up nightly when
// the CI runner's datacenter IP gets throttled by no-key providers (Google
// Flights/Hotels return HTTP 429; Skiplagged's origin returns 429/502). A
// rate-limited night is noise: it must not red the Live Probes workflow,
// otherwise the signal we actually care about (a rotated API, a re-stubbed
// checker, a real format change) drowns in flapping.
//
// The classification is deliberately conservative: only well-known throttle and
// transient-gateway markers are matched, plus the context-deadline that follows
// the batchexec client's internal 429 retry/backoff loop. A 200 response with an
// unexpected body shape ("unexpected flight data format") is NOT transient and
// still fails — that is real drift worth surfacing.
var transientMarkers = []string{
	"429",
	"rate limited",
	"too many requests",
	"unexpected status 429",
	"bad gateway",
	"502",
	"context deadline exceeded",
	"timeout",
	"connection reset",
}

// IsTransientProbeErr reports whether err looks like transient network noise
// (provider throttling or a transient gateway/timeout) rather than a real
// provider regression.
func IsTransientProbeErr(err error) bool {
	if err == nil {
		return false
	}
	return IsTransientMsg(err.Error())
}

// IsTransientMsg is the string form of IsTransientProbeErr, for callers that
// carry a failure reason as a plain string (e.g. a result.Error field) rather
// than an error value.
func IsTransientMsg(msg string) bool {
	msg = strings.ToLower(msg)
	for _, m := range transientMarkers {
		if strings.Contains(msg, m) {
			return true
		}
	}
	return false
}

// SkipIfTransient skips the test when err is transient network noise, and is a
// no-op when err is nil. Live probes call this on a provider error so a throttled
// nightly run skips (yellow) instead of failing (red). Real regressions — which
// surface as non-transient errors — fall through to the caller's own assertions.
//
// Usage:
//
//	out, err := SearchFlights(ctx, ...)
//	testutil.SkipIfTransient(t, err)        // skips on 429/timeout noise
//	if err != nil { t.Fatalf("...", err) }  // real failure still fails
func SkipIfTransient(t *testing.T, err error) {
	t.Helper()
	if IsTransientProbeErr(err) {
		t.Skipf("skipping: transient provider noise (not a regression): %v", err)
	}
}
