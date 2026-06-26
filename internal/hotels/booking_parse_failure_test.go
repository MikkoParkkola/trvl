package hotels

import (
	"errors"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestBookingParseFailure pins the discriminator that distinguishes a
// rotated/undecodable Booking.com Apollo response (result entries present, none
// decoded) from a genuine empty result (no entries) and a normal partial parse.
// The genuine-empty case must NOT be reported as a failure, or an honest no-hit
// gets misrendered as a broken provider.
func TestBookingParseFailure(t *testing.T) {
	cases := []struct {
		name       string
		parsed     int
		rawResults int
		wantParse  bool
	}{
		{"no result entries is a healthy empty result", 0, 0, false},
		{"all entries decoded", 5, 5, false},
		{"some entries dropped but not all", 3, 5, false},
		{"entries present but none decoded is a parse failure", 0, 5, true},
		{"single undecodable entry is a parse failure", 0, 1, true},
	}
	for _, c := range cases {
		err := bookingParseFailure(c.parsed, c.rawResults)
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
