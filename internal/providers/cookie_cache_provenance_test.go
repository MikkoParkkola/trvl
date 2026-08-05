package providers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/consent"
)

const provenanceTestTarget = "https://www.thetrainline.com/search"

// writeCookieCacheFixture writes a cache file verbatim, so a test can pin what
// happens for JSON this code did not produce -- in particular a file written
// before the provenance field existed, where the key is ABSENT rather than
// empty. Generating the fixture through saveCachedCookies would always emit the
// key and the legacy case would stop being tested.
func writeCookieCacheFixture(t *testing.T, host, body string) {
	t.Helper()
	path, err := cookieCachePath(host)
	if err != nil {
		t.Fatalf("resolving the cache path: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}
}

// TestLegacyCacheEntriesCountAsBrowserDerived is the fail-fast case for #534:
// a cache file written by a version that had no provenance field must be
// treated as browser-derived and withheld under the opt-out.
//
// The fixture is hand-written with the key absent. Asserting on an empty string
// would be a different, weaker test: an absent key and an empty value only
// coincide because Go zeroes the field, and a future encoding change could
// separate them.
func TestLegacyCacheEntriesCountAsBrowserDerived(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)
	t.Setenv(consent.CookiesEnv, "1")

	u, err := url.Parse(provenanceTestTarget)
	if err != nil {
		t.Fatalf("parsing the test URL: %v", err)
	}
	saved := time.Now().Format(time.RFC3339Nano)
	writeCookieCacheFixture(t, u.Host, fmt.Sprintf(
		`[{"name":"session","value":"harvested-before-provenance-existed","domain":"","path":"","expires":"0001-01-01T00:00:00Z","secure":false,"http_only":false,"saved_at":%q}]`,
		saved))

	client := freshClient(t)
	if loadCachedCookies(client, provenanceTestTarget) {
		t.Error("a cache entry with no provenance was loaded under the opt-out; an unknown origin has to resolve toward refusing")
	}
	if got := client.Jar.Cookies(u); len(got) != 0 {
		t.Errorf("the jar was seeded anyway: %d cookie(s) reached the request, want 0", len(got))
	}
}

// TestDeclineKeepsSiteDerivedCookies is the point of the change: the opt-out
// stops replaying the user's browser cookies without throwing away the cookies
// the site itself issued, which used to be refused along with them.
//
// It asserts at the loadCachedCookies seam with the jar inspected afterwards --
// the same seam the broad-refusal test uses -- rather than on a provenance
// helper, because a helper that classifies correctly while the load path
// ignores it would pass a helper test.
func TestDeclineKeepsSiteDerivedCookies(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)
	t.Setenv(consent.CookiesEnv, "1")

	u, err := url.Parse(provenanceTestTarget)
	if err != nil {
		t.Fatalf("parsing the test URL: %v", err)
	}
	saved := time.Now().Format(time.RFC3339Nano)
	writeCookieCacheFixture(t, u.Host, fmt.Sprintf(
		`[{"name":"from_site","value":"issued-by-the-site","domain":"","path":"","expires":"0001-01-01T00:00:00Z","secure":false,"http_only":false,"saved_at":%q,"provenance":"site"},`+
			`{"name":"from_browser","value":"harvested-from-the-browser","domain":"","path":"","expires":"0001-01-01T00:00:00Z","secure":false,"http_only":false,"saved_at":%q,"provenance":"browser"}]`,
		saved, saved))

	client := freshClient(t)
	if !loadCachedCookies(client, provenanceTestTarget) {
		t.Fatal("the whole cache was refused; the opt-out is meant to drop the browser-derived entries, not the file")
	}
	got := client.Jar.Cookies(u)
	if len(got) != 1 {
		t.Fatalf("got %d cookie(s) in the jar, want exactly the site-derived one", len(got))
	}
	if got[0].Name != "from_site" {
		t.Fatalf("the jar holds %q, want from_site: a browser-derived cookie reached the request under the opt-out", got[0].Name)
	}
}

