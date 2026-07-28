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

	"github.com/MikkoParkkola/trvl/internal/consent"
)

const cookieCacheTTL = 24 * time.Hour

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
}

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
// It refuses everything when the user has declined browser cookie reads, and
// that refusal is the fifth bypass of this branch's family -- found by review,
// not by us. Cookies read out of the user's browser are WRITTEN here:
// auth.go's tier-3 fallback calls saveCachedCookies right after
// tryBrowserCookieRetry succeeds. So the decline stopped fresh reads while this
// path kept replaying a previous harvest for the whole cache TTL. Setting the
// variable would have looked like it worked and changed nothing for a day.
//
// It refuses the WHOLE cache, not the browser-derived entries, because this
// file records no provenance: a cookie saved after an ordinary preflight and
// one copied out of Chrome are the same three fields on disk. Distinguishing
// them is the better fix and is filed separately; refusing all of them is the
// one available now that cannot be wrong in the direction that matters. The
// cost is a refetch, paid only by users who asked for exactly this.
func loadCachedCookies(client *http.Client, targetURL string) bool {
	if consent.CookiesDeclined() {
		return false
	}

	u, err := url.Parse(targetURL)
	if err != nil || u.Host == "" {
		return false
	}

	path, err := cookieCachePath(u.Host)
	if err != nil {
		return false
	}

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

	// Check TTL against the oldest SavedAt.
	if time.Since(cached[0].SavedAt) > cookieCacheTTL {
		slog.Debug("cookie cache: expired", "domain", u.Host,
			"age", time.Since(cached[0].SavedAt).Round(time.Minute))
		return false
	}

	cookies := make([]*http.Cookie, len(cached))
	for i, c := range cached {
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
		// The cache file carries no provenance, so its contents are treated as
		// browser-derived (#534). On a provider client that means the vault
		// records them as such, in the same critical section it commits them —
		// a later opt-out then takes them back. A plain jar (the throwaway one
		// CachedCookiesForURL builds to read the file) has nothing to record on
		// and nothing to revoke, so it just takes them.
		if v := vaultOf(client); v != nil {
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

	now := time.Now()
	cached := make([]cachedCookie, len(cookies))
	for i, c := range cookies {
		cached[i] = cachedCookie{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   c.Domain,
			Path:     c.Path,
			Expires:  c.Expires,
			Secure:   c.Secure,
			HttpOnly: c.HttpOnly,
			SavedAt:  now,
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
		slog.Debug("cookie cache: write failed", "path", path, "error", err)
	} else {
		slog.Debug("cookie cache: saved", "domain", u.Host, "count", len(cached))
	}
}
