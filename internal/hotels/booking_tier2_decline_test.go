package hotels

import (
	"context"
	"errors"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/providers"
)

// TestBookingTokenHarvestHonoursTheTier2Decline pins the fourth bypass of the
// same family the earlier rounds of #521 found, this one on the hotel path.
//
// The booking.com aws-waf-token harvest used to pass a force flag that skipped
// the Tier-2 opt-in, so a hotel search reached a browser by a route the user's
// setting did not govern. The flag is gone; this test is what keeps it gone.
//
// TRVL_ALLOW_BROWSER_COOKIES is set deliberately. Without it the harvest refuses
// for a second, unrelated reason -- a guard that stops `go test` launching a real
// browser -- and both guards return the same sentinel, so the test would pass
// while never exercising the decline at all. A first draft of this test did
// exactly that, and its own control case caught it. With the test-binary guard
// disarmed, the decline is the only thing left that can refuse.
//
// There is no paired "runs without a decline" case here, and the omission is
// deliberate rather than forgotten: proving that requires stubbing the browser
// runner, which is private to internal/providers, and that package already owns
// both halves of it. What this package has to prove is only that its own call
// site no longer asks to skip the check.
//
// Verified by mutation, with a result worth recording: restoring the force flag
// does not fail this test, it fails to COMPILE. The option it named was deleted
// from internal/providers, so the bypass cannot come back through one word at one
// call site -- someone would have to add the option back first. This test guards
// the weaker property that survives that: the decline reaches this path at all.
func TestBookingTokenHarvestHonoursTheTier2Decline(t *testing.T) {
	t.Setenv("TRVL_ALLOW_BROWSER_COOKIES", "1")
	t.Setenv("TRVL_NO_TIER2_CDP", "1")

	// forceRefresh skips the cache and the in-process cookie reader, leaving the
	// browser harvest as the only remaining path. Without it, a machine with a
	// warm cookie cache returns a token and passes having tested nothing.
	_, err := acquireBookingWAFToken(context.Background(), bookingBaseURL, true)

	if err == nil {
		t.Fatal("token harvest succeeded despite an explicit tier-2 decline; the browser gate is not on this path")
	}
	// errors.Is against the shared sentinel rather than a string match, so
	// rewording the message cannot silently turn this green.
	if !errors.Is(err, providers.ErrTier2Disabled) {
		t.Fatalf("harvest refused, but not for the declared reason: err = %v, want it to wrap ErrTier2Disabled", err)
	}
}
