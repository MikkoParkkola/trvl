package cookies

import (
	"errors"
	"testing"
	"time"
)

// TestPageIfPermittedRefusedAfterADecline pins the twelfth path of this family,
// and the first in a currency other than cookies.
//
// BrowserReadPage drives the user's real Chrome or Safari through osascript and
// reads the rendered page out of their logged-in session. That takes SECONDS — a
// window opens, a page loads, a challenge may be solved by hand — and the check
// sat before all of it. A decline arriving during the read lost: the read
// finished afterwards and the page text went back to the caller anyway. The
// cookie readers had exactly this race; the page reader kept it two rounds
// longer because it returns text rather than cookies and no lint was looking.
//
// Ordered around a CONTROL so the decline assertion is about the decline.
func TestPageIfPermittedRefusedAfterADecline(t *testing.T) {
	const harvested = "the user's logged-in account page"

	t.Run("returned while nothing is declined", func(t *testing.T) {
		got, err := pageIfPermitted(harvested)
		if err != nil {
			t.Fatalf("the page was refused with no opt-out in force: %v", err)
		}
		if got != harvested {
			t.Fatalf("the fixture returned nothing, so the decline case would prove nothing: %q", got)
		}
	})

	t.Run("refused once the user declines", func(t *testing.T) {
		t.Setenv(DisableEnv, "1")

		// This text came out of a browser read that began before the decline.
		got, err := pageIfPermitted(harvested)
		if !errors.Is(err, ErrBrowserReadDeclined) {
			t.Errorf("a read that finished after the opt-out did not report the decline: %v", err)
		}
		if got != "" {
			t.Errorf("the logged-in page text survived the opt-out: %q", got)
		}
	})
}

// TestCachedPageCannotLandAfterADecline covers the more durable half. A page
// that escapes into the cache is served for the whole TTL, so a decline that
// arrives during the read has to stop the COPY as well as the return value.
func TestCachedPageCannotLandAfterADecline(t *testing.T) {
	const (
		url  = "https://example.test/account"
		text = "the user's logged-in account page"
	)

	clear := func(t *testing.T) {
		t.Helper()
		t.Cleanup(func() {
			browserPageCache.Lock()
			delete(browserPageCache.entries, url)
			browserPageCache.Unlock()
		})
	}
	cached := func() (browserCacheEntry, bool) {
		browserPageCache.Lock()
		defer browserPageCache.Unlock()
		e, ok := browserPageCache.entries[url]
		return e, ok
	}

	// CONTROL: the store must work, or the refusal below would be the emptiness
	// of a cache that never accepts anything.
	t.Run("stored while nothing is declined", func(t *testing.T) {
		clear(t)
		if err := cachePageIfPermitted(url, text, time.Minute); err != nil {
			t.Fatalf("the store was refused with no opt-out in force: %v", err)
		}
		if e, ok := cached(); !ok || e.text != text {
			t.Fatalf("the fixture stored nothing, so the decline case would prove nothing: %+v", e)
		}
	})

	t.Run("refused once the user declines", func(t *testing.T) {
		clear(t)
		t.Setenv(DisableEnv, "1")

		if err := cachePageIfPermitted(url, text, time.Minute); !errors.Is(err, ErrBrowserReadDeclined) {
			t.Errorf("the store did not report the decline: %v", err)
		}
		if e, ok := cached(); ok {
			t.Errorf("the logged-in page was cached after the opt-out and would be served for the whole TTL: %+v", e)
		}
	})
}
