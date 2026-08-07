// Package cookies extracts browser cookies for use in HTTP requests.
// It tries the nab CLI/MCP tool first (which handles decryption and keychain
// access), then falls back to no-op. This avoids CGO and keychain dependencies
// in the main binary.
package cookies

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/MikkoParkkola/trvl/internal/consent"
	"github.com/MikkoParkkola/trvl/internal/logredact"
	trvlnab "github.com/MikkoParkkola/trvl/internal/nab"
	"github.com/MikkoParkkola/trvl/internal/safeexec"
	"golang.org/x/sync/singleflight"
	"os/exec"
)

// nabCookieTimeout bounds a cookie export. Generous compared with a credential
// read, because nab decrypts a whole cookie jar, but finite: this runs inside a
// search the user is waiting on.
const nabCookieTimeout = 5 * time.Second

// nabCookieBudget caps the whole attempt across every browser tried, so a
// wedged helper cannot be paid for twice in one search.
const nabCookieBudget = 6 * time.Second

var (
	browserAuthNow   = time.Now
	browserAuthStart = func(name string, args ...string) error {
		return exec.Command(name, args...).Start()
	}
)

// BrowserCookies extracts cookies for a domain from the user's default browser.
// It tries Brave first, then Chrome.
// Returns a Cookie header value (e.g. "datadome=abc; _session=xyz").
// Returns empty string if no cookies found or nab is not available.
//
// The whole attempt, across both browsers, shares one budget. Trying them in
// sequence with a per-browser deadline would let a wedged nab cost twice over,
// on a search the user is waiting on and did not ask to spend on cookies.
func BrowserCookies(domain string) string {
	return BrowserCookiesContext(context.Background(), domain)
}

// DisableEnv turns off every read of the user's browser cookie stores.
//
// The name is kept here for this package's callers; the variable and the rule
// for reading it live in internal/consent, which both this package and
// internal/nab can see. They used to hold a copy each, because internal/cookies
// imports internal/nab and sharing the other way would close an import cycle.
const DisableEnv = consent.CookiesEnv

// Disabled reports whether the user has declined browser cookie reads.
func Disabled() bool { return consent.CookiesDeclined() }

// BrowserCookiesContext is BrowserCookies with caller cancellation honoured.
// A request that has gone away must not keep a helper running on its behalf.
func BrowserCookiesContext(ctx context.Context, domain string) string {
	// Before the suppression cache and before the singleflight, because a user
	// who has declined must not have a helper started for them by a concurrent
	// caller that arrived first.
	if Disabled() {
		return ""
	}

	if _, ok := cookieSuppressed(domain); ok {
		return ""
	}

	// A caller that is already gone gets nothing started on its behalf. Without
	// this, entering the flight below would launch a helper for a request nobody
	// is waiting on — and a client that cancels and retries could do it again on
	// every attempt.
	if ctx.Err() != nil {
		return ""
	}

	// Collapse concurrent extraction for the same domain. A WAF challenge fires
	// for every property in a result set at once, so without this a single
	// search could start two nab process trees per property — the accumulation
	// this whole change exists to stop.
	//
	// DoChan rather than Do: Do blocks, so a caller that had gone away would
	// still sit here for the full budget before anyone noticed.
	ch := cookieGroup.DoChan(domain, func() (any, error) {
		if _, ok := cookieSuppressed(domain); ok {
			return "", nil
		}

		// The shared extraction outlives any single caller, so it must not
		// inherit one caller's cancellation; its own budget bounds it.
		bctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), nabCookieBudget)
		defer cancel()

		var readErr error
		for _, browser := range []string{"brave", "chrome"} {
			if bctx.Err() != nil {
				break
			}
			c, err := extractViaNab(bctx, browser, domain)
			if err != nil {
				// Keep going: Brave failing says nothing about Chrome. The
				// causes are joined so the report below can tell a machine
				// without the helper from one that has it and cannot read.
				readErr = errors.Join(readErr, err)
				continue
			}
			if c != "" {
				slog.Debug("browser cookies found", "browser", browser, "domain", domain)
				return c, nil
			}
		}

		// Nothing usable. Suppress retries briefly: a WAF challenge repeats
		// across every property in a result set, and without this each one
		// re-pays the full budget for the same answer.
		noteCookieFailure(domain)
		reportCookieReadFailure(domain, readErr)
		return "", nil
	})

	select {
	case <-ctx.Done():
		// This caller stops waiting immediately. The extraction continues for
		// whoever is left, and its result still populates the suppression map.
		return ""
	case res := <-ch:
		c, _ := res.Val.(string)
		return c
	}
}

