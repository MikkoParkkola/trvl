package hotels

import (
	"errors"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestHotelParseFailure pins the discriminator that distinguishes a
// rotated/undecodable Google Hotels response (Google's metadata claims hotels
// exist, none decoded) from a genuine empty result (no hotels available) and a
// normal partial parse. The genuine-empty case must NOT be reported as a
// failure, or an honest no-hit gets misrendered as a broken provider.
func TestHotelParseFailure(t *testing.T) {
	cases := []struct {
		name           string
		parsed         int
		totalAvailable int
		wantParse      bool
	}{
		{"no hotels and none available is a healthy empty result", 0, 0, false},
		{"all available hotels parsed", 5, 5, false},
		{"some hotels parsed of many available", 3, 5, false},
		{"hotels available but none decoded is a parse failure", 0, 5, true},
		{"single available hotel undecoded is a parse failure", 0, 1, true},
	}
	for _, c := range cases {
		err := hotelParseFailure(c.parsed, c.totalAvailable)
		if c.wantParse {
			if err == nil {
				t.Errorf("%s: want parse failure, got nil", c.name)
				continue
			}
			if !errors.Is(err, models.ErrParseFailed) {
				t.Errorf("%s: error %q does not wrap ErrParseFailed", c.name, err)
			}
			// A clean message routes through ClassifyProviderError to StatusFailed,
			// not a rate-limit/timeout misclassification.
			if got := models.ClassifyProviderError(err); got != models.StatusFailed {
				t.Errorf("%s: ClassifyProviderError = %q, want %q", c.name, got, models.StatusFailed)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: want nil, got %q", c.name, err)
		}
	}
}
