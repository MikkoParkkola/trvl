package providers

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sync"

	"github.com/MikkoParkkola/trvl/internal/cookies"
)

// cookieVault is the cookie jar a providerClient uses. It is an http.CookieJar,
// so net/http drives it exactly like the standard jar, and it adds the two
// operations the browser opt-out needs: seed-with-provenance and revoke.
//
// Round 8 of review found the reason it has to be a jar rather than a flag next
// to one. The previous shape stored provenance in an atomic on the client and
// revoked by assigning a fresh jar over pc.client.Jar. That has two defects and
// they compound:
//
//   - Seeding and revocation took no common lock. A search that had already
//     started reading the user's browser could commit its cookies *after* a
//     decline had run, and the discard — having seen the flag still false —
//     had already returned. The setting appeared applied while browser cookies
//     kept going out.
//   - Replacing pc.client.Jar is a write to a pointer that in-flight requests
//     read inside http.Client.send. A data race, not merely a logical one.
//
// Both die if the jar is installed once and never reassigned, and if entering
// and leaving are the same critical section. Revocation swaps the *inner* jar
// while the vault address the client holds stays fixed; the decline is
// re-checked under the lock immediately before cookies are committed, so a read
// that began before the opt-out cannot land after it.
//
// Only browser-derived cookies are tracked. An ordinary preflight session never
// touched the user's browser, and discarding it would make a browser control
// into a general cache-buster.
type cookieVault struct {
	mu            sync.Mutex
	jar           http.CookieJar
	browserSeeded bool
}

// newCookieVault returns a vault wrapping a fresh standard jar. It returns nil
// only if the standard jar cannot be built, which cookiejar.New does not do for
// nil options; callers treat nil as "no vault" and refuse to seed.
func newCookieVault() *cookieVault {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil
	}
	return &cookieVault{jar: jar}
}

// SetCookies implements http.CookieJar. This is the ordinary server-driven
// path: Set-Cookie headers from responses. It records no provenance, because
// none of it came from the user's browser.
func (v *cookieVault) SetCookies(u *url.URL, list []*http.Cookie) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.jar.SetCookies(u, list)
}

// Cookies implements http.CookieJar.
func (v *cookieVault) Cookies(u *url.URL) []*http.Cookie {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.jar.Cookies(u)
}

// seedFromBrowser commits cookies that came from the user's browser — read live
// via kooky/CDP, recovered from a browser window, or restored from the on-disk
// cache, which carries no provenance and so is treated as browser-derived
// (#534) — and records that provenance in the same critical section.
//
// The decline is re-checked here, under the lock, rather than only at the call
// site: the browser read that produced these cookies takes seconds, and the
// user may have declined during it. Returns false when nothing was committed.
func (v *cookieVault) seedFromBrowser(u *url.URL, list []*http.Cookie) bool {
	if v == nil || u == nil || len(list) == 0 {
		return false
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if cookies.Disabled() {
		return false
	}
	v.jar.SetCookies(u, list)
	v.browserSeeded = true
	return true
}

// discardBrowserSeeded drops every cookie in the vault if any of them came from
// the browser, and clears the provenance. Returns true when something was
// discarded.
//
// The whole jar goes, not the browser-derived entries alone: net/http/cookiejar
// exposes no removal, and a WAF session is a single interdependent set anyway —
// the token that was harvested and the token minted in reply to it are not
// separable. Replacing the inner jar is safe here in a way replacing the client's
// jar was not, because the client's pointer never changes.
func (v *cookieVault) discardBrowserSeeded() bool {
	if v == nil {
		return false
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.browserSeeded {
		return false
	}
	var next http.CookieJar
	if jar, err := cookiejar.New(nil); err == nil {
		next = jar
	} else {
		// Unreachable with nil options. Leaving the old jar in place would keep
		// exactly the cookies this call exists to remove, so an empty stand-in
		// that still accepts Set-Cookie is the safe failure.
		next = emptyJar{}
	}
	v.jar = next
	v.browserSeeded = false
	return true
}

// isBrowserSeeded reports whether the vault currently holds browser-derived
// cookies.
func (v *cookieVault) isBrowserSeeded() bool {
	if v == nil {
		return false
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.browserSeeded
}

// emptyJar is the last-resort stand-in described in discardBrowserSeeded. It
// accepts and forgets, which keeps requests working without retaining anything.
type emptyJar struct{}

func (emptyJar) SetCookies(*url.URL, []*http.Cookie) {}
func (emptyJar) Cookies(*url.URL) []*http.Cookie     { return nil }

// vaultOf returns the vault backing a client's jar, or nil when the client uses
// a plain jar. Browser seeding refuses to proceed on a nil vault: cookies from
// the user's browser only ever enter a jar that can give them back.
func vaultOf(client *http.Client) *cookieVault {
	if client == nil {
		return nil
	}
	v, _ := client.Jar.(*cookieVault)
	return v
}