// cookieNegTTL suppresses repeated extraction attempts for a domain after a
// failure. Short, because a user who unlocks their browser mid-session should
// not have to wait out a long penalty.
const cookieNegTTL = 30 * time.Second

var (
	cookieGroup    singleflight.Group
	cookieNegMu    sync.Mutex
	cookieNegUntil = map[string]time.Time{}
)

func cookieSuppressed(domain string) (error, bool) {
	cookieNegMu.Lock()
	defer cookieNegMu.Unlock()
	until, ok := cookieNegUntil[domain]
	if !ok || !time.Now().Before(until) {
		return nil, false
	}
	return errCookieSuppressed, true
}

func noteCookieFailure(domain string) {
	cookieNegMu.Lock()
	cookieNegUntil[domain] = time.Now().Add(cookieNegTTL)
	cookieNegMu.Unlock()
}

// resetCookieCache clears the suppression map. Test-only.
func resetCookieCache() {
	cookieNegMu.Lock()
	domains := make([]string, 0, len(cookieNegUntil))
	for d := range cookieNegUntil {
		domains = append(domains, d)
	}
	cookieNegUntil = map[string]time.Time{}
	cookieNegMu.Unlock()
	for _, d := range domains {
		cookieGroup.Forget(d)
	}
	cookieWarnMu.Lock()
	cookieWarnSeen = map[string]bool{}
	cookieWarnMu.Unlock()
}

var errCookieSuppressed = errors.New("cookie extraction suppressed after a recent failure")

// The two ways the reader can fail to work at all, as opposed to running fine
// and finding nothing. #529: the old reader collapsed both of these and the
// ordinary miss into a single empty string, so a provider fallback that could
// never return a cookie looked exactly like a user who happens not to be logged
// in, and a search degraded to blocked-or-empty results with nothing said.
var (
	// errNabUnavailable means the helper is not installed. Every cookie
	// fallback on this machine is a no-op until that changes, so it is worth
	// telling the user once.
	errNabUnavailable = errors.New("the nab helper is not installed, so browser cookies cannot be read")
	// errNabExtractFailed means the helper ran and could not produce cookies:
	// a Keychain denial, a locked profile, a timeout. Distinct from the above
	// because the user's fix is different, and distinct from an ordinary miss
	// because there is a fix at all.
	errNabExtractFailed = errors.New("the browser cookie store could not be read")
)

var (
	cookieWarnMu   sync.Mutex
	cookieWarnSeen = map[string]bool{}
)

// warnOnce reports whether cause has yet to be reported in this process.
//
// Once per process and not once per domain, which is the "do not spam" half of
// #529. Both causes are machine-level facts: nab is installed or it is not, the
// Keychain is unlocked or it is not. A WAF challenge fires for every property in
// a result set, so a per-domain signal would put one line per property on the
// user's terminal to say the same thing. The 30s suppression above is the wrong
// gate for this — it is scoped per domain and exists to bound cost, not noise.
func warnOnce(cause string) bool {
	cookieWarnMu.Lock()
	defer cookieWarnMu.Unlock()
	if cookieWarnSeen[cause] {
		return false
	}
	cookieWarnSeen[cause] = true
	return true
}

// announceCookieRead tells the user that trvl is about to read their browser
// cookie store, and how to refuse.
//
// The opt-out settled in #521 is only a control for someone who already knows it
// exists. Everything else about this read is silent: it starts from an ordinary
// search, it reaches a local credential store, and on macOS it can raise a
// Keychain prompt whose own text says nothing about trvl. A user who never asked
// for any of that had no way to see it coming, which was the actual complaint in
// #507 and the half of #521 the env var does not answer.
//
// It says three things and each is established at this point in the code: the
// helper exists (LookupPath returned above), the domain is the one about to be
// passed to it, and the variable declines it. It does NOT say a browser will be
// read successfully, that cookies exist, or that any search will benefit --
// nothing here establishes those, and promising them is the defect #528 exists
// to stop, in a second place.
//
// Once per process, via the same gate as the failure warnings and for the same
// reason: a WAF challenge fires for every property in a result set, and a notice
// repeated per domain is a notice the user learns to skip.
func announceCookieRead(domain string) {
	if !warnOnce("browser-cookie-read") {
		return
	}
	slog.Warn("about to read your browser's cookie store so this search can use your logged-in session; on macOS this can ask for Keychain access",
		"domain", domain,
		"decline_with", consent.CookiesEnv+"=1")
}

