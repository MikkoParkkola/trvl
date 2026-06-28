package flights

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/breaker"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestFlightBreakerTripsAtThreshold proves the package-level flightBreaker the
// secondary-provider fan-out shares trips after DefaultThreshold consecutive
// failures, so a persistently-failing provider is skipped on the next search
// instead of being retried. It exercises the same Breaker instance
// runProviderTasks consults via Tripped/RecordFailure/RecordSuccess.
func TestFlightBreakerTripsAtThreshold(t *testing.T) {
	const key = "flight-breaker-wire-test-provider"
	// Start from a clean slate in case another test touched this key.
	flightBreaker.RecordSuccess(key)

	for i := 0; i < breaker.DefaultThreshold-1; i++ {
		flightBreaker.RecordFailure(key)
		if flightBreaker.Tripped(key) {
			t.Fatalf("breaker tripped after %d failures, want open only at threshold %d", i+1, breaker.DefaultThreshold)
		}
	}
	flightBreaker.RecordFailure(key) // threshold-th failure trips it
	if !flightBreaker.Tripped(key) {
		t.Fatalf("breaker should be tripped after %d consecutive failures", breaker.DefaultThreshold)
	}

	// A success closes it again so the provider is retried.
	flightBreaker.RecordSuccess(key)
	if flightBreaker.Tripped(key) {
		t.Fatal("breaker should be closed after a successful probe")
	}
}

// TestOutcomeFailed pins the breaker's failure discriminator: errors and
// failure-class statuses count as failures; empty-but-healthy and
// not-configured responses do not, so a provider with no flights is not
// penalised.
func TestOutcomeFailed(t *testing.T) {
	cases := []struct {
		name string
		out  providerOutcome
		want bool
	}{
		{"healthy with flights", providerOutcome{succeeded: true, status: models.ProviderStatus{Status: models.StatusOK}}, false},
		{"healthy but empty", providerOutcome{status: models.ProviderStatus{Status: models.StatusOK}}, false},
		{"not configured", providerOutcome{status: models.ProviderStatus{Status: models.StatusNotConfigured}}, false},
		{"explicit error", providerOutcome{err: errTest, status: models.ProviderStatus{Status: models.StatusFailed}}, true},
		{"failed status no err", providerOutcome{status: models.ProviderStatus{Status: models.StatusFailed}}, true},
		{"timeout status no err", providerOutcome{status: models.ProviderStatus{Status: models.StatusTimeout}}, true},
		{"rate limited", providerOutcome{status: models.ProviderStatus{Status: models.StatusRateLimited}}, true},
	}
	for _, c := range cases {
		if got := outcomeFailed(c.out); got != c.want {
			t.Errorf("%s: outcomeFailed = %v, want %v", c.name, got, c.want)
		}
	}
}

func resetFlightBreakerForTest(t *testing.T, keys ...string) {
	t.Helper()
	for _, key := range keys {
		flightBreaker.RecordSuccess(key)
	}
	t.Cleanup(func() {
		for _, key := range keys {
			flightBreaker.RecordSuccess(key)
		}
	})
}

var errTest = errProbe("boom")

type errProbe string

func (e errProbe) Error() string { return string(e) }
