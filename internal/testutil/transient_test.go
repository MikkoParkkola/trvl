package testutil

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/models"
)

func TestIsTransientMsg(t *testing.T) {
	transient := []string{
		"HTTP 429 Too Many Requests",
		"rate limited by provider",
		"upstream returned 502 bad gateway",
		"context deadline exceeded",
		"i/o timeout",
		"connection reset by peer",
		"hometogo resolve: unexpected status 403 for https://www.hometogo.com/berlin/",
		"HTTP 403 Forbidden",
		"wizzair: status 403 (edge-blocked, body=\"...\")",
		"provider blocked: AKAMAI_BLOCK",
		// Google Flights throttling of CI runner IPs (trvl#720).
		"google flights quota refusal (code 13): upstream provider rate-limited or temporarily unavailable",
		"google flights cooling down: upstream provider rate-limited or temporarily unavailable",
		"upstream provider rate-limited",
		"service temporarily unavailable",
	}
	for _, m := range transient {
		if !IsTransientMsg(m) {
			t.Errorf("IsTransientMsg(%q) = false, want true", m)
		}
	}
	// The whole point of the gate: a real provider-format drift must NOT be
	// swallowed as transient, or the nightly would silently green on breakage.
	nonTransient := []string{
		"extract flights: unexpected flight data format",
		"apollo store not found in page",
		"",
		"invalid IATA code",
	}
	for _, m := range nonTransient {
		if IsTransientMsg(m) {
			t.Errorf("IsTransientMsg(%q) = true, want false (real failure must surface)", m)
		}
	}
}

func TestIsTransientProbeErr(t *testing.T) {
	if !IsTransientProbeErr(errors.New("got status 429")) {
		t.Error("429 error should be transient")
	}
	if IsTransientProbeErr(errors.New("unexpected flight data format")) {
		t.Error("format-drift error must NOT be transient")
	}
	if IsTransientProbeErr(nil) {
		t.Error("nil error is not transient")
	}
}

func TestSkipIfTransient_NilIsNoop(t *testing.T) {
	// Must not skip (or panic) when there is no error.
	SkipIfTransient(t, nil)
}

// Typed sentinels classify as transient even when the message text would not
// match any marker, so rewording an error cannot silently re-red the nightly.
func TestIsTransientProbeErr_Sentinels(t *testing.T) {
	cases := []error{
		models.ErrRateLimited,
		fmt.Errorf("some reworded wrapper: %w", models.ErrRateLimited),
		batchexec.ErrRefused,
		fmt.Errorf("probe: %w", batchexec.ErrRefused),
	}
	for _, err := range cases {
		if !IsTransientProbeErr(err) {
			t.Errorf("IsTransientProbeErr(%v) = false, want true", err)
		}
	}
}

func TestRetryTransient(t *testing.T) {
	orig := ProbeRetryDelay
	ProbeRetryDelay = func() time.Duration { return 0 }
	defer func() { ProbeRetryDelay = orig }()

	// Transient then success: exactly one retry, nil result.
	calls := 0
	err := RetryTransient(t, func() error {
		calls++
		if calls == 1 {
			return models.ErrRateLimited
		}
		return nil
	})
	if err != nil || calls != 2 {
		t.Errorf("transient-then-ok: err=%v calls=%d, want nil/2", err, calls)
	}

	// Persistently transient: still only two attempts, transient error returned.
	calls = 0
	err = RetryTransient(t, func() error { calls++; return models.ErrRateLimited })
	if !errors.Is(err, models.ErrRateLimited) || calls != 2 {
		t.Errorf("always-transient: err=%v calls=%d, want ErrRateLimited/2", err, calls)
	}

	// Real failure: no retry.
	calls = 0
	drift := errors.New("unexpected flight data format")
	err = RetryTransient(t, func() error { calls++; return drift })
	if err != drift || calls != 1 {
		t.Errorf("non-transient: err=%v calls=%d, want drift/1", err, calls)
	}
}