// reportCookieReadFailure surfaces the cannot-work outcomes and stays quiet
// about everything else.
//
// A nil err means the reader ran and this domain simply has no cookies, which is
// the ordinary case and not a failure. Reporting it would drown the two cases
// that are.
//
// A decline reaches here with a nil err by construction — extractViaNab returns
// no error when the user has opted out — and that is deliberate. Warning on the
// decline path would announce that a read was attempted and that cookies were or
// were not there, which is the disclosure the opt-out exists to prevent (#507,
// #521, #530). Refusals stay at Debug, as they do in the provider-side reader.
func reportCookieReadFailure(domain string, err error) {
	if err == nil {
		slog.Debug("no browser cookies for domain", "domain", domain)
		return
	}
	switch {
	case errors.Is(err, errNabUnavailable):
		// Checked first: it is the more actionable of the two and it subsumes
		// the other, since a helper that is absent cannot also have failed for
		// an interesting reason.
		if warnOnce("nab-unavailable") {
			slog.Warn("browser cookie fallback unavailable: nab is not installed, so searches that need your logged-in session will fall back to blocked or empty results",
				"domain", domain, "err", logredact.Err(err))
		}
	default:
		if warnOnce("nab-failed") {
			slog.Warn("browser cookie fallback failed: nab could not read your browser cookie store, so searches that need your logged-in session may return blocked or empty results",
				"domain", domain, "err", logredact.Err(err))
		}
	}
}

// extractViaNab uses the nab CLI to export cookies for the given browser and domain.
// nab handles keychain access and AES decryption transparently.
//
// It runs bounded and terminal-detached for the same reason the AF-KLM
// credential lookup does (#507). This is reached from ordinary hotel and rail
// searches — a Booking.com WAF challenge triggers it, as do Trainline, Eurostar
// and SNCF — so the user did not ask for it and must not pay for it. Decrypting
// Chrome or Brave cookies requires macOS Keychain access, which can raise its
// own permission prompt; unbounded and terminal-attached, that is the exact
// shape that produced #507: a credential prompt appearing mid-search and a
// helper that never returns.
//
// It returns three outcomes and not one, which is #529. An empty string with a
// nil error means the read worked and this domain has no cookies; an error means
// the read could not happen, and which error says whether the fix is installing
// the helper or unlocking the store. Collapsing those into "" left every caller
// with a fallback that could be permanently dead and look idle.
//
// The decline is re-checked here even though the only caller already checked it.
// This function is the seam where the helper process is actually started, and
// three rounds of review on #521 each found the same failure shape: a gate placed
// on the caller rather than the seam, and then a caller that did not have it. The
// duplicate check costs one env read on a path that is about to fork a process.
// It returns a nil error: a refusal is not a failure to report.
func extractViaNab(ctx context.Context, browser, domain string) (string, error) {
	if Disabled() {
		return "", nil
	}

	nabPath, err := trvlnab.LookupPath()
	if err != nil {
		return "", fmt.Errorf("%w: %w", errNabUnavailable, err)
	}

	// Said before the helper is started, not after, because a disclosure that
	// arrives once the Keychain prompt is already on screen is not a disclosure.
	announceCookieRead(domain)

	cmd, _, cancel := safeexec.Command(ctx, nabCookieTimeout,
		nabPath, "cookies", "export", domain, "--cookies", browser)
	defer cancel()

	out, err := safeexec.Output(cmd)
	if err != nil {
		return "", fmt.Errorf("%w (%s): %w", errNabExtractFailed, browser, err)
	}
	// Ran clean and said nothing: the user has no cookies for this domain. An
	// ordinary miss, reported as one.
	if len(out) == 0 {
		return "", nil
	}
	return parseNetscapeCookies(string(out)), nil
}

