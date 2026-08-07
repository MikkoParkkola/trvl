package providers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chromedp/cdproto/network"

	"github.com/MikkoParkkola/trvl/internal/consent"
)

// TestCDPHarvestSeparatesTheTwoConsentQuestions pins the boundary between the
// two controls, in the direction that survived review.
//
// An earlier revision of this file asserted the opposite: that
// TRVL_NO_BROWSER_COOKIES must also stop the Tier-2 harvest, on the reasoning
// that "a harvest whose entire purpose is cookies" is governed by the cookie
// opt-out. That reasoning was wrong about the mechanism. runCDPCollect passes no
// UserDataDir, so the browser it starts is BLANK — no logins, no history, none
// of the user's cookies. What it harvests is a session this process created by
// visiting the site, not one taken from the user. Gating it on the cookie
// decline blocked the only acquisition path that still works for a declining
// user (Booking.com reaches it at internal/hotels/booking_search.go) and
// protected nothing.
//
// The two variables answer two different questions:
//   - TRVL_NO_BROWSER_COOKIES — may trvl touch MY browsers and my sessions?
//   - TRVL_NO_TIER2_CDP       — may trvl run a browser process at all?
//
// This test holds that line in both directions.
func TestCDPHarvestSeparatesTheTwoConsentQuestions(t *testing.T) {
	const target = "https://example.test/search"

	// A stub browser, so the test proves the gating rather than what is installed
	// on the host. It records whether it was reached at all.
	var launched bool
	stub := func(ctx context.Context, execPath, targetURL string, wait time.Duration) ([]*network.Cookie, error) {
		launched = true
		return []*network.Cookie{{Name: "cf_clearance", Value: "minted-by-this-blank-browser"}}, nil
	}
	withStub := func(t *testing.T) {
		t.Helper()
		orig := cdpRunner
		cdpRunner = stub
		launched = false
		t.Cleanup(func() { cdpRunner = orig })
	}

	// CONTROL. Nothing declined: the harvest must reach the browser and return
	// cookies, or neither case below would prove anything.
	t.Run("harvests while nothing is declined", func(t *testing.T) {
		withStub(t)
		t.Setenv("TRVL_ALLOW_BROWSER_COOKIES", "1")

		got, err := RefreshCookiesViaCDP(context.Background(), target,
			WithTier2ExecPath("/nonexistent/browser"), WithTier2ChallengeWait(time.Millisecond), withTier2Lookup(publicTestLookup))
		if err != nil {
			t.Fatalf("the fixture refused with no opt-out in force, so the cases below would prove nothing: %v", err)
		}
		if !launched || len(got) != 1 {
			t.Fatalf("the fixture harvested nothing: launched=%v got=%v", launched, got)
		}
	})

	// The behaviour the release turns back on. A user who declined access to
	// their own browsers keeps the anonymous fallback, because the anonymous
	// fallback never touched their browsers.
	t.Run("a cookie decline alone does not stop the anonymous harvest", func(t *testing.T) {
		withStub(t)
		t.Setenv("TRVL_ALLOW_BROWSER_COOKIES", "1")
		// Note what is NOT set here: the Tier-2 variable is left at its default,
		// so the only opt-out in force is the one about the user's own browsers.
		t.Setenv(consent.CookiesEnv, "1")
		if Tier2Declined() {
			t.Fatal("precondition: the Tier-2 opt-out must be off, or this proves nothing")
		}

		got, err := RefreshCookiesViaCDP(context.Background(), target,
			WithTier2ExecPath("/nonexistent/browser"), WithTier2ChallengeWait(time.Millisecond), withTier2Lookup(publicTestLookup))
		if err != nil {
			t.Fatalf("%s blocked the profile-less harvest, which reads none of the user's cookies: %v",
				consent.CookiesEnv, err)
		}
		if !launched {
			t.Errorf("%s stopped the blank browser from starting; that is the Tier-2 variable's job", consent.CookiesEnv)
		}
		if len(got) != 1 {
			t.Errorf("the anonymous harvest returned nothing: %v", got)
		}
	})

	// The other direction: the variable that DOES govern this path still does.
	t.Run("the Tier-2 decline stops it", func(t *testing.T) {
		withStub(t)
		t.Setenv("TRVL_ALLOW_BROWSER_COOKIES", "1")
		t.Setenv(consent.Tier2Env, "1")

		got, err := RefreshCookiesViaCDP(context.Background(), target,
			WithTier2ExecPath("/nonexistent/browser"), WithTier2ChallengeWait(time.Millisecond))
		if !errors.Is(err, ErrTier2Disabled) {
			t.Errorf("%s did not stop the harvest: err=%v", consent.Tier2Env, err)
		}
		if launched {
			t.Errorf("a browser was launched after the user set %s", consent.Tier2Env)
		}
		if len(got) != 0 {
			t.Errorf("harvested cookies were returned after the decline: %v", got)
		}
	})
}

// TestCDPDriverRefusesTheTier2Decline covers the driver rather than the
// entrypoint, because that is where a caller reaching past RefreshCookiesViaCDP
// would spawn the browser. Only the Tier-2 variable is asserted here: the
// cookie variable's non-effect is proved at the entrypoint above, against a
// stubbed browser, because proving it here would mean letting the driver
// actually try to launch one.
func TestCDPDriverRefusesTheTier2Decline(t *testing.T) {
	t.Setenv("TRVL_ALLOW_BROWSER_COOKIES", "1")
	t.Setenv(consent.Tier2Env, "1")

	got, err := runCDPCollect(context.Background(), "/nonexistent/browser",
		"https://example.test/", time.Millisecond)
	if !errors.Is(err, ErrTier2Disabled) {
		t.Errorf("the driver would have launched a browser after a Tier-2 decline: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("the driver returned cookies after a decline: %v", got)
	}
}
