package providers

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/consent"
)

// TestCachedCookiesAreRefusedAfterADecline pins the fifth bypass of this
// family, and the only one that survived a decline being set correctly.
//
// Cookies read out of the user's browser get PERSISTED: auth.go's tier-3
// fallback calls saveCachedCookies as soon as tryBrowserCookieRetry succeeds.
// Every read of that file went through loadCachedCookies, which asked nobody's
// permission. So a user who ran trvl once, then set TRVL_NO_BROWSER_COOKIES,
// kept sending their harvested browser cookies for the whole 24-hour cache TTL.
// The setting looked like it worked. Nothing about the requests changed.
//
// The control case below is load-bearing and is why the test is written in this
// order: it proves the fixture actually wrote a cache file that CAN be loaded.
// Without it, a typo in the temporary HOME would make the decline case pass
// against a cache that was never there -- which is exactly how the first draft
// of the hotel test in this branch fooled itself.
// The fixture is written through a vault seeded from the browser, because that
// is what makes the entry browser-derived on disk (#534). The refusal is now
// targeted at those entries rather than at the whole file, so a fixture saved
// from a plain jar would be site-derived, would legitimately survive the
// decline, and would prove nothing about this bypass.
func TestCachedCookiesAreRefusedAfterADecline(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	const target = "https://www.thetrainline.com/search"
	u, err := url.Parse(target)
	if err != nil {
		t.Fatalf("parsing the test URL: %v", err)
	}

	seed := newCookieVault()
	if seed == nil {
		t.Fatal("building the seed vault")
	}
	if !seed.seedFromBrowser(u, []*http.Cookie{{Name: "session", Value: "harvested-from-the-browser"}}) {
		t.Fatal("the seed vault refused the browser cookies, so the fixture is not browser-derived")
	}
	saveCachedCookies(&http.Client{Jar: seed}, target)

	// Control: the cache is real and loadable when nothing has been declined.
	if !loadCachedCookies(freshClient(t), target) {
		t.Fatal("the fixture wrote no loadable cache, so the decline case below would prove nothing")
	}

	t.Setenv(consent.CookiesEnv, "1")

	client := freshClient(t)
	if loadCachedCookies(client, target) {
		t.Error("the persisted cookies were loaded despite the decline; the cache replays a browser harvest the user asked us to stop")
	}
	if got := client.Jar.Cookies(u); len(got) != 0 {
		t.Errorf("the jar was seeded anyway: %d cookie(s) reached the request, want 0", len(got))
	}
}

func freshClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("building a jar: %v", err)
	}
	return &http.Client{Jar: jar}
}
