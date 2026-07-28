package ground

import (
	"context"
	"errors"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/consent"
	"github.com/MikkoParkkola/trvl/internal/providers"
)

// The scraper is the third place in the repo that can start a browser, and the
// only one of the three that starts an EMPTY profile. Which decline governs it
// is therefore a real question, and it has a documented answer: Tier-2, because
// TRVL_NO_TIER2_CDP is the control for a browser that never opens the user's own
// profile (internal/consent/consent.go:35-39).
//
// Both directions are asserted below. A gate that is missing ships a browser the
// user refused; a gate that is too wide refuses an access they never objected to
// and quietly turns two opt-outs into one. The second failure is the harder one
// to notice, because every test of the first still passes.

// TestBrowserScrapeRoutesRefusesOnTier2Decline is the gate that has to hold.
func TestBrowserScrapeRoutesRefusesOnTier2Decline(t *testing.T) {
	t.Setenv(consent.Tier2LegacyEnv, "")
	t.Setenv(consent.CookiesEnv, "")
	t.Setenv(consent.Tier2Env, "1")

	routes, err := BrowserScrapeRoutes(context.Background(), "sncf", "PAR", "LYS", "2026-09-15", "EUR")
	if err == nil {
		t.Fatalf("a browser was allowed to start after the user declined Tier-2; got %d routes", len(routes))
	}
	if !errors.Is(err, providers.ErrTier2Disabled) {
		t.Errorf("refusal does not satisfy the errors.Is every caller uses: %v", err)
	}
	if routes != nil {
		t.Errorf("refusal still returned routes: %v", routes)
	}
}

// TestNewBrowserScraperContextRefusesOnTier2Decline covers the allocator, which
// is reachable without going through the exported entry point.
func TestNewBrowserScraperContextRefusesOnTier2Decline(t *testing.T) {
	t.Setenv(consent.Tier2LegacyEnv, "")
	t.Setenv(consent.CookiesEnv, "")
	t.Setenv(consent.Tier2Env, "1")

	ctx, cancel, err := newBrowserScraperContext(context.Background())
	if cancel != nil {
		cancel()
	}
	if err == nil {
		t.Fatal("the allocator built a browser context after the user declined Tier-2")
	}
	if ctx != nil {
		t.Error("a usable context was returned alongside the refusal")
	}
	if !errors.Is(err, providers.ErrTier2Disabled) {
		t.Errorf("refusal does not satisfy the errors.Is every caller uses: %v", err)
	}
}

// TestBrowserScrapeRoutesDoesNotRefuseOnCookieDeclineAlone is the direction a
// previous fix got wrong, so it is asserted rather than assumed.
//
// TRVL_NO_BROWSER_COOKIES declines reads of the user's browser cookie stores.
// This scraper reads none: its allocator sets no user-data-dir, so Chrome starts
// on a throwaway profile, and whatever cookies it ends up holding were handed to
// it by the site during that session. Refusing here would deny a user their rail
// search over a store that was never touched, and would make the two variables
// one variable in practice while the documentation still promises two.
//
// The refusal that DOES belong on a cookie decline sits in
// providers.ResolveChallenge, which drives the user's real logged-in profile.
// That is the difference this test exists to keep visible.
// The allocator is the seam this asserts on, not BrowserScrapeRoutes: with the
// gate correctly absent, the exported entry point would go on to drive a real
// Chrome for up to browserScraperTimeout. newBrowserScraperContext only builds
// the chromedp context, which starts no process until something runs against it,
// so it answers "was consent refused?" without launching anything.
func TestBrowserScrapeRoutesDoesNotRefuseOnCookieDeclineAlone(t *testing.T) {
	t.Setenv(consent.Tier2Env, "")
	t.Setenv(consent.Tier2LegacyEnv, "")
	t.Setenv(consent.CookiesEnv, "1")

	if providers.Tier2Declined() {
		t.Fatalf("precondition: Tier2Declined must be FALSE here, or this test passes for the wrong reason")
	}
	if !consent.CookiesDeclined() {
		t.Fatalf("precondition: CookiesDeclined must be true here")
	}

	ctx, cancel, err := newBrowserScraperContext(context.Background())
	if cancel != nil {
		cancel()
	}
	if err != nil {
		t.Fatalf("the cookie decline is refusing an empty-profile browser it does not govern, "+
			"which collapses TRVL_NO_BROWSER_COOKIES and TRVL_NO_TIER2_CDP into one control: %v", err)
	}
	if ctx == nil {
		t.Error("no context and no error, so the caller cannot tell what happened")
	}
}
