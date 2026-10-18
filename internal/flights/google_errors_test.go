package flights

import (
	"errors"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/testutil"
)

// The Google throttle errors seen on CI runners (trvl#720) must carry the
// typed models.ErrRateLimited sentinel so probes classify them with errors.Is
// rather than by message text, and the live-probe gate must treat them as
// transient noise instead of wire-format drift.
func TestGoogleThrottleErrorsWrapSentinel(t *testing.T) {
	cases := map[string]error{
		"quota refusal code 13": googleWrbError(13),
		"cooling down":          googleRequestError(batchexec.ErrRefused),
	}
	for name, err := range cases {
		if !errors.Is(err, models.ErrRateLimited) {
			t.Errorf("%s: %v does not wrap models.ErrRateLimited", name, err)
		}
		if !testutil.IsTransientProbeErr(err) {
			t.Errorf("%s: %v not classified transient by the probe gate", name, err)
		}
	}
	if msg := googleWrbError(13).Error(); !strings.Contains(msg, "quota refusal (code 13)") {
		t.Errorf("code 13 message changed: %q", msg)
	}
}

// A transport failure that is not a cooldown is not a throttle and must not be
// wrapped as one, so it still fails the probe.
func TestGoogleRequestErrorTransportNotSentinel(t *testing.T) {
	err := googleRequestError(errors.New("tls: handshake failure"))
	if errors.Is(err, models.ErrRateLimited) {
		t.Errorf("plain transport error wrapped as rate-limited: %v", err)
	}
}
