// challenge.go implements HEADLESS-FIRST anti-bot challenge resolution. When a
// provider preflight is blocked by a JS/WAF interstitial, we first drive the
// user's installed Chrome/Brave HEADLESS (no visible window, no focus steal) via
// the Chrome DevTools Protocol to let the challenge solve itself silently. Only
// when a genuinely interactive captcha (Datadome / hCaptcha / reCAPTCHA) remains
// — something a headless browser cannot clear without a human — does the caller
// fall back to opening a VISIBLE window.
//
// This is the implementation of MIK-6218: make browser-assisted challenge
// resolution headless-first so the focus-stealing window is a last resort.
package providers

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"

	"github.com/MikkoParkkola/trvl/internal/consent"
)

// ChallengeStatus is the outcome of a headless-first challenge resolution.
type ChallengeStatus int

const (
	// ChallengeUnresolved means the headless attempt neither cleared the
	// challenge nor positively identified an interactive captcha (e.g. the
	// browser errored or produced an inconclusive page).
	ChallengeUnresolved ChallengeStatus = iota
	// ChallengeCleared means the headless browser silently solved the challenge
	// and usable cookies are ready — NO visible window is needed.
	ChallengeCleared
	// ChallengeNeedsHuman means an interactive captcha interstitial remains that
	// a headless browser cannot solve; the caller may open a visible window so a
	// human can complete it.
	ChallengeNeedsHuman
)

// String renders the status for logs.
func (s ChallengeStatus) String() string {
	switch s {
	case ChallengeCleared:
		return "cleared"
	case ChallengeNeedsHuman:
		return "needs_human"
	default:
		return "unresolved"
	}
}

// ChallengeResult is the typed outcome returned by ResolveChallenge. On
// ChallengeCleared the Cookies are ready (and have been persisted to the
// ~/.trvl/cookies cache). On ChallengeNeedsHuman, Marker names the detected
// captcha vendor.
type ChallengeResult struct {
	Status  ChallengeStatus
	Cookies []*http.Cookie
	Marker  string
}

// captchaMarker pairs a captcha vendor with substrings that, when present in a
// post-headless page, indicate an INTERACTIVE interstitial requiring a human.
type captchaMarker struct {
	Vendor  string
	Needles []string
}

// interactiveCaptchaMarkers lists the known interactive-interstitial signatures.
// These are vendors whose challenge cannot be cleared headlessly because they
// demand a human gesture (image grid, slider, checkbox). Lower-cased needles.
var interactiveCaptchaMarkers = []captchaMarker{
	// Datadome serves its interactive captcha from geo.captcha-delivery.com.
	{Vendor: "datadome", Needles: []string{"geo.captcha-delivery.com", "captcha-delivery.com"}},
	// hCaptcha widget host + the rendered container class.
	{Vendor: "hcaptcha", Needles: []string{"hcaptcha.com", "h-captcha"}},
	// Google reCAPTCHA api script + the rendered container class.
	{Vendor: "recaptcha", Needles: []string{"google.com/recaptcha", "recaptcha/api.js", "g-recaptcha"}},
}

// DetectInteractiveCaptcha reports whether body contains markers of a known
// interactive captcha interstitial (Datadome, hCaptcha, reCAPTCHA) that a
// headless browser cannot clear and which therefore requires a human. When it
// returns true, the second value names the matched vendor.
//
// Pure and deterministic — the testable gate that decides CLEARED vs
// NEEDS_HUMAN. A clean page (no markers) returns (false, "").
func DetectInteractiveCaptcha(body []byte) (bool, string) {
	if len(body) == 0 {
		return false, ""
	}
	lower := strings.ToLower(string(body))
	for _, m := range interactiveCaptchaMarkers {
		for _, needle := range m.Needles {
			if strings.Contains(lower, needle) {
				return true, m.Vendor
			}
		}
	}
	return false, ""
}

// cdpChallengeRunner drives an installed browser HEADLESS and returns both the
// harvested cookies AND the final page HTML so the caller can decide CLEARED vs
// NEEDS_HUMAN. Overridable in tests so the orchestration can be exercised
// offline without spawning a real browser.
var cdpChallengeRunner = runCDPChallenge

