package flights

import (
	"errors"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestParseFailureIfAllDropped pins the discriminator that distinguishes a
// rotated/undecodable Google Flights response (entries present, none parsed)
// from a genuine empty result (no entries) and a normal partial parse.
func TestParseFailureIfAllDropped(t *testing.T) {
	cases := []struct {
		name      string
		parsed    int
		rawCount  int
		wantParse bool
	}{
		{"no entries is a healthy empty result", 0, 0, false},
		{"all entries parsed", 5, 5, false},
		{"some entries dropped but not all", 3, 5, false},
		{"entries present but none parsed is a parse failure", 0, 5, true},
		{"single unparseable entry is a parse failure", 0, 1, true},
	}
	for _, c := range cases {
		err := parseFailureIfAllDropped(c.parsed, c.rawCount)
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
		} else if err != nil {
			t.Errorf("%s: want nil, got %q", c.name, err)
		}
	}
}
