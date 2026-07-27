package providers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chromedp/cdproto/network"
)

// TestExplicitDeclineHoldsAtBothEntrypoints is the regression test for the defect
// an adversarial review found in the first cut of #521: the opt-out was checked
// with `!cfg.force && !Tier2Enabled()`, and every production caller
// (internal/ground/trainline.go, internal/ground/sncf.go,
// internal/hotels/booking_search.go) passed a WithTier2Force option that set
// cfg.force. TRVL_NO_TIER2_CDP therefore had no effect on any real search, while
// the tests — which only ever called the unforced path — stayed green.
//
// The force option is gone now, so the shape that produced the bug cannot recur
// in that exact form. What this test pins is the property that outlived it: a
// decline holds at every entrypoint, in every spelling, with no runner reached.
func TestExplicitDeclineHoldsAtBothEntrypoints(t *testing.T) {
	declines := []struct {
		name, env, value string
	}{
		{"opt-out set", tier2DisableEnv, "1"},
		{"opt-out set to an arbitrary truthy value", tier2DisableEnv, "yes"},
		{"legacy opt-in explicitly zero", tier2EnableEnv, "0"},
		{"legacy opt-in explicitly false", tier2EnableEnv, "false"},
	}

	for _, d := range declines {
		t.Run(d.name, func(t *testing.T) {
			// Lift the test-binary guard so it cannot be what produces the
			// refusal. Only the decline may.
			t.Setenv("TRVL_ALLOW_BROWSER_COOKIES", "1")
			t.Setenv(tier2DisableEnv, "")
			t.Setenv(tier2EnableEnv, "")
			t.Setenv(d.env, d.value)

			// Pretend a browser is installed, so detection cannot be the reason
			// either.
			prevExists := fileExists
			fileExists = func(string) bool { return true }
			defer func() { fileExists = prevExists }()

			// If any gate leaks, these runners record it instead of spawning a
			// real browser.
			spawned := false
			prevRunner, prevChallenge := cdpRunner, cdpChallengeRunner
			cdpRunner = func(context.Context, string, string, time.Duration) ([]*network.Cookie, error) {
				spawned = true
				return nil, nil
			}
			cdpChallengeRunner = func(context.Context, string, string, time.Duration) ([]*network.Cookie, string, error) {
				spawned = true
				return nil, "", nil
			}
			defer func() { cdpRunner, cdpChallengeRunner = prevRunner, prevChallenge }()

			if _, err := RefreshCookiesViaCDP(context.Background(), "https://www.thetrainline.com/"); !errors.Is(err, ErrTier2Disabled) {
				t.Fatalf("RefreshCookiesViaCDP: err = %v, want ErrTier2Disabled", err)
			}
			if _, err := ResolveChallenge(context.Background(), "https://www.thetrainline.com/"); !errors.Is(err, ErrTier2Disabled) {
				t.Fatalf("ResolveChallenge: err = %v, want ErrTier2Disabled", err)
			}
			if spawned {
				t.Fatal("a runner was reached despite an explicit decline")
			}
		})
	}
}

// TestDriversRefuseAnExplicitDecline gates the two functions that actually spawn
// the browser, not just the entrypoints above them. A future caller that reaches
// past RefreshCookiesViaCDP/ResolveChallenge still cannot start a browser the
// user declined -- the low-level placement is the point (#507/#515).
func TestDriversRefuseAnExplicitDecline(t *testing.T) {
	t.Setenv("TRVL_ALLOW_BROWSER_COOKIES", "1")
	t.Setenv(tier2DisableEnv, "1")

	if _, err := runCDPCollect(context.Background(), "/nonexistent/browser", "https://www.thetrainline.com/", time.Millisecond); !errors.Is(err, ErrTier2Disabled) {
		t.Fatalf("runCDPCollect: err = %v, want ErrTier2Disabled", err)
	}
	if _, _, err := runCDPChallenge(context.Background(), "/nonexistent/browser", "https://www.thetrainline.com/", time.Millisecond); !errors.Is(err, ErrTier2Disabled) {
		t.Fatalf("runCDPChallenge: err = %v, want ErrTier2Disabled", err)
	}
}
