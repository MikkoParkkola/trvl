// tier2_chromedp.go is the second-line acquisition path: a cookie-refresh that
// drives the user's ALREADY-INSTALLED Chrome/Brave/Edge in headless mode via
// the Chrome DevTools Protocol (github.com/chromedp/chromedp). It bundles NO
// browser of its own — it locates an installed Chromium-family browser, launches
// it headless (no window, no focus steal), navigates the target so the JS anti-
// bot challenge can resolve, harvests the resulting cookies (cf_clearance et al)
// and writes them into the existing ~/.trvl/cookies cache for Tier-1 to reuse.
//
// This path is EXPENSIVE and it spawns a browser process, but nothing is shown
// on screen and focus is never taken. It runs by default when Tier-1 hits a
// challenge page (see IsChallengePage in tier1_client.go); set
// TRVL_NO_TIER2_CDP to decline. Single static binary is preserved:
// chromedp is pure Go and the browser is the user's own install.
package providers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"runtime"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"

	"github.com/MikkoParkkola/trvl/internal/consent"
)

// tier2EnableEnv was the opt-in for the Tier-2 CDP cookie-refresh path. The
// path now runs by default; an explicit 0/false here still turns it off.
const tier2EnableEnv = consent.Tier2LegacyEnv

// tier2DisableEnv is the opt-out, named to match TRVL_NO_BROWSER_COOKIES.
const tier2DisableEnv = consent.Tier2Env

// ErrTier2Disabled is returned when the Tier-2 path is invoked after the user
// declined it (TRVL_NO_TIER2_CDP set, or TRVL_TIER2_CDP=0). Nothing suppresses
// it — see Tier2Declined.
//
// It is the same value as consent.ErrTier2Declined, not a copy: internal/ground
// compares against it through this name, and two distinct errors that both mean
// "declined" is the drift this consolidation removed elsewhere.
var ErrTier2Disabled = consent.ErrTier2Declined

// ErrNoBrowserFound is returned when no installed Chromium-family browser can be
// located to drive headlessly.
// errTier2CookiesDeclined is what a CDP harvest returns when the user declined
// browser COOKIES rather than the CDP path itself. It wraps ErrTier2Disabled so
// every existing caller's errors.Is still holds, but it names the variable the
// user actually set: telling a TRVL_NO_BROWSER_COOKIES user to go change
// TRVL_NO_TIER2_CDP is the same misnamed-control defect as #507/#515.
var errTier2CookiesDeclined = fmt.Errorf("%w: browser cookies declined (unset %s to enable)",
	consent.ErrTier2Declined, consent.CookiesEnv)

// ErrNoBrowserFound is returned when no installed Chromium-family browser can be
// located to drive headlessly.
var ErrNoBrowserFound = errors.New("tier2 cdp: no installed Chrome/Brave/Edge/Chromium browser found")

// defaultChallengeWait is how long we let the page sit so a JS challenge can
// solve itself before harvesting cookies.
const defaultChallengeWait = 8 * time.Second

// tier2Config holds resolved options for RefreshCookiesViaCDP.
type tier2Config struct {
	challengeWait time.Duration
	execPath      string
}

// Tier2Option configures RefreshCookiesViaCDP.
type Tier2Option func(*tier2Config)

// WithTier2ChallengeWait overrides how long the page is given to resolve its JS
// challenge before cookies are harvested.
func WithTier2ChallengeWait(d time.Duration) Tier2Option {
	return func(c *tier2Config) {
		if d > 0 {
			c.challengeWait = d
		}
	}
}

// WithTier2ExecPath forces a specific browser executable instead of auto-detect.
func WithTier2ExecPath(path string) Tier2Option {
	return func(c *tier2Config) { c.execPath = path }
}

