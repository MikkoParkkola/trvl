package providers

import (
	"net/http"
	"net/url"
	"sync"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/consent"
)

// TestBrowserSeedCannotLandAfterADecline pins the eighth path of this family,
// and the first that is a race rather than a missing check.
//
// Reading the user's browser takes seconds — Keychain unlock, a cold profile, a
// window the user has to clear by hand. A decline that arrives during that read
// used to lose: the revocation looked at a client that was not yet marked as
// browser-seeded, found nothing to do, and returned; the read then finished and
// committed its cookies into a jar nobody was going to check again. The setting
// was applied and the browser cookies kept going out.
//
// So the check that matters is not the one at the call site before the read. It
// is the one under the lock, immediately before the cookies are committed.
func TestBrowserSeedCannotLandAfterADecline(t *testing.T) {
	u, err := url.Parse("https://example.test/preflight")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	fromBrowser := []*http.Cookie{{Name: "datadome", Value: "from-the-users-browser"}}

	// CONTROL. Nothing declined: the seed must land, or the assertion below
	// would pass against a vault that simply never accepts anything.
	t.Run("lands while nothing is declined", func(t *testing.T) {
		v := newCookieVault()
		if !v.seedFromBrowser(u, fromBrowser) {
			t.Fatal("the seed was refused with no opt-out in force")
		}
		if len(v.Cookies(u)) == 0 {
			t.Fatal("the fixture stored nothing, so the decline case would prove nothing")
		}
		if !v.isBrowserSeeded() {
			t.Error("the provenance was not recorded")
		}
	})

	t.Run("refused once the user declines", func(t *testing.T) {
		v := newCookieVault()
		t.Setenv(consent.CookiesEnv, "1")

		// The read that produced these cookies began before the decline. It
		// still must not commit them.
		if v.seedFromBrowser(u, fromBrowser) {
			t.Error("a browser read that started before the opt-out committed its cookies after it")
		}
		if cs := v.Cookies(u); len(cs) != 0 {
			t.Errorf("browser-derived cookies are in the jar and would be sent: %v", cs)
		}
		if v.isBrowserSeeded() {
			t.Error("the jar is marked browser-seeded despite committing nothing")
		}
	})
}

// TestConcurrentSeedAndDiscardLeaveNothingBehind runs the two halves against
// each other. Under `trvl mcp` several searches share one provider client, so
// seeds and a revocation genuinely overlap; the outcome must not depend on who
// wins. Run under -race this also covers the second half of the round-8 finding:
// the jar the http.Client holds is written by one goroutine while others read
// it through Cookies.
func TestConcurrentSeedAndDiscardLeaveNothingBehind(t *testing.T) {
	u, err := url.Parse("https://example.test/preflight")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	v := newCookieVault()

	// CONTROL: a permitted seed first, so the discard below has something real
	// to remove and the final emptiness is not the emptiness of a jar that was
	// never filled.
	if !v.seedFromBrowser(u, []*http.Cookie{{Name: "datadome", Value: "from-the-users-browser"}}) {
		t.Fatal("the fixture did not seed")
	}

	t.Setenv(consent.CookiesEnv, "1")

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// In-flight browser reads finishing after the decline.
			v.seedFromBrowser(u, []*http.Cookie{{Name: "datadome", Value: "late-arrival"}})
			// And the ordinary server-driven path, which must keep working.
			v.SetCookies(u, []*http.Cookie{{Name: "server", Value: "set-cookie"}})
			_ = v.Cookies(u)
		}()
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v.discardBrowserSeeded()
		}()
	}
	wg.Wait()

	// Whoever won the interleaving, no browser-derived cookie may survive: once
	// declined, no seed can commit, so every discard is final.
	if v.isBrowserSeeded() {
		t.Error("the vault is still marked browser-seeded after the opt-out")
	}
	for _, c := range v.Cookies(u) {
		if c.Name == "datadome" {
			t.Errorf("a browser-derived cookie survived and would still be sent: %v", c)
		}
	}
}
