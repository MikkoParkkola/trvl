package providers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chromedp/cdproto/network"

	"github.com/MikkoParkkola/trvl/internal/consent"
)

// TestResolveChallengeObeysTheCookieDecline closes the sibling of the Tier-2
// harvest gate, and it is the one the adversarial review found still open.
//
// ResolveChallenge is a second CDP entrypoint. It exists to clear an anti-bot
// challenge rather than to refresh cookies, and it was gated on Tier2Declined
// alone — so a user who set TRVL_NO_BROWSER_COOKIES and left the Tier-2 variable
// at its default still got their browser launched, their session harvested from
// it, and the result written to ~/.trvl/cookies for the whole cache TTL. The
// stated purpose of the call does not change whose session is being taken.
func TestResolveChallengeObeysTheCookieDecline(t *testing.T) {
	const target = "https://example.test/search"

	var launched bool
	stub := func(ctx context.Context, execPath, targetURL string, wait time.Duration) ([]*network.Cookie, string, error) {
		launched = true
		return []*network.Cookie{{Name: "cf_clearance", Value: "from-the-users-browser"}}, "<html>ok</html>", nil
	}
	withStub := func(t *testing.T) {
		t.Helper()
		orig := cdpChallengeRunner
		cdpChallengeRunner = stub
		launched = false
		t.Cleanup(func() { cdpChallengeRunner = orig })
	}

	// CONTROL. Without this the decline cases below would pass against a path
	// that refuses in a test binary for unrelated reasons.
	t.Run("resolves while nothing is declined", func(t *testing.T) {
		withStub(t)
		t.Setenv("TRVL_ALLOW_BROWSER_COOKIES", "1")

		got, err := ResolveChallenge(context.Background(), target,
			WithTier2ExecPath("/nonexistent/browser"), WithTier2ChallengeWait(time.Millisecond))
		if err != nil {
			t.Fatalf("the fixture refused with no opt-out in force, so the decline cases would prove nothing: %v", err)
		}
		if !launched || got == nil || len(got.Cookies) != 1 {
			t.Fatalf("the fixture harvested nothing: launched=%v got=%v", launched, got)
		}
	})

	t.Run("refused by the cookie decline alone", func(t *testing.T) {
		withStub(t)
		t.Setenv("TRVL_ALLOW_BROWSER_COOKIES", "1")
		// The Tier-2 variable is deliberately left at its default.
		t.Setenv(consent.CookiesEnv, "1")

		got, err := ResolveChallenge(context.Background(), target,
			WithTier2ExecPath("/nonexistent/browser"), WithTier2ChallengeWait(time.Millisecond))
		if !errors.Is(err, ErrTier2Disabled) {
			t.Errorf("the cookie decline did not stop the challenge run: err=%v", err)
		}
		if launched {
			t.Error("a browser was launched after the user declined browser cookies")
		}
		if got != nil {
			t.Errorf("a challenge result was returned after the decline: %+v", got)
		}
	})

	// A challenge wait runs for seconds, so the decline can land mid-flight with
	// the cookies already in hand. The gate sits before the result branches, so
	// this holds whether the page cleared or still shows an interactive captcha:
	// the cleared branch persists to the cache and the needs-human branch hands
	// the cookies back to the caller, and both are transmissions.
	t.Run("refused when the decline arrives during the challenge", func(t *testing.T) {
		orig := cdpChallengeRunner
		cdpChallengeRunner = func(ctx context.Context, execPath, targetURL string, wait time.Duration) ([]*network.Cookie, string, error) {
			t.Setenv(consent.CookiesEnv, "1")
			return []*network.Cookie{{Name: "cf_clearance", Value: "from-the-users-browser"}}, "<html>ok</html>", nil
		}
		t.Cleanup(func() { cdpChallengeRunner = orig })
		t.Setenv("TRVL_ALLOW_BROWSER_COOKIES", "1")

		got, err := ResolveChallenge(context.Background(), target,
			WithTier2ExecPath("/nonexistent/browser"), WithTier2ChallengeWait(time.Millisecond))
		if !errors.Is(err, ErrTier2Disabled) {
			t.Errorf("a challenge run that finished after the opt-out did not report the decline: %v", err)
		}
		if got != nil {
			t.Errorf("the challenge run survived a decline that arrived while it was running: %+v", got)
		}
	})
}

// TestChallengeDriverRefusesTheCookieDeclineToo covers the driver rather than the
// entrypoint, because that is where a caller reaching past ResolveChallenge would
// spawn the browser.
func TestChallengeDriverRefusesTheCookieDeclineToo(t *testing.T) {
	t.Setenv("TRVL_ALLOW_BROWSER_COOKIES", "1")
	t.Setenv(consent.CookiesEnv, "1")

	got, html, err := runCDPChallenge(context.Background(), "/nonexistent/browser",
		"https://example.test/", time.Millisecond)
	if !errors.Is(err, ErrTier2Disabled) {
		t.Errorf("the driver would have launched a browser after a cookie decline: %v", err)
	}
	if len(got) != 0 || html != "" {
		t.Errorf("the driver returned browser state after a decline: cookies=%v html=%q", got, html)
	}
}
