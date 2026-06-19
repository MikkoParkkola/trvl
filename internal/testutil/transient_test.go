package testutil

import (
	"errors"
	"testing"
)

func TestIsTransientMsg(t *testing.T) {
	transient := []string{
		"HTTP 429 Too Many Requests",
		"rate limited by provider",
		"upstream returned 502 bad gateway",
		"context deadline exceeded",
		"i/o timeout",
		"connection reset by peer",
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
