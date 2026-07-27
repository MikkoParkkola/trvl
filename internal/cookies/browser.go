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
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

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
// Reading those stores is what makes rail search work against operators that
// challenge non-browser traffic, so it is on by default. It is also a read of a
// local credential store — on macOS it reaches the Keychain — that the user did
// not ask for, which is the kind of thing someone is entitled to decline. This
// is the way to decline it. Rail searches against a challenging operator then
// fail rather than degrade quietly, which is the honest cost of the choice.
const DisableEnv = "TRVL_NO_BROWSER_COOKIES"

// Disabled reports whether the user has declined browser cookie reads.
//
// Any non-empty value other than "0" or "false" counts, because someone setting
// this is expressing a preference about their credentials and the least
// surprising reading of TRVL_NO_BROWSER_COOKIES=yes is that they meant it.
func Disabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(DisableEnv))) {
	case "", "0", "false":
		return false
	default:
		return true
	}
}

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

		for _, browser := range []string{"brave", "chrome"} {
			if bctx.Err() != nil {
				break
			}
			if c := extractViaNab(bctx, browser, domain); c != "" {
				slog.Debug("browser cookies found", "browser", browser, "domain", domain)
				return c, nil
			}
		}

		// Nothing usable. Suppress retries briefly: a WAF challenge repeats
		// across every property in a result set, and without this each one
		// re-pays the full budget for the same answer.
		noteCookieFailure(domain)
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
}

var errCookieSuppressed = errors.New("cookie extraction suppressed after a recent failure")

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
// The decline is re-checked here even though the only caller already checked it.
// This function is the seam where the helper process is actually started, and
// three rounds of review on #521 each found the same failure shape: a gate placed
// on the caller rather than the seam, and then a caller that did not have it. The
// duplicate check costs one env read on a path that is about to fork a process.
func extractViaNab(ctx context.Context, browser, domain string) string {
	if Disabled() {
		return ""
	}

	nabPath, err := trvlnab.LookupPath()
	if err != nil {
		return ""
	}

	cmd, _, cancel := safeexec.Command(ctx, nabCookieTimeout,
		nabPath, "cookies", "export", domain, "--cookies", browser)
	defer cancel()

	out, err := safeexec.Output(cmd)
	if err != nil || len(out) == 0 {
		return ""
	}
	return parseNetscapeCookies(string(out))
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
	if c := BrowserCookiesContext(req.Context(), domain); c != "" {
		req.Header.Set("Cookie", c)
	}
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

// OpenBrowserForAuth opens url in the user's default browser so they can
// complete a CAPTCHA or login challenge. Suppresses repeated opens for the
// same domain within 24 hours. Returns an error if the browser could not
// be launched, or nil if suppressed by cooldown.
func OpenBrowserForAuth(url string) error {
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
