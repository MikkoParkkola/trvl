package ground

import (
	"errors"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/breaker"
)

// TestGroundBreakerTripsAtThreshold proves the package-level groundBreaker the
// provider fan-out shares trips after DefaultThreshold consecutive failures, so
// a persistently-failing provider is skipped on the next search instead of
// being retried. It exercises the same Breaker instance launchProvider consults
// via Tripped/RecordFailure/RecordSuccess.
func TestGroundBreakerTripsAtThreshold(t *testing.T) {
	const key = "ground-breaker-wire-test-provider"
	// Start from a clean slate in case another test touched this key.
	groundBreaker.RecordSuccess(key)

	for i := 0; i < breaker.DefaultThreshold-1; i++ {
		groundBreaker.RecordFailure(key)
		if groundBreaker.Tripped(key) {
			t.Fatalf("breaker tripped after %d failures, want open only at threshold %d", i+1, breaker.DefaultThreshold)
		}
	}
	groundBreaker.RecordFailure(key) // threshold-th failure trips it
	if !groundBreaker.Tripped(key) {
		t.Fatalf("breaker should be tripped after %d consecutive failures", breaker.DefaultThreshold)
	}

	// A success closes it again so the provider is retried.
	groundBreaker.RecordSuccess(key)
	if groundBreaker.Tripped(key) {
		t.Fatal("breaker should be closed after a successful probe")
	}
}

// TestErrCircuitBrokenIsDistinct guards the sentinel the aggregator matches with
// errors.Is: it must be its own error, not collide with the not-applicable
// classification, so a breaker skip is reported as circuit_broken rather than a
// silent "no route" skip.
func TestErrCircuitBrokenIsDistinct(t *testing.T) {
	if errCircuitBroken == nil {
		t.Fatal("errCircuitBroken sentinel must be non-nil")
	}
	if isProviderNotApplicable(errCircuitBroken) {
		t.Fatal("a circuit-broken skip must not be classified as not-applicable")
	}
	if !errors.Is(errCircuitBroken, errCircuitBroken) {
		t.Fatal("errors.Is must match the sentinel against itself")
	}
}
