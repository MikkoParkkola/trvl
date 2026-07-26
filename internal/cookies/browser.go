// Package cookies extracts browser cookies for use in HTTP requests.
// It tries the nab CLI/MCP tool first (which handles decryption and keychain
// access), then falls back to no-op. This avoids CGO and keychain dependencies
// in the main binary.
package cookies

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	trvlnab "github.com/MikkoParkkola/trvl/internal/nab"
	"github.com/MikkoParkkola/trvl/internal/safeexec"
	"os/exec"
)

// nabCookieTimeout bounds a cookie export. Generous compared with a credential
// read, because nab decrypts a whole cookie jar, but finite: this runs inside a
// search the user is waiting on.
const nabCookieTimeout = 5 * time.Second

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
func BrowserCookies(domain string) string {
	for _, browser := range []string{"brave", "chrome"} {
		if c := extractViaNab(browser, domain); c != "" {
			slog.Debug("browser cookies found", "browser", browser, "domain", domain)
			return c
		}
	}
	return ""
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
func extractViaNab(browser, domain string) string {
	nabPath, err := trvlnab.LookupPath()
	if err != nil {
		return ""
	}

	cmd, _, cancel := safeexec.Command(context.Background(), nabCookieTimeout,
		nabPath, "cookies", "export", domain, "--cookies", browser)
	defer cancel()

	out, err := cmd.Output()
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
	if c := BrowserCookies(domain); c != "" {
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
