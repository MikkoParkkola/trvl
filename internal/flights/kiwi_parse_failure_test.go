package flights

import (
	"errors"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestKiwiParseFailureIfAllDropped pins the discriminator that distinguishes a
// rotated/undecodable Kiwi response (itinerary entries present, none carried a
// usable route) from a genuine empty result (no entries) and a normal partial
// parse. The all-dropped case must wrap ErrParseFailed so the circuit breaker
// counts a broken decoder instead of treating it as a route with no flights.
func TestKiwiParseFailureIfAllDropped(t *testing.T) {
	cases := []struct {
		name      string
		usable    int
		rawCount  int
		wantParse bool
	}{
		{"no entries is a healthy empty result", 0, 0, false},
		{"all entries usable", 5, 5, false},
		{"some entries dropped but not all", 3, 5, false},
		{"entries present but none usable is a parse failure", 0, 5, true},
		{"single unusable entry is a parse failure", 0, 1, true},
	}
	for _, c := range cases {
		err := kiwiParseFailureIfAllDropped(c.usable, c.rawCount)
		if c.wantParse {
			if err == nil {
				t.Errorf("%s: want parse failure, got nil", c.name)
				continue
			}
			if !errors.Is(err, models.ErrParseFailed) {
				t.Errorf("%s: error %q does not wrap ErrParseFailed", c.name, err)
			}
			if got := models.ClassifyProviderError(err); got != models.StatusFailed {
				t.Errorf("%s: ClassifyProviderError = %q, want %q", c.name, got, models.StatusFailed)
			}
		} else if err != nil {
			t.Errorf("%s: want nil, got %q", c.name, err)
		}
	}
}
