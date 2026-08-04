package providers

import (
	"net/http"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/consent"
)

// TestWarmCacheCookiesRefusedAfterADecline pins the eleventh path of this
// family, and the reason the fix moved from the call sites to the reader.
//
// Every browser read in this package used to decide the consent question on the
// way IN. Reading a browser takes seconds — Keychain unlock, a cold profile, a
// window the user has to clear by hand — so a decline arriving during the read
// lost: the entry check had already passed, the read finished afterwards, and
// the credentials went out. Review found that shape in the vault (round 8) and
// then again in the Booking.com room fetch (round 11), which took the result of
// one of these readers and put it straight into a Cookie header.
//
// The warm cache is the sharpest version of it: the read completed BEFORE the
// user declined and the cookies are already sitting in memory, so nothing about
// the entry check even applies. The gate therefore has to be on the way out.
func TestWarmCacheCookiesRefusedAfterADecline(t *testing.T) {
	const target = "https://example.test/search"

	// A read that has already finished, exactly as the background pre-warm
	// goroutine leaves it.
	prime := func(t *testing.T) {
		t.Helper()
		done := make(chan struct{})
		close(done)
		entry := &warmCacheEntry{
			cookies: []*http.Cookie{{Name: "datadome", Value: "from-the-users-browser"}},
			done:    done,
		}
		key := warmCacheKey(target, "")
		warmCache.mu.Lock()
		warmCache.entries[key] = entry
		warmCache.mu.Unlock()
		t.Cleanup(func() {
			warmCache.mu.Lock()
			delete(warmCache.entries, key)
			warmCache.mu.Unlock()
		})
	}

	// CONTROL. Nothing declined: the warm result must come back, or the decline
	// case below would be passing against a cache that never returns anything.
	t.Run("returned while nothing is declined", func(t *testing.T) {
		prime(t)
		got := warmBrowserCookiesResult(target, "", time.Second)
		if len(got) != 1 || got[0].Value != "from-the-users-browser" {
			t.Fatalf("the fixture returned nothing, so the decline case would prove nothing: %v", got)
		}
	})

	t.Run("refused once the user declines", func(t *testing.T) {
		prime(t)
		t.Setenv(consent.CookiesEnv, "1")

		// These cookies were read before the decline and are already in memory.
		// They still must not be handed to a caller that will send them.
		if got := warmBrowserCookiesResult(target, "", time.Second); len(got) != 0 {
			t.Errorf("browser cookies read before the opt-out survived it: %v", got)
		}
	})
}

// TestPermittedAfterReadIsTheGate covers the helper itself, because it is now
// the single place every browser reader in this package leaves through.
func TestPermittedAfterReadIsTheGate(t *testing.T) {
	harvested := []*http.Cookie{{Name: "datadome", Value: "from-the-users-browser"}}

	t.Run("passed through while nothing is declined", func(t *testing.T) {
		if got := permittedAfterRead(harvested); len(got) != 1 {
			t.Fatalf("the read was dropped with no opt-out in force: %v", got)
		}
	})

	t.Run("dropped once the user declines", func(t *testing.T) {
		t.Setenv(consent.CookiesEnv, "1")
		if got := permittedAfterRead(harvested); got != nil {
			t.Errorf("a browser read that finished before the opt-out survived it: %v", got)
		}
	})
}