// TestSaveRecordsProvenance pins what each kind of save site writes, since the
// load path can only be as good as the field it reads.
func TestSaveRecordsProvenance(t *testing.T) {
	u, err := url.Parse(provenanceTestTarget)
	if err != nil {
		t.Fatalf("parsing the test URL: %v", err)
	}

	readProvenance := func(t *testing.T) []string {
		t.Helper()
		path, err := cookieCachePath(u.Host)
		if err != nil {
			t.Fatalf("resolving the cache path: %v", err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading the cache file: %v", err)
		}
		var cached []cachedCookie
		if err := json.Unmarshal(data, &cached); err != nil {
			t.Fatalf("parsing the cache file: %v", err)
		}
		out := make([]string, len(cached))
		for i, c := range cached {
			out[i] = c.Provenance
		}
		return out
	}

	t.Run("browser seeded vault", func(t *testing.T) {
		homeDir := t.TempDir()
		t.Setenv("HOME", homeDir)
		t.Setenv("USERPROFILE", homeDir)
		v := newCookieVault()
		if v == nil {
			t.Fatal("building the vault")
		}
		if !v.seedFromBrowser(u, []*http.Cookie{{Name: "session", Value: "harvested"}}) {
			t.Fatal("the vault refused the browser cookies")
		}
		saveCachedCookies(&http.Client{Jar: v}, provenanceTestTarget)
		if got := readProvenance(t); len(got) != 1 || got[0] != provenanceBrowser {
			t.Fatalf("provenance %v, want [browser]", got)
		}
	})

	t.Run("plain jar", func(t *testing.T) {
		// The headless tier-2 path saves from a plain jar filled by a fresh
		// profile: those cookies came from the site, never from the user's own
		// browser store, and a decline of browser reads must not drop them.
		homeDir := t.TempDir()
		t.Setenv("HOME", homeDir)
		t.Setenv("USERPROFILE", homeDir)
		jar, err := cookiejar.New(nil)
		if err != nil {
			t.Fatalf("building a jar: %v", err)
		}
		jar.SetCookies(u, []*http.Cookie{{Name: "waf", Value: "issued-by-the-site"}})
		saveCachedCookies(&http.Client{Jar: jar}, provenanceTestTarget)
		if got := readProvenance(t); len(got) != 1 || got[0] != provenanceSite {
			t.Fatalf("provenance %v, want [site]", got)
		}
	})

	t.Run("site cookies loaded into a vault stay site", func(t *testing.T) {
		// The provenance must not be sticky-browser: loading a site-derived
		// cache into a provider client's vault has to leave the vault
		// unmarked, or the next save would relabel those cookies browser and
		// the following opt-out would drop them for good.
		homeDir := t.TempDir()
		t.Setenv("HOME", homeDir)
		t.Setenv("USERPROFILE", homeDir)
		saved := time.Now().Format(time.RFC3339Nano)
		writeCookieCacheFixture(t, u.Host, fmt.Sprintf(
			`[{"name":"waf","value":"issued-by-the-site","domain":"","path":"","expires":"0001-01-01T00:00:00Z","secure":false,"http_only":false,"saved_at":%q,"provenance":"site"}]`,
			saved))

		v := newCookieVault()
		if v == nil {
			t.Fatal("building the vault")
		}
		client := &http.Client{Jar: v}
		if !loadCachedCookies(client, provenanceTestTarget) {
			t.Fatal("the site-derived cache did not load")
		}
		if v.isBrowserSeeded() {
			t.Fatal("loading site-derived cookies marked the vault browser-seeded")
		}
		saveCachedCookies(client, provenanceTestTarget)
		if got := readProvenance(t); len(got) != 1 || got[0] != provenanceSite {
			t.Fatalf("provenance after a round trip %v, want [site]", got)
		}
	})
}
