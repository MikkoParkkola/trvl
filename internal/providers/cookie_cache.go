package providers

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/MikkoParkkola/trvl/internal/logredact"

	"github.com/MikkoParkkola/trvl/internal/consent"
)

const cookieCacheTTL = 24 * time.Hour

// Provenance values recorded on every cached cookie: which kind of save site
// wrote it. The distinction the opt-out needs is only "came out of the user's
// own browser store" versus "the site handed it to us", so there are two.
//
// provenanceBrowser is also what an entry with NO provenance means. Caches
// written before this field existed cannot be classified after the fact, and
// the only safe reading of an unknown cookie under an opt-out is the one that
// refuses it.
const (
	provenanceBrowser = "browser"
	provenanceSite    = "site"
)

// cachedCookie is the on-disk representation of an http.Cookie.
type cachedCookie struct {
	Name     string    `json:"name"`
	Value    string    `json:"value"`
	Domain   string    `json:"domain"`
	Path     string    `json:"path"`
	Expires  time.Time `json:"expires"`
	Secure   bool      `json:"secure"`
	HttpOnly bool      `json:"http_only"`
	SavedAt  time.Time `json:"saved_at"`
	// Provenance is "browser" or "site"; empty means an entry written before
	// this field existed, which reads as "browser". Written without omitempty
	// so the file always states it rather than leaving a reader to infer it.
	Provenance string `json:"provenance"`
}

// isBrowserDerived reports whether this entry must be withheld from a user who
// declined browser cookie reads. Anything that is not explicitly site-derived
// counts, so a value this code has never heard of resolves toward refusing.
func (c cachedCookie) isBrowserDerived() bool { return c.Provenance != provenanceSite }

// cookieCacheDir returns ~/.trvl/cookies, creating it if needed.
func cookieCacheDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".trvl", "cookies")
	return dir, os.MkdirAll(dir, 0o700)
}

// cookieCachePath returns the file path for a domain's cookie cache.
func cookieCachePath(domain string) (string, error) {
	dir, err := cookieCacheDir()
	if err != nil {
		return "", err
	}
	// Sanitize domain for filename.
	safe := ""
	for _, c := range domain {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '.' || c == '-' {
			safe += string(c)
		} else {
			safe += "_"
		}
	}
	return filepath.Join(dir, safe+".json"), nil
}

// loadCachedCookies reads persisted cookies for a URL and seeds them into
// the HTTP client's jar. Returns true if cookies were loaded and are fresh
// (saved within cookieCacheTTL).
//
// It withholds browser-derived entries when the user has declined browser
// cookie reads, and that refusal is the fifth bypass of this branch's family --
// found by review, not by us. Cookies read out of the user's browser are
// WRITTEN here: auth.go's tier-3 fallback calls saveCachedCookies right after
// tryBrowserCookieRetry succeeds. So the decline stopped fresh reads while this
// path kept replaying a previous harvest for the whole cache TTL. Setting the
// variable would have looked like it worked and changed nothing for a day.
//
// It used to refuse the WHOLE cache under that decline, because the file
// recorded no provenance: a cookie saved after an ordinary preflight and one
// copied out of Chrome were the same fields on disk. Now each entry says which
// kind of save site wrote it, so the decline drops the browser-derived entries
// and keeps the rest. An entry with no provenance -- written by a version
// before the field existed -- is treated as browser-derived, because an
// unknown origin under an opt-out has to resolve toward refusing.
func loadCachedCookies(client *http.Client, targetURL string) bool {
	declined := consent.CookiesDeclined()

	u, err := url.Parse(targetURL)
	if err != nil || u.Host == "" {
		return false
	}

	path, err := cookieCachePath(u.Host)
	if err != nil {
		return false
	}

	// #nosec G304 -- cookieCachePath derives a sanitized host filename under
	// ~/.trvl/cookies; the URL cannot escape that directory.
	data, err := os.ReadFile(path)
	if err != nil {
		return false // no cache file
	}

	var cached []cachedCookie
	if err := json.Unmarshal(data, &cached); err != nil {
		slog.Debug("cookie cache: bad JSON, ignoring", "path", path)
		return false
	}

	if len(cached) == 0 {
		return false
	}

	if declined {
		// Filter before the TTL check below, which indexes cached[0].
		kept := make([]cachedCookie, 0, len(cached))
		for _, c := range cached {
			if !c.isBrowserDerived() {
				kept = append(kept, c)
			}
		}
		if len(kept) == 0 {
			slog.Debug("cookie cache: all entries browser-derived, declined", "domain", u.Host)
			return false
		}
		cached = kept
	}

	// Check TTL against the oldest SavedAt.
	if time.Since(cached[0].SavedAt) > cookieCacheTTL {
		slog.Debug("cookie cache: expired", "domain", u.Host,
			"age", time.Since(cached[0].SavedAt).Round(time.Minute))
		return false
	}

	cookies := make([]*http.Cookie, len(cached))
	anyBrowser := false
	for i, c := range cached {
		if c.isBrowserDerived() {
			anyBrowser = true
		}
		// #nosec G124 -- restore the original upstream cookie attributes exactly;
		// adding flags here can prevent a valid request cookie from being sent.
		cookies[i] = &http.Cookie{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   c.Domain,
			Path:     c.Path,
			Expires:  c.Expires,
			Secure:   c.Secure,
			HttpOnly: c.HttpOnly,
		}
	}

	if client.Jar != nil {
		// Browser-derived contents go into a provider client's vault as such,
		// in the same critical section that commits them, so a later opt-out
		// can take them back. A set with no browser-derived entry must NOT go
		// in that way: marking the vault browser-seeded for site cookies would
		// make the next saveCachedCookies record them as browser-derived, and
		// the provenance would be sticky-browser from then on. A plain jar (the
		// throwaway one CachedCookiesForURL builds) has nothing to record on
		// and nothing to revoke, so it just takes them.
		if v := vaultOf(client); v != nil && anyBrowser {
			return v.seedFromBrowser(u, cookies)
		}
		client.Jar.SetCookies(u, cookies)
		slog.Debug("cookie cache: loaded", "domain", u.Host, "count", len(cookies))
		return true
	}
	return false
}

