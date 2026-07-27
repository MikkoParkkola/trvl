package providers

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/cookies"
)

// TestInPackageReadersHonourTheOptOut covers the paths provider recovery code
// actually takes, not just the exported wrapper. Search recovery reaches the
// kooky reader through currentCookieSource, through the browser-hint variant,
// and through the background pre-warm helper — none of which go via
// BrowserCookiesForURL. Gating only the exported name would leave a control
// that reads correct and behaves wrong.
//
// TRVL_ALLOW_BROWSER_COOKIES is set deliberately: without it the test-binary
// guard returns nil on its own and every assertion below would pass whether the
// opt-out existed or not. With it set, nil can only come from the opt-out, so
// deleting the gate makes these subtests read the developer's real cookie
// stores and fail.
func TestInPackageReadersHonourTheOptOut(t *testing.T) {
	t.Setenv("TRVL_ALLOW_BROWSER_COOKIES", "1")
	t.Setenv(cookies.DisableEnv, "1")

	const target = "https://www.booking.com/"

	// Counts only. A cookie value is a live session and this test runs against
	// real stores when the gate is broken; printing one would publish it into a
	// terminal or a CI log.
	t.Run("currentCookieSource", func(t *testing.T) {
		if got := currentCookieSource(target); got != nil {
			t.Fatalf("opt-out set, still got %d cookies through currentCookieSource", len(got))
		}
	})

	t.Run("browserHintPath", func(t *testing.T) {
		for _, hint := range []string{"brave", "chrome"} {
			if got := browserCookiesForURLWithHint(target, hint); got != nil {
				t.Errorf("opt-out set, still got %d cookies with hint %q", len(got), hint)
			}
		}
	})

	t.Run("prewarmReader", func(t *testing.T) {
		if got := readBrowserCookiesDirect(target, ""); got != nil {
			t.Fatalf("opt-out set, still got %d cookies through the pre-warm reader", len(got))
		}
	})

	t.Run("exportedWrapper", func(t *testing.T) {
		if got := BrowserCookiesForURL(target); got != nil {
			t.Fatalf("opt-out set, still got %d cookies through BrowserCookiesForURL", len(got))
		}
	})
}
