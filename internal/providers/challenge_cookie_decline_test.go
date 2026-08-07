package providers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chromedp/cdproto/network"

	"github.com/MikkoParkkola/trvl/internal/consent"
)

// TestResolveChallengeSeparatesTheTwoConsentQuestions is the sibling of
// TestCDPHarvestSeparatesTheTwoConsentQuestions, and it corrects the same
// mistake.
//
// An earlier revision asserted that TRVL_NO_BROWSER_COOKIES must stop
// ResolveChallenge, justified by a comment claiming the path "launches the
// user's own browser and takes its session". It does not. runCDPChallenge
// passes no UserDataDir, so the browser starts blank — the challenge is cleared
// by a session this process created. The comment was believed instead of the
// allocator seventy lines below it, and an adversarial review caught it.
//
// TRVL_NO_TIER2_CDP is what governs this path: it is the variable that answers
// "may trvl run a browser process at all?".
func TestResolveChallengeSeparatesTheTwoConsentQuestions(t *testing.T) {
	const target = "https://example.test/search"

	var launched bool
	stub := func(ctx context.Context, execPath, targetURL string, wait time.Duration) ([]*network.Cookie, string, error) {
		launched = true
		return []*network.Cookie{{Name: "cf_clearance", Value: "minted-by-this-blank-browser"}}, "<html>ok</html>", nil
	}
	withStub := func(t *testing.T) {
		t.Helper()
		orig := cdpChallengeRunner
		cdpChallengeRunner = stub
		launched = false
		t.Cleanup(func() { cdpChallengeRunner = orig })
	}

	// CONTROL. Without this the cases below would pass against a path that
	// refuses in a test binary for unrelated reasons.
	t.Run("resolves while nothing is declined", func(t *testing.T) {
		withStub(t)
		t.Setenv("TRVL_ALLOW_BROWSER_COOKIES", "1")

		res, err := ResolveChallenge(context.Background(), target,
			WithTier2ExecPath("/nonexistent/browser"), WithTier2ChallengeWait(time.Millisecond), withTier2Lookup(publicTestLookup))
		if err != nil {
			t.Fatalf("the fixture refused with no opt-out in force: %v", err)
		}
		if !launched || res == nil || res.Status != ChallengeCleared {
			t.Fatalf("the fixture did not clear the challenge: launched=%v res=%+v", launched, res)
		}
	})

	t.Run("a cookie decline alone does not stop the anonymous resolve", func(t *testing.T) {
		withStub(t)
		t.Setenv("TRVL_ALLOW_BROWSER_COOKIES", "1")
		// The Tier-2 variable is deliberately left at its default.
		t.Setenv(consent.CookiesEnv, "1")
		if Tier2Declined() {
			t.Fatal("precondition: the Tier-2 opt-out must be off, or this proves nothing")
		}

		res, err := ResolveChallenge(context.Background(), target,
			WithTier2ExecPath("/nonexistent/browser"), WithTier2ChallengeWait(time.Millisecond), withTier2Lookup(publicTestLookup))
		if err != nil {
			t.Fatalf("%s blocked a profile-less challenge resolve, which reads none of the user's cookies: %v",
				consent.CookiesEnv, err)
		}
		if !launched {
			t.Errorf("%s stopped the blank browser from starting; that is the Tier-2 variable's job", consent.CookiesEnv)
		}
		if res == nil || res.Status != ChallengeCleared {
			t.Errorf("the anonymous resolve returned no cleared result: %+v", res)
		}
	})

	t.Run("the Tier-2 decline stops it", func(t *testing.T) {
		withStub(t)
		t.Setenv("TRVL_ALLOW_BROWSER_COOKIES", "1")
		t.Setenv(consent.Tier2Env, "1")

		res, err := ResolveChallenge(context.Background(), target,
			WithTier2ExecPath("/nonexistent/browser"), WithTier2ChallengeWait(time.Millisecond))
		if !errors.Is(err, ErrTier2Disabled) {
			t.Errorf("%s did not stop the resolve: err=%v", consent.Tier2Env, err)
		}
		if launched {
			t.Errorf("a browser was launched after the user set %s", consent.Tier2Env)
		}
		if res != nil {
			t.Errorf("a result was returned after the decline: %+v", res)
		}
	})
}

// TestChallengeDriverRefusesTheTier2Decline covers the driver, where a caller
// reaching past ResolveChallenge would spawn the browser. Only the Tier-2
// variable is asserted, for the same reason as the Tier-2 driver test: proving
// the cookie variable's non-effect here would mean letting the driver try to
// launch a real browser.
func TestChallengeDriverRefusesTheTier2Decline(t *testing.T) {
	t.Setenv("TRVL_ALLOW_BROWSER_COOKIES", "1")
	t.Setenv(consent.Tier2Env, "1")

	cookies, html, err := runCDPChallenge(context.Background(), "/nonexistent/browser",
		"https://example.test/", time.Millisecond)
	if !errors.Is(err, ErrTier2Disabled) {
		t.Errorf("the driver would have launched a browser after a Tier-2 decline: %v", err)
	}
	if len(cookies) != 0 || html != "" {
		t.Errorf("the driver returned data after a decline: cookies=%v html=%q", cookies, html)
	}
}