// ResolveChallenge launches the user's installed browser HEADLESS (no window, no
// focus steal), lets the anti-bot challenge at targetURL resolve, then inspects
// the resulting page:
//
//   - If an interactive captcha interstitial remains (Datadome / hCaptcha /
//     reCAPTCHA), it returns ChallengeNeedsHuman with the detected vendor — the
//     caller may then open a VISIBLE window for a human to finish.
//   - Otherwise it treats the challenge as silently cleared, persists the
//     harvested cookies to the ~/.trvl/cookies cache for Tier-1 reuse, and
//     returns ChallengeCleared.
//
// Gating: the path runs by default and returns ErrTier2Disabled when the user
// declined it (TRVL_NO_TIER2_CDP, or TRVL_TIER2_CDP=0). A browser-cookie decline
// (TRVL_NO_BROWSER_COOKIES) stops it too — it reads the user's browser session
// whatever the reason for the call. It also refuses inside a `go test` binary,
// so the suite stays offline; TRVL_ALLOW_BROWSER_COOKIES lifts that. It reuses
// the Tier2Option set so callers configure it the same way as the lower-level
// CDP refresh.
func ResolveChallenge(ctx context.Context, targetURL string, opts ...Tier2Option) (*ChallengeResult, error) {
	cfg := tier2Config{challengeWait: defaultChallengeWait}
	for _, o := range opts {
		o(&cfg)
	}

	// An explicit decline is absolute; see RefreshCookiesViaCDP.
	if Tier2Declined() {
		return nil, ErrTier2Disabled
	}

	// A browser-cookie decline is equally absolute here. This path launches the
	// user's own browser and takes its session, which is precisely what the
	// decline refused — the fact that it is reached to clear a challenge rather
	// than to refresh cookies does not make the session any less theirs.
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

	rawCookies, html, err := cdpChallengeRunner(ctx, execPath, targetURL, cfg.challengeWait)
	if err != nil {
		return nil, err
	}

	cookies := convertNetworkCookies(rawCookies)

	// The decline can also arrive while the challenge is resolving. Ask the
	// consent state directly rather than inferring refusal from an empty
	// harvest, and refuse before either exit: the cleared branch persists the
	// cookies to the cache, and the needs-human branch hands them back to the
	// caller. Both are transmissions of a session the user has since refused.
	if consent.CookiesDeclined() {
		return nil, errTier2CookiesDeclined
	}

	if needsHuman, marker := DetectInteractiveCaptcha([]byte(html)); needsHuman {
		// The headless pass could not clear an interactive captcha. Do NOT
		// persist — the session is not authenticated. Signal the caller to open
		// a visible window.
		return &ChallengeResult{Status: ChallengeNeedsHuman, Cookies: cookies, Marker: marker}, nil
	}

	// No interactive interstitial remained — treat as silently cleared and make
	// the cookies available to Tier-1 on the next request.
	persistCookiesToCache(targetURL, cookies)
	return &ChallengeResult{Status: ChallengeCleared, Cookies: cookies}, nil
}

// runCDPChallenge is the production cdpChallengeRunner: it drives an installed
// browser headlessly via chromedp and returns the cookies plus the final page
// HTML present after the challenge wait. No window is shown and focus is never
// stolen (Headless + DefaultExecAllocatorOptions; no activation/foreground
// flags).
func runCDPChallenge(ctx context.Context, execPath, targetURL string, challengeWait time.Duration) ([]*network.Cookie, string, error) {
	// Same reason as runCDPCollect: an explicit decline is absolute and is
	// enforced on the spawning function, so a caller that reaches past
	// ResolveChallenge still cannot start a browser.
	if Tier2Declined() {
		return nil, "", ErrTier2Disabled
	}

	// And the same for the browser-cookie decline, on the spawning function, so
	// a caller that reaches past ResolveChallenge still cannot start a browser.
	if consent.CookiesDeclined() {
		return nil, "", errTier2CookiesDeclined
	}

	// Same reason as runCDPCollect: with Tier-2 on by default, the driver is
	// what must refuse inside a test binary, so stubbed tests still run.
	if os.Getenv("TRVL_ALLOW_BROWSER_COOKIES") == "" && isTestBinary() {
		return nil, "", ErrTier2Disabled
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
	var html string
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
		// Best-effort outer HTML capture for captcha-marker detection. A failure
		// here must not abort the harvest, so it is tolerated.
		chromedp.ActionFunc(func(ctx context.Context) error {
			_ = chromedp.OuterHTML("html", &html, chromedp.ByQuery).Do(ctx)
			return nil
		}),
	)
	if err != nil {
		return nil, "", err
	}
	return cookies, html, nil
}

// headlessFirstResolve is the seam used by tryBrowserEscapeHatch to attempt a
// silent headless resolution before falling back to a visible window.
// Overridable in tests. The default refuses to spawn a real browser inside a
// `go test` binary so the default suite stays offline/deterministic (mirrors
// the browserCookiesForURL test guard).
var headlessFirstResolve = defaultHeadlessFirstResolve

func defaultHeadlessFirstResolve(ctx context.Context, targetURL string) (*ChallengeResult, error) {
	if os.Getenv("TRVL_ALLOW_BROWSER_COOKIES") == "" && isTestBinary() {
		return nil, ErrTier2Disabled
	}
	return ResolveChallenge(ctx, targetURL)
}
