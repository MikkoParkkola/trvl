package ground

import (
	"context"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/cookies"
)

// TestRailProvidersHonourTheBrowserCookieOptOut pins the control on the path each
// rail provider actually uses, one case per provider.
//
// The gate itself lives in the cookies package and is tested there. This exists
// because the guarantee a user cares about is not "the gate works" but "no rail
// search reads my cookie store", and those are only the same claim while every
// provider still goes through the gated function. A provider rewired to its own
// extractor — a plausible change when one operator's challenge needs different
// handling — would leave the cookies-package tests green while quietly reading
// the store again. Calling each provider's own extractor variable is what makes
// that regression fail here.
//
// Timed for the same reason as the gate's own test: an empty return proves the
// value was not used, and only the speed proves no helper was launched to
// produce it.
func TestRailProvidersHonourTheBrowserCookieOptOut(t *testing.T) {
	t.Setenv(cookies.DisableEnv, "1")

	for _, tc := range []struct {
		provider string
		domain   string
		extract  func(context.Context, string) string
	}{
		{"trainline", "thetrainline.com", trainlineBrowserCookies},
		{"eurostar", "eurostar.com", eurostarBrowserCookies},
		{"sncf", "sncf-connect.com", sncfBrowserCookies},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			start := time.Now()
			got := tc.extract(context.Background(), tc.domain)
			elapsed := time.Since(start)

			if got != "" {
				// Length, never the value: a failure here means real session
				// cookies were extracted, and a test that printed them would put
				// a contributor's live session into a public CI log.
				t.Errorf("%s returned cookies while %s is set (%d bytes; value withheld)",
					tc.provider, cookies.DisableEnv, len(got))
			}
			if elapsed > 50*time.Millisecond {
				t.Errorf("%s took %v to decline, long enough that a helper may have run; "+
					"if this provider now uses its own extractor it must honour %s too",
					tc.provider, elapsed, cookies.DisableEnv)
			}
		})
	}
}