// parseNetscapeCookies converts Netscape cookie file format into a Cookie header value.
// Each non-comment line is tab-delimited:
//
//	domain  includeSubdomains  path  secure  expiry  name  value
func parseNetscapeCookies(data string) string {
	var pairs []string
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) >= 7 {
			name, value := parts[5], parts[6]
			if name != "" {
				pairs = append(pairs, name+"="+value)
			}
		}
	}
	return strings.Join(pairs, "; ")
}

// ApplyCookies adds browser cookies to an HTTP request for the given domain.
// It is a no-op when no cookies are found.
func ApplyCookies(req *http.Request, domain string) {
	// The request carries the caller's context; use it rather than starting a
	// detached lookup on behalf of a request that may already be cancelled.
	c := BrowserCookiesContext(req.Context(), domain)
	if c == "" {
		return
	}
	// Re-checked after the read, not only inside it: reading the browser stores
	// takes seconds (Keychain unlock, cold profile), and a decline that lands
	// during the read must still win. See AttachBrowserCookies.
	if Disabled() {
		return
	}
	req.Header.Set("Cookie", c)
}

// AttachBrowserCookies attaches cookies that came from the user's browser to a
// request, unless browser access has been declined. It reports whether anything
// was attached.
//
// It exists because a cookie jar is not the only way browser credentials reach
// the wire: several providers read the browser once and then set the cookies on
// the request directly, past any jar. Round 9 of review found two such paths
// (Trainline's Tier-1 retry, Rome2Rio's Cloudflare path) still sending browser
// cookies after an opt-out, for the same reason the jar did before it grew a
// vault — the consent check sat before the slow read rather than after it.
//
// So the check belongs here, at the last point before transmission, where a
// decline that arrives while the browser is being read still wins.
func AttachBrowserCookies(req *http.Request, list []*http.Cookie) bool {
	if req == nil || len(list) == 0 || Disabled() {
		return false
	}
	for _, ck := range list {
		if ck != nil {
			req.AddCookie(ck)
		}
	}
	return true
}

// IsCaptchaResponse reports whether an HTTP response is a Datadome CAPTCHA block
// and returns the CAPTCHA URL when true.
func IsCaptchaResponse(statusCode int, body []byte) (bool, string) {
	if statusCode != http.StatusForbidden {
		return false, ""
	}
	bodyStr := string(body)
	if !strings.Contains(bodyStr, "captcha-delivery.com") {
		return false, ""
	}
	// Extract redirect URL from JSON: {"url":"https://..."}
	const marker = `"url":"`
	if idx := strings.Index(bodyStr, marker); idx >= 0 {
		start := idx + len(marker)
		if end := strings.Index(bodyStr[start:], `"`); end > 0 {
			return true, bodyStr[start : start+end]
		}
	}
	return true, ""
}

// browserAuthOpened tracks when a domain was last opened for auth,
// preventing repeated browser popups within the cooldown period.
var browserAuthOpened = struct {
	mu      sync.Mutex
	domains map[string]time.Time
}{domains: make(map[string]time.Time)}

// browserAuthCooldown is the minimum time between opening the browser for the
// same domain. Set to 24 hours — once opened, never again until tomorrow.
const browserAuthCooldown = 24 * time.Hour

// ErrBrowserAuthDeclined reports that the user has opted out of trvl touching
// their own browsers, so no window was opened.
var ErrBrowserAuthDeclined = errors.New("browser auth declined: user opted out of browser access")