// CachedCookiesForURL returns the persisted, non-expired cookies for targetURL
// from the ~/.trvl/cookies cache. Returns nil when no fresh cache entry exists.
// This is the exported read path used by providers that harvest a WAF token via
// CDP once and reuse it across process restarts (token lifetime ~days).
func CachedCookiesForURL(targetURL string) []*http.Cookie {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil
	}
	client := &http.Client{Jar: jar}
	if !loadCachedCookies(client, targetURL) {
		return nil
	}
	u, err := url.Parse(targetURL)
	if err != nil || u.Host == "" {
		return nil
	}
	return jar.Cookies(u)
}

// saveCachedCookies persists the current cookies for a URL to disk.
//
// Provenance is read off the jar rather than passed in by the caller: there are
// five save sites, and a parameter at each is five chances to write the wrong
// answer. The vault already knows whether anything browser-derived was ever
// committed to it, and that is the question being recorded. A client with no
// vault -- the plain jar the headless tier-2 path saves from -- has never held
// the user's own browser cookies, so its contents are site-derived.
//
// The whole file gets one value, because a jar merges: once browser cookies
// are in it, an individual cookie handed back cannot be traced. Marking the
// mixed case browser-derived is the conservative direction, and the cost of
// getting it wrong that way is a refetch.
func saveCachedCookies(client *http.Client, targetURL string) {
	u, err := url.Parse(targetURL)
	if err != nil || u.Host == "" {
		return
	}

	if client.Jar == nil {
		return
	}

	cookies := client.Jar.Cookies(u)
	if len(cookies) == 0 {
		return
	}

	provenance := provenanceSite
	if v := vaultOf(client); v != nil && v.isBrowserSeeded() {
		provenance = provenanceBrowser
	}

	now := time.Now()
	cached := make([]cachedCookie, len(cookies))
	for i, c := range cookies {
		cached[i] = cachedCookie{
			Name:       c.Name,
			Value:      c.Value,
			Domain:     c.Domain,
			Path:       c.Path,
			Expires:    c.Expires,
			Secure:     c.Secure,
			HttpOnly:   c.HttpOnly,
			SavedAt:    now,
			Provenance: provenance,
		}
	}

	path, err := cookieCachePath(u.Host)
	if err != nil {
		return
	}

	data, err := json.Marshal(cached)
	if err != nil {
		return
	}

	if err := os.WriteFile(path, data, 0o600); err != nil {
		slog.Debug("cookie cache: write failed", "path", path, "error", logredact.Err(err))
	} else {
		slog.Debug("cookie cache: saved", "domain", u.Host, "count", len(cached))
	}
}
