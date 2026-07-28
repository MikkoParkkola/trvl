package providers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chromedp/cdproto/network"

	"github.com/MikkoParkkola/trvl/internal/consent"
)

// TestCDPHarvestObeysTheCookieDecline pins the thirteenth path of this family,
// and the widest one: a second consent variable that acted as a bypass.
//
// Tier-2 launches the user's installed browser, waits for the anti-bot challenge
// to resolve, harvests the cookies and writes them to ~/.trvl/cookies. It was
// gated on Tier2Declined ALONE. So a user who set TRVL_NO_BROWSER_COOKIES — "do
// not use my browser's cookies" — and left the Tier-2 variable at its default
// still got a browser launched, their session harvested from it, and the result
// persisted to disk for the whole cache TTL.
//
// Two variables, one question. TRVL_NO_TIER2_CDP is the narrower one ("no
// headless browser"); it cannot be the only thing standing in front of a harvest
// whose entire purpose is cookies.
func TestCDPHarvestObeysTheCookieDecline(t *testing.T) {
	const target = "https://example.test/search"

	// A stub browser, so the test proves the gating rather than what is installed
	// on the host. It records whether it was reached at all.
	var launched bool
	stub := func(ctx context.Context, execPath, targetURL string, wait time.Duration) ([]*network.Cookie, error) {
		launched = true
		return []*network.Cookie{{Name: "cf_clearance", Value: "from-the-users-browser"}}, nil
	}
	withStub := func(t *testing.T) {
		t.Helper()
		orig := cdpRunner
		cdpRunner = stub
		launched = false
		t.Cleanup(func() { cdpRunner = orig })
	}

	// CONTROL. Nothing declined: the harvest must reach the browser and return
	// cookies, or the decline case below would be passing against a path that
	// never works in a test binary anyway.
	t.Run("harvests while nothing is declined", func(t *testing.T) {
		withStub(t)
		t.Setenv("TRVL_ALLOW_BROWSER_COOKIES", "1")

		got, err := RefreshCookiesViaCDP(context.Background(), target,
			WithTier2ExecPath("/nonexistent/browser"), WithTier2ChallengeWait(time.Millisecond))
		if err != nil {
			t.Fatalf("the fixture refused with no opt-out in force, so the decline case would prove nothing: %v", err)
		}
		if !launched || len(got) != 1 {
			t.Fatalf("the fixture harvested nothing: launched=%v got=%v", launched, got)
		}
	})

	t.Run("refused by the cookie decline alone", func(t *testing.T) {
		withStub(t)
		t.Setenv("TRVL_ALLOW_BROWSER_COOKIES", "1")
		// Note what is NOT set here: the Tier-2 variable is left at its default.
		t.Setenv(consent.CookiesEnv, "1")

		got, err := RefreshCookiesViaCDP(context.Background(), target,
			WithTier2ExecPath("/nonexistent/browser"), WithTier2ChallengeWait(time.Millisecond))
		if !errors.Is(err, ErrTier2Disabled) {
			t.Errorf("the cookie decline did not stop the harvest: err=%v", err)
		}
		if launched {
			t.Error("a browser was launched after the user declined browser cookies")
		}
		if len(got) != 0 {
			t.Errorf("harvested cookies were returned after the decline: %v", got)
		}
	})

	// The harvest takes seconds — a challenge wait on a cold profile — so a
	// decline can arrive while the browser is still working. The cookies are then
	// already in hand, and the entry check cannot help.
	t.Run("refused when the decline arrives during the harvest", func(t *testing.T) {
		orig := cdpRunner
		cdpRunner = func(ctx context.Context, execPath, targetURL string, wait time.Duration) ([]*network.Cookie, error) {
			// The user declines while we are reading.
			t.Setenv(consent.CookiesEnv, "1")
			return []*network.Cookie{{Name: "cf_clearance", Value: "from-the-users-browser"}}, nil
		}
		t.Cleanup(func() { cdpRunner = orig })
		t.Setenv("TRVL_ALLOW_BROWSER_COOKIES", "1")

		got, err := RefreshCookiesViaCDP(context.Background(), target,
			WithTier2ExecPath("/nonexistent/browser"), WithTier2ChallengeWait(time.Millisecond))
		if !errors.Is(err, ErrTier2Disabled) {
			t.Errorf("a harvest that finished after the opt-out did not report the decline: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("the harvest survived a decline that arrived while it was running: %v", got)
		}
	})
}

// TestCDPDriverRefusesTheCookieDeclineToo covers the driver rather than the
// entrypoint, because that is where a caller reaching past RefreshCookiesViaCDP
// would spawn the browser.
func TestCDPDriverRefusesTheCookieDeclineToo(t *testing.T) {
	t.Setenv("TRVL_ALLOW_BROWSER_COOKIES", "1")
	t.Setenv(consent.CookiesEnv, "1")

	got, err := runCDPCollect(context.Background(), "/nonexistent/browser",
		"https://example.test/", time.Millisecond)
	if !errors.Is(err, ErrTier2Disabled) {
		t.Errorf("the driver would have launched a browser after a cookie decline: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("the driver returned cookies after a decline: %v", got)
	}
}
