package ground

import (
	"context"
	"errors"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/consent"
	"github.com/MikkoParkkola/trvl/internal/providers"
)

// declineCookiesOnly sets the browser-cookie opt-out and clears BOTH Tier-2
// variables, which is the exact configuration the bypass needed: a user who
// asked for no browser cookies and never touched the Tier-2 knobs.
func declineCookiesOnly(t *testing.T) {
	t.Helper()
	t.Setenv(consent.Tier2Env, "")
	t.Setenv(consent.Tier2LegacyEnv, "")
	t.Setenv(consent.CookiesEnv, "1")

	if providers.Tier2Declined() {
		t.Fatalf("precondition: Tier2Declined must be FALSE here, or this test passes for the wrong reason")
	}
	if !consent.CookiesDeclined() {
		t.Fatalf("precondition: CookiesDeclined must be true here")
	}
}

// The scraper is the third place in the repo that can start a browser. Gating it
// on Tier2Declined alone left TRVL_NO_BROWSER_COOKIES with no effect on it, and
// the SNCF caller harvests an x-bff-key from the session it opens.
func TestBrowserScrapeRoutesRefusesOnCookieDeclineAlone(t *testing.T) {
	declineCookiesOnly(t)

	routes, err := BrowserScrapeRoutes(context.Background(), "sncf", "PAR", "LYS", "2026-09-15", "EUR")
	if err == nil {
		t.Fatalf("a browser was allowed to start after the user declined browser cookies; got %d routes", len(routes))
	}
	if !errors.Is(err, providers.ErrTier2CookiesDeclined) {
		t.Errorf("error does not name the variable the user actually set: %v", err)
	}
	if !errors.Is(err, providers.ErrTier2Disabled) {
		t.Errorf("error no longer satisfies the errors.Is every existing caller uses: %v", err)
	}
	if routes != nil {
		t.Errorf("refusal still returned routes: %v", routes)
	}
}

// Gating the exported entry point is not enough on its own: the allocator is
// reachable directly, which is why the Tier-2 check sits on it as well.
func TestNewBrowserScraperContextRefusesOnCookieDeclineAlone(t *testing.T) {
	declineCookiesOnly(t)

	ctx, cancel, err := newBrowserScraperContext(context.Background())
	if cancel != nil {
		cancel()
	}
	if err == nil {
		t.Fatal("the allocator built a browser context after the user declined browser cookies")
	}
	if ctx != nil {
		t.Error("a usable context was returned alongside the refusal")
	}
	if !errors.Is(err, providers.ErrTier2CookiesDeclined) {
		t.Errorf("error does not name the variable the user actually set: %v", err)
	}
}

// The gate sits above the provider switch, so it has to hold for every provider
// and not just the SNCF path that motivated the fix. Trainline reaches the same
// scraper through the same entry point.
func TestBrowserScrapeRoutesRefusesForTrainlineToo(t *testing.T) {
	declineCookiesOnly(t)

	routes, err := BrowserScrapeRoutes(context.Background(), "trainline", "PAR", "LYS", "2026-09-15", "EUR")
	if err == nil {
		t.Fatalf("trainline started a browser after the user declined browser cookies; got %d routes", len(routes))
	}
	if !errors.Is(err, providers.ErrTier2CookiesDeclined) {
		t.Errorf("trainline refusal does not name the cookie variable: %v", err)
	}
	if routes != nil {
		t.Errorf("refusal still returned routes: %v", routes)
	}
}

// Setting both variables is the case where check order becomes visible. Either
// order refuses, and ErrTier2CookiesDeclined wraps ErrTier2Disabled so either
// order satisfies the same errors.Is — which is exactly why the wrong order is
// easy to ship and hard to notice. What the user reads is the difference: the
// message has to name the cookie variable they set, not fall back to the
// generic Tier-2 wording just because that check happened to run first.
func TestBrowserScrapeRoutesNamesTheCookieVariableWhenBothAreSet(t *testing.T) {
	t.Setenv(consent.Tier2LegacyEnv, "")
	t.Setenv(consent.Tier2Env, "1")
	t.Setenv(consent.CookiesEnv, "1")

	if !providers.Tier2Declined() {
		t.Fatalf("precondition: both declines must be active for this test to mean anything")
	}
	if !consent.CookiesDeclined() {
		t.Fatalf("precondition: CookiesDeclined must be true here")
	}

	_, err := BrowserScrapeRoutes(context.Background(), "sncf", "PAR", "LYS", "2026-09-15", "EUR")
	if err == nil {
		t.Fatal("a browser was allowed to start with both declines set")
	}
	if !errors.Is(err, providers.ErrTier2CookiesDeclined) {
		t.Errorf("with both variables set the user is told about the wrong one; the cookie decline is the specific signal and must win: %v", err)
	}
	if !errors.Is(err, providers.ErrTier2Disabled) {
		t.Errorf("error no longer satisfies the errors.Is every existing caller uses: %v", err)
	}
}