// Tier2Declined reports whether the user has EXPLICITLY asked for no headless
// browser.
//
// The Tier-2 path is on by default. It drives an already-installed Chrome,
// Brave or Edge with chromedp.Headless (runCDPCollect), so nothing appears on
// screen and focus is never taken; a user who is not looking at the process
// list cannot tell it ran. What they can tell is that a challenged search
// returned nothing, which is what leaving it off by default produced.
//
// Two ways to decline, both honoured:
//
//	TRVL_NO_TIER2_CDP  set to anything but 0/false — the opt-out, matching
//	                   TRVL_NO_BROWSER_COOKIES (#521)
//	TRVL_TIER2_CDP     explicitly 0/false — this used to be the opt-IN, so a
//	                   user who set it to 0 to keep the browser off meant it,
//	                   and flipping the default must not quietly overrule them
//
// Nothing overrules this. The predecessor of this function answered "is the
// default on?", which a caller-supplied force option was allowed to overrule —
// and every production caller passed that option (internal/ground/trainline.go,
// internal/ground/sncf.go, internal/hotels/booking_search.go), which left
// TRVL_NO_TIER2_CDP with no effect on any real search: a setting that reads as
// a privacy control and silently was not. That is the #507/#515 defect class,
// and it is why the question asked here is "did the user say no?" and why the
// drivers below are where it gets asked.
//
// The rule itself lives in internal/consent, alongside the cookie decline, so
// there is one place to look for "did the user decline this?".
func Tier2Declined() bool { return consent.Tier2Declined() }

