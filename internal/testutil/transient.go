// Package testutil holds shared HTTP fixtures and live-probe helpers for tests.
package testutil

import (
	"errors"
	"math/rand/v2"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// transientMarkers are substrings that identify an error as transient network
// noise rather than a genuine provider regression. These come up nightly when
// the CI runner's datacenter IP gets throttled or edge-blocked by no-key
// providers (Google Flights/Hotels return HTTP 429; Skiplagged's origin returns
// 429/502; HomeToGo and Wizz's CloudFront/Akamai edge return HTTP 403 to
// datacenter IPs). A rate-limited or edge-blocked night is noise: it must not
// red the Live Probes workflow, otherwise the signal we actually care about (a
// rotated API path, a re-stubbed checker, a real format change) drowns in
// flapping.
//
// The classification is deliberately conservative: only well-known throttle,
// edge-block, and transient-gateway markers are matched, plus the
// context-deadline that follows the batchexec client's internal 429 retry/backoff
// loop. A 200 response with an unexpected body shape ("unexpected flight data
// format") and a rotated version path (404 / "version path rotated") are NOT
// transient and still fail — those are real drift worth surfacing.
var transientMarkers = []string{
	"429",
	"rate limited",
	"rate-limited",
	"quota refusal",
	"cooling down",
	"cooldown",
	"temporarily unavailable",
	"too many requests",
	"unexpected status 429",
	"bad gateway",
	"502",
	"403",
	"forbidden",
	"akamai_block",
	"edge-blocked",
	"context deadline exceeded",
	"timeout",
	"connection reset",
}

// IsTransientProbeErr reports whether err looks like transient network noise
// (provider throttling or a transient gateway/timeout) rather than a real
// provider regression.
//
// Typed sentinels are checked first so classification survives a reworded
// error message: models.ErrRateLimited (wrapped by every Google Flights
// quota-refusal / cooldown error, see internal/flights/google_errors.go) and
// batchexec.ErrRefused (raw client call skipped during a cooldown). The text
// markers remain as a fallback for providers that only surface strings.
func IsTransientProbeErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, models.ErrRateLimited) || errors.Is(err, batchexec.ErrRefused) {
		return true
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

// ProbeRetryDelay returns the jittered wait before the single retry that
// RetryTransient performs. It defaults to 20-60s, which outlasts a short
// throttle burst without hammering the provider. TRVL_PROBE_RETRY_DELAY (a Go
// duration such as "0s" or "5s") overrides it with a fixed delay. It is a
// variable so unit tests can make it instant.
var ProbeRetryDelay = func() time.Duration {
	if v := os.Getenv("TRVL_PROBE_RETRY_DELAY"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return 20*time.Second + rand.N(40*time.Second) // #nosec G404 -- retry jitter for live probes, not a security value
}

// RetryTransient runs fn and, when it fails with transient provider noise,
// waits ProbeRetryDelay() and runs it exactly once more. It returns the last
// error. A non-transient error is returned immediately without a retry, so
// real drift still surfaces on the first attempt.
func RetryTransient(t testing.TB, fn func() error) error {
	t.Helper()
	err := fn()
	if !IsTransientProbeErr(err) {
		return err
	}
	d := ProbeRetryDelay()
	t.Logf("transient provider error, retrying once in %s: %v", d.Round(time.Second), err)
	time.Sleep(d)
	return fn()
}
