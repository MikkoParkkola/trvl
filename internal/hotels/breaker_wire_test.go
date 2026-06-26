package hotels

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/breaker"
)

// TestHotelBreakerTripsAtThreshold proves the package-level hotelBreaker the
// scraper fan-out shares trips after DefaultThreshold consecutive failures, so
// a persistently-failing provider is skipped on the next search instead of
// being hammered. It exercises the same Breaker instance runAux consults.
func TestHotelBreakerTripsAtThreshold(t *testing.T) {
	const key = "breaker-wire-test-provider"
	// Start from a clean slate in case another test touched this key.
	hotelBreaker.RecordSuccess(key)

	for i := 0; i < breaker.DefaultThreshold-1; i++ {
		hotelBreaker.RecordFailure(key)
		if hotelBreaker.Tripped(key) {
			t.Fatalf("breaker tripped after %d failures, want open only at threshold %d", i+1, breaker.DefaultThreshold)
		}
	}
	hotelBreaker.RecordFailure(key) // threshold-th failure trips it
	if !hotelBreaker.Tripped(key) {
		t.Fatalf("breaker should be tripped after %d consecutive failures", breaker.DefaultThreshold)
	}

	// A success closes it again so the provider is retried.
	hotelBreaker.RecordSuccess(key)
	if hotelBreaker.Tripped(key) {
		t.Fatal("breaker should be closed after a successful probe")
	}
}
