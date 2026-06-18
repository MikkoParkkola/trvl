// tier2_chromedp.go is the second-line acquisition path: a cookie-refresh that
// drives the user's ALREADY-INSTALLED Chrome/Brave/Edge in headless mode via
// the Chrome DevTools Protocol (github.com/chromedp/chromedp). It bundles NO
// browser of its own — it locates an installed Chromium-family browser, launches
// it headless (no window, no focus steal), navigates the target so the JS anti-
// bot challenge can resolve, harvests the resulting cookies (cf_clearance et al)
// and writes them into the existing ~/.trvl/cookies cache for Tier-1 to reuse.
//
// This path is EXPENSIVE and visible (it spawns a browser process), so it is
// gated behind an explicit opt-in (TRVL_TIER2_CDP=1, or WithTier2Force) and is
// meant to be invoked only when Tier-1 returns a challenge page
// (see IsChallengePage in tier1_client.go). Single static binary is preserved:
// chromedp is pure Go and the browser is the user's own install.
package providers

import (
	"context"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"runtime"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// tier2EnableEnv opts into the Tier-2 CDP cookie-refresh path.
const tier2EnableEnv = "TRVL_TIER2_CDP"

// ErrTier2Disabled is returned when RefreshCookiesViaCDP is invoked without the
// opt-in (TRVL_TIER2_CDP unset/0 and WithTier2Force not used).
var ErrTier2Disabled = errors.New("tier2 cdp cookie-refresh disabled (set TRVL_TIER2_CDP=1 to enable)")

// ErrNoBrowserFound is returned when no installed Chromium-family browser can be
// located to drive headlessly.
var ErrNoBrowserFound = errors.New("tier2 cdp: no installed Chrome/Brave/Edge/Chromium browser found")

// defaultChallengeWait is how long we let the page sit so a JS challenge can
// solve itself before harvesting cookies.
const defaultChallengeWait = 8 * time.Second

// tier2Config holds resolved options for RefreshCookiesViaCDP.
type tier2Config struct {
	force         bool
	challengeWait time.Duration
	execPath      string
}

// Tier2Option configures RefreshCookiesViaCDP.
type Tier2Option func(*tier2Config)

// WithTier2Force bypasses the TRVL_TIER2_CDP env opt-in (e.g. when the caller
// has already confirmed escalation interactively).
func WithTier2Force() Tier2Option {
	return func(c *tier2Config) { c.force = true }
}

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

// Tier2Enabled reports whether the Tier-2 opt-in is set via TRVL_TIER2_CDP.
func Tier2Enabled() bool {
	v := os.Getenv(tier2EnableEnv)
	return v == "1" || v == "true" || v == "yes"
}

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
// Tier-2 entrypoint and is gated: without the opt-in it returns ErrTier2Disabled.
func RefreshCookiesViaCDP(ctx context.Context, targetURL string, opts ...Tier2Option) ([]*http.Cookie, error) {
	cfg := tier2Config{challengeWait: defaultChallengeWait}
	for _, o := range opts {
		o(&cfg)
	}

	if !cfg.force && !Tier2Enabled() {
		return nil, ErrTier2Disabled
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

	cookies := convertNetworkCookies(raw)
	persistCookiesToCache(targetURL, cookies)
	return cookies, nil
}

// runCDPCollect drives an installed browser headlessly via chromedp and returns
// the cookies present after the challenge wait. No window is shown and focus is
// never stolen (Headless + DefaultExecAllocatorOptions).
func runCDPCollect(ctx context.Context, execPath, targetURL string, challengeWait time.Duration) ([]*network.Cookie, error) {
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