// OpenBrowserForAuth opens url in the user's default browser so they can
// complete a CAPTCHA or login challenge. Suppresses repeated opens for the
// same domain within 24 hours. Returns an error if the browser could not
// be launched, or nil if suppressed by cooldown.
//
// Gated on CookiesDeclined. This launches the user's real, visible browser —
// the one they are logged into — which is exactly what that decline covers.
// The other decline (Tier2) governs the empty-profile headless browser and is
// deliberately not consulted here: conflating the two is the defect this
// release already had to undo once in internal/providers.
func OpenBrowserForAuth(url string) error {
	if consent.CookiesDeclined() {
		slog.Info("not opening browser for auth: user opted out of browser access",
			"env", consent.CookiesEnv)
		return ErrBrowserAuthDeclined
	}

	// Extract domain from URL for cooldown tracking.
	domain := url
	if idx := strings.Index(url, "://"); idx >= 0 {
		domain = url[idx+3:]
	}
	if idx := strings.Index(domain, "/"); idx >= 0 {
		domain = domain[:idx]
	}

	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd, args = "open", []string{url}
	case "linux":
		cmd, args = "xdg-open", []string{url}
	case "windows":
		cmd, args = "cmd", []string{"/c", "start", url}
	default:
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}

	browserAuthOpened.mu.Lock()
	defer browserAuthOpened.mu.Unlock()

	if last, ok := browserAuthOpened.domains[domain]; ok && browserAuthNow().Sub(last) < browserAuthCooldown {
		slog.Debug("browser auth cooldown active", "domain", domain)
		return nil // suppressed
	}
	if err := browserAuthStart(cmd, args...); err != nil {
		return err
	}
	browserAuthOpened.domains[domain] = browserAuthNow()
	return nil
}

// HeaderIfPermitted is the string half of AttachBrowserCookies: providers that
// hand a whole Cookie header value to a request constructor rather than
// attaching cookies one at a time. Wrap it AT THE READ, not at the send — the
// browser read is the slow part, so a decline arriving during it has to win.
func HeaderIfPermitted(header string) string {
	if Disabled() {
		return ""
	}
	return header
}

// HeaderIfPermittedForURL is HeaderIfPermitted plus the origin check the
// consent layer cannot make on its own: cookies read for one site must only
// ever be sent to that site. Callers that derive a request URL from untrusted
// input (an MCP argument, a scraped listing link) MUST use this form, because
// consent alone would happily hand a live session cookie to whatever host the
// URL names.
//
// site is the registrable domain the cookies were read for, e.g. "booking.com".
// The header survives only when the request is https and its host is that
// domain or a subdomain of it. Matching is on the parsed hostname, so neither
// "https://evil.com/?x=www.booking.com" nor the userinfo trick
// "https://www.booking.com@evil.com/" gets through.
func HeaderIfPermittedForURL(header, rawURL, site string) string {
	if Disabled() || header == "" {
		return ""
	}
	if !IsHTTPSOnSite(rawURL, site) {
		slog.Debug("withholding browser cookies: request host is not the site they were read for",
			"site", site)
		return ""
	}
	return header
}

// IsHTTPSOnSite reports whether rawURL is an https URL on site or a subdomain
// of it. It is the check behind HeaderIfPermittedForURL, exported so a caller
// that takes a URL from outside can refuse the request outright rather than
// only withholding credentials from it.
func IsHTTPSOnSite(rawURL, site string) bool {
	site = strings.ToLower(strings.TrimSpace(site))
	if site == "" {
		return false
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" {
		return false
	}
	host := strings.ToLower(u.Hostname())

	// url.Hostname strips the brackets from an IPv6 literal but keeps the zone
	// identifier, so "https://[::1%25.booking.com]/" arrives here as the string
	// "::1%.booking.com" -- which ends in ".booking.com" and would otherwise pass,
	// while the dialer connects to IPv6 loopback. A hostname that survives to a
	// suffix comparison must be a DNS name: no zone identifier, no colons, and
	// not an IP address in any form.
	if strings.ContainsAny(host, "%:[]") || net.ParseIP(host) != nil {
		return false
	}

	// The clause above matches ASCII punctuation, so a fullwidth homoglyph host
	// such as "：：１％.booking.com" walks straight past it and suffix-matches.
	// Measured, not assumed: Go's IDNA profile REJECTS U+FF1A rather than folding
	// it to ":", so that host resolves to the punycode label
	// "xn--1-kn0i4ba.booking.com" and never reaches loopback -- the reviewer's
	// claimed exploit does not reproduce. It is refused anyway. This function
	// admits one host and its subdomains, all of which are ASCII, so a non-ASCII
	// hostname is by definition not one of them and does not need a theory of
	// harm to be turned away.
	for i := 0; i < len(host); i++ {
		if host[i] >= utf8.RuneSelf {
			return false
		}
	}

	return host == site || strings.HasSuffix(host, "."+site)
}