// fileExists is overridable in tests so browser detection can be exercised
// without depending on what is installed on the build/CI host.
var fileExists = func(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// browserCandidatePaths returns the well-known install locations for
// Chromium-family browsers on the current OS, in preference order
// (Chrome, then Brave, then Edge, then Chromium).
func browserCandidatePaths() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		}
	case "windows":
		return []string{
			`C:\Program Files\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files\BraveSoftware\Brave-Browser\Application\brave.exe`,
			`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
		}
	default: // linux and friends
		return []string{
			"/usr/bin/google-chrome",
			"/usr/bin/google-chrome-stable",
			"/usr/bin/brave-browser",
			"/usr/bin/microsoft-edge",
			"/usr/bin/chromium",
			"/usr/bin/chromium-browser",
			"/snap/bin/chromium",
		}
	}
}

// detectInstalledBrowser returns the path to the first installed Chromium-family
// browser, or ("", false) if none is found.
func detectInstalledBrowser() (string, bool) {
	for _, p := range browserCandidatePaths() {
		if fileExists(p) {
			return p, true
		}
	}
	return "", false
}

// cdpRunner is the function that actually drives the browser; it is overridable
// in tests so the cookie-harvest orchestration can be verified without spawning
// a real browser. It navigates targetURL using execPath, waits challengeWait,
// and returns the page's cookies.
var cdpRunner = runCDPCollect

// RefreshCookiesViaCDP launches the user's installed browser headlessly, lets
// the anti-bot challenge at targetURL resolve, harvests the resulting cookies,
// persists them to the ~/.trvl/cookies cache, and returns them. It is the
// Tier-2 entrypoint. It runs by default and returns ErrTier2Disabled when the
// user declined (TRVL_NO_TIER2_CDP, or TRVL_TIER2_CDP=0), and also inside a
// `go test` binary unless TRVL_ALLOW_BROWSER_COOKIES is set.
func RefreshCookiesViaCDP(ctx context.Context, targetURL string, opts ...Tier2Option) ([]*http.Cookie, error) {
	cfg := tier2Config{challengeWait: defaultChallengeWait}
	for _, o := range opts {
		o(&cfg)
	}

	// An explicit decline is absolute; no option overrules it.
	if Tier2Declined() {
		return nil, ErrTier2Disabled
	}

	// And a decline of browser COOKIES stops it too. This whole path exists to
	// obtain cookies from a browser, so the cookie opt-out governs it just as
	// much as the CDP one does — a user who set TRVL_NO_BROWSER_COOKIES and
	// left the Tier-2 variable alone was still getting a browser launched, its
	// cookies harvested, and the result written to ~/.trvl/cookies. Two
	// variables, one question, and the narrower one must not be a bypass.
	if consent.CookiesDeclined() {
		return nil, errTier2CookiesDeclined
	}

	if _, err := url.Parse(targetURL); err != nil {
		return nil, err
	}

	execPath := cfg.execPath
	if execPath == "" {
		p, ok := detectInstalledBrowser()
		if !ok {
			return nil, ErrNoBrowserFound
		}
		execPath = p
	}

	raw, err := cdpRunner(ctx, execPath, targetURL, cfg.challengeWait)
	if err != nil {
		return nil, err
	}

	harvested := convertNetworkCookies(raw)

	// The CDP read takes seconds — a challenge wait, a cold profile — so the
	// decline that matters is the one in force NOW, not the one checked before
	// the browser started. Refuse before the cache write, or a decline arriving
	// mid-read still ends with the user's session on disk for the whole TTL.
	//
	// Asked directly rather than through permittedAfterRead: that helper answers
	// nil for BOTH a refusal and a harvest that legitimately found no cookies,
	// and turning the second one into a decline error is the same conflation the
	// refusal logging exists to prevent, only inverted.
	if consent.CookiesDeclined() {
		return nil, errTier2CookiesDeclined
	}
	persistCookiesToCache(targetURL, harvested)
	return harvested, nil
}

// runCDPCollect drives an installed browser headlessly via chromedp and returns
// the cookies present after the challenge wait. No window is shown and focus is
// never stolen (Headless + DefaultExecAllocatorOptions).
func runCDPCollect(ctx context.Context, execPath, targetURL string, challengeWait time.Duration) ([]*network.Cookie, error) {
	// An explicit decline is absolute and is checked HERE, on the function that
	// actually spawns the browser, so a caller that reaches past the entrypoint
	// still cannot start one.
	if Tier2Declined() {
		return nil, ErrTier2Disabled
	}
	if consent.CookiesDeclined() {
		return nil, errTier2CookiesDeclined
	}

	// Now that Tier-2 is on by default, this is what keeps `go test` from
	// launching a real browser on a build host. It sits on the driver rather
	// than the entrypoint so tests that stub cdpRunner still exercise the
	// orchestration. Mirrors the browserCookiesForURL guard.
	if os.Getenv("TRVL_ALLOW_BROWSER_COOKIES") == "" && isTestBinary() {
		return nil, ErrTier2Disabled
	}

	allocOpts := append([]chromedp.ExecAllocatorOption{},
		chromedp.DefaultExecAllocatorOptions[:]...)
	allocOpts = append(allocOpts,
		chromedp.ExecPath(execPath),
		chromedp.Headless,
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
		chromedp.Flag("disable-background-networking", true),
		chromedp.Flag("disable-extensions", true),
	)

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, allocOpts...)
	defer cancelAlloc()

	taskCtx, cancelTask := chromedp.NewContext(allocCtx)
	defer cancelTask()

	var cookies []*network.Cookie
	err := chromedp.Run(taskCtx,
		network.Enable(),
		chromedp.Navigate(targetURL),
		chromedp.Sleep(challengeWait),
		chromedp.ActionFunc(func(ctx context.Context) error {
			c, err := network.GetCookies().Do(ctx)
			if err != nil {
				return err
			}
			cookies = c
			return nil
		}),
	)
	if err != nil {
		return nil, err
	}
	return cookies, nil
}

// convertNetworkCookies maps CDP network.Cookie values to net/http cookies.
func convertNetworkCookies(in []*network.Cookie) []*http.Cookie {
	out := make([]*http.Cookie, 0, len(in))
	for _, c := range in {
		if c == nil {
			continue
		}
		hc := &http.Cookie{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   c.Domain,
			Path:     c.Path,
			Secure:   c.Secure,
			HttpOnly: c.HTTPOnly,
		}
		if c.Expires > 0 {
			hc.Expires = time.Unix(int64(c.Expires), 0)
		}
		out = append(out, hc)
	}
	return out
}

// persistCookiesToCache writes harvested cookies into the existing
// ~/.trvl/cookies cache by routing them through a throwaway std client+jar and
// reusing saveCachedCookies, so Tier-1 picks them up on the next request.
func persistCookiesToCache(targetURL string, cookies []*http.Cookie) {
	if len(cookies) == 0 {
		return
	}
	u, err := url.Parse(targetURL)
	if err != nil || u.Host == "" {
		return
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return
	}
	jar.SetCookies(u, cookies)
	saveCachedCookies(&http.Client{Jar: jar}, targetURL)
}
