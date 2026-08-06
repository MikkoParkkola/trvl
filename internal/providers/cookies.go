package providers

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/cookies"
	"github.com/browserutils/kooky"
	_ "github.com/browserutils/kooky/browser/all" // register all browser cookie finders
	"github.com/browserutils/kooky/browser/brave"
	"github.com/browserutils/kooky/browser/chrome"
)

// providerInteractiveKey is the context key that marks a call as interactive —
// i.e. originating from a human session where it is acceptable to launch the
// user's browser to clear a WAF/JS challenge. Non-interactive callers (unit
// tests, background jobs, MCP tool calls from headless pipelines) must leave
// this unset so the Tier 4 escape hatch never fires.
type providerInteractiveKey struct{}

// providerElicitKey is the context key for the elicitation callback.
type providerElicitKey struct{}

// ElicitConfirmFunc prompts the user with a message and returns true if they
// confirmed. This abstraction decouples the provider runtime from the MCP
// protocol layer — MCP handlers wrap their ElicitFunc into this signature.
type ElicitConfirmFunc func(message string) (confirmed bool, err error)

// WithInteractive returns a derived context marked as an interactive session.
// CLI entrypoints and MCP handlers that run with a human in the loop should
// call this so that the provider runtime may, if absolutely needed, open the
// user's browser to solve a JS bot-detection challenge.
func WithInteractive(ctx context.Context) context.Context {
	return context.WithValue(ctx, providerInteractiveKey{}, true)
}

// WithElicit returns a derived context carrying an elicitation callback.
// When the provider runtime needs user confirmation (e.g. "please visit
// booking.com to clear a WAF challenge"), it calls this function instead
// of silently opening a browser and timing out.
func WithElicit(ctx context.Context, fn ElicitConfirmFunc) context.Context {
	return context.WithValue(ctx, providerElicitKey{}, fn)
}

// getElicit returns the elicitation callback from ctx, or nil if none is set.
func getElicit(ctx context.Context) ElicitConfirmFunc {
	if ctx == nil {
		return nil
	}
	fn, _ := ctx.Value(providerElicitKey{}).(ElicitConfirmFunc)
	return fn
}

// isInteractive reports whether ctx was marked interactive by WithInteractive.
func isInteractive(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(providerInteractiveKey{}).(bool)
	return v
}

// openerFunc is the injectable hook used by openURLInBrowser so tests can
// exercise the OS-dispatch logic without actually launching a browser. The
// default implementation shells out via exec.Command; tests override it to
// record calls and return canned errors.
type openerFunc func(goos, browserPreference, targetURL string) error

// defaultOpenURL is the production openerFunc — it dispatches to the
// platform-native "open this URL" command.
//
// These are not waited on to completion. Waiting was a latent hang on a search
// path: `xdg-open` in particular can block for as long as the browser it launched
// stays open, and the whole point of this call is that the browser stays open long
// enough for a human to solve a challenge. Bounding it the way trvl bounds its
// credential helpers would be worse than the disease, because on Linux the browser
// is a child of `xdg-open` and killing the group would shut the window in the
// user's face.
//
// One of them is watched briefly, and only one. An earlier version consulted only
// Start's error, on the reasoning that the sole failure worth reporting was a missing
// command. That was wrong for the preferred-browser attempt: `open` exists whatever
// browser you name it, so an unavailable preference starts fine and then exits
// non-zero, and reporting that as success skipped the plain `open` fallback on the
// next line. The user got no window and never saw the challenge. That attempt now
// goes through startAndReapWithin. The other three do not, because they have no
// fallback behind them and `xdg-open` staying alive is normal rather than an error,
// so watching them would reintroduce the wait this comment opens by ruling out.
//
// Started, but still reaped either way. A child that is never waited on stays a
// zombie for the life of the parent, and trvl's MCP server is long-lived, so that
// would be one dead entry per challenge.
func defaultOpenURL(goos, browserPreference, targetURL string) error {
	switch goos {
	case "darwin":
		if browserPreference != "" {
			if err := startAndReapWithin(exec.Command("open", "-a", browserPreference, targetURL), launcherFailureWindow); err == nil {
				return nil
			}
		}
		if err := startAndReap(exec.Command("open", targetURL)); err != nil {
			return fmt.Errorf("open: %w", err)
		}
		return nil
	case "linux":
		if err := startAndReap(exec.Command("xdg-open", targetURL)); err != nil {
			return fmt.Errorf("xdg-open: %w", err)
		}
		return nil
	case "windows":
		if err := startAndReap(exec.Command("cmd", "/c", "start", "", targetURL)); err != nil {
			return fmt.Errorf("start: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("openURLInBrowser: unsupported OS %q", goos)
	}
}

// currentOpenURL is the active openerFunc. Tests may swap it out.
var currentOpenURL openerFunc = defaultOpenURL

// OpenURLInBrowser launches the user's browser pointed at targetURL so they
// can clear any WAF/JS challenge inline. On macOS it honours
// browserPreference (defaults to "Google Chrome"); on Linux and Windows the
// preference is ignored and the OS default is used.
//
// Callers MUST gate this on an explicit opt-in because it produces a visible
// side effect for the end user.
func OpenURLInBrowser(targetURL, browserPreference string) error {
	return openURLInBrowser(targetURL, browserPreference)
}

// openURLInBrowser is the internal implementation of OpenURLInBrowser.
func openURLInBrowser(targetURL, browserPreference string) error {
	if strings.TrimSpace(targetURL) == "" {
		return fmt.Errorf("openURLInBrowser: empty URL")
	}
	goos := runtime.GOOS
	if goos == "darwin" && strings.TrimSpace(browserPreference) == "" {
		browserPreference = "Google Chrome"
	}
	return currentOpenURL(goos, browserPreference, targetURL)
}

// cookieSourceFunc is the injectable hook used by waitForFreshCookies to
// re-read browser cookies on each tick. Production code points it at
// browserCookiesForURL; tests swap it out for an in-memory sequence.
type cookieSourceFunc func(targetURL string) []*http.Cookie

// currentCookieSource is the active cookieSourceFunc. Tests may swap it out.
var currentCookieSource cookieSourceFunc = browserCookiesForURL

// waitForFreshCookies polls the user's browser cookie stores for the given
// URL and returns as soon as the cookie set differs (by name+value) from
// prevSnapshot. Returns (newCookies, true) on a detected change, or
// (prevSnapshot, false) if maxWait elapses or ctx is cancelled.
//
// Intended as the wait step of the Tier 4 escape hatch: the caller has just
// launched the user's browser to solve a WAF challenge; this function blocks
// until the browser's cookie jar visibly updates (meaning the challenge is
// solved) or the deadline passes.
func waitForFreshCookies(ctx context.Context, targetURL string, prevSnapshot []*http.Cookie, pollInterval, maxWait time.Duration) ([]*http.Cookie, bool) {
	if pollInterval <= 0 {
		pollInterval = time.Second
	}
	if maxWait <= 0 {
		maxWait = 10 * time.Second
	}
	deadline := time.Now().Add(maxWait)
	prevKey := cookieSnapshotKey(prevSnapshot)

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return prevSnapshot, false
		case <-ticker.C:
			fresh := currentCookieSource(targetURL)
			if cookieSnapshotKey(fresh) != prevKey {
				return fresh, true
			}
			if time.Now().After(deadline) {
				return prevSnapshot, false
			}
		}
	}
}

// cookieSnapshotKey builds a deterministic, order-independent fingerprint of
// a cookie slice so waitForFreshCookies can detect set-level changes. Only
// name+value pairs are considered meaningful — domain and path are excluded
// because the same logical session cookie can be written under slightly
// different scopes across reads.
func cookieSnapshotKey(cookies []*http.Cookie) string {
	if len(cookies) == 0 {
		return ""
	}
	parts := make([]string, 0, len(cookies))
	for _, c := range cookies {
		if c == nil {
			continue
		}
		parts = append(parts, c.Name+"="+c.Value)
	}
	sort.Strings(parts)
	// Delimiter 0x1f (unit separator) is not valid in cookie name or value
	// per RFC 6265, so collisions between "ab|cd" + "" and "ab" + "cd" are
	// impossible.
	return strings.Join(parts, "\x1f")
}

// browserCookieLookupTimeout bounds how long we spend reading cookies from
// browser stores. On macOS, kooky's first Keychain access for the Safe
// Storage password + SQLite AES decryption takes 6-10s on cold start
// (subsequent calls are cached by the Keychain daemon in < 1s). A 5s
// budget caused systematic timeouts before valid Brave/Chrome cookies
// could be returned, triggering the full WAF recovery cascade on every
// first search. 12s gives cold Keychain enough headroom while staying
// well within perProviderTimeout (30s).
const browserCookieLookupTimeout = 12 * time.Second

// --- Browser cookie warm-up cache ---
//
// On cold start, kooky's first Keychain access takes 2-8 seconds on macOS.
// The tier cascade in auth.go calls browserCookiesForURL multiple times
// (initial apply, Tier 3a retry, Tier 4 snapshot), each blocking on the
// same slow Keychain lookup. This warm-up cache eliminates that latency by
// starting the kooky read as soon as the provider client is created
// (in getOrCreateClient), then serving the cached result to all subsequent
// callers.

// warmCacheEntry holds the result of a background cookie warm-up.
type warmCacheEntry struct {
	cookies []*http.Cookie
	done    chan struct{} // closed when the read completes
}

// warmCache stores in-flight and completed cookie warm-up results.
var warmCache = struct {
	mu      sync.Mutex
	entries map[string]*warmCacheEntry
}{entries: make(map[string]*warmCacheEntry)}

// warmCacheKey builds a lookup key from URL + browser hint.
func warmCacheKey(targetURL, browserHint string) string {
	return browserHint + "\x00" + targetURL
}

// WarmBrowserCookies starts a non-blocking background read of browser
// cookies for targetURL. The result is cached so that subsequent calls to
// browserCookiesForURLWithHint return immediately. Safe to call multiple
// times for the same URL; only the first call triggers a read.
func WarmBrowserCookies(targetURL, browserHint string) {
	key := warmCacheKey(targetURL, browserHint)

	warmCache.mu.Lock()
	if _, exists := warmCache.entries[key]; exists {
		warmCache.mu.Unlock()
		return // already warming or warmed
	}
	entry := &warmCacheEntry{done: make(chan struct{})}
	warmCache.entries[key] = entry
	warmCache.mu.Unlock()

	go func() {
		defer close(entry.done)
		entry.cookies = readBrowserCookiesDirect(targetURL, browserHint)
	}()
}

// warmBrowserCookiesResult blocks until the warm-up for targetURL completes
// (up to the given timeout) and returns the cached cookies. Returns nil if
// no warm-up was started or the timeout expires.
func warmBrowserCookiesResult(targetURL, browserHint string, timeout time.Duration) (out []*http.Cookie) {
	defer func() { out = permittedAfterRead(out) }()

	key := warmCacheKey(targetURL, browserHint)

	warmCache.mu.Lock()
	entry, exists := warmCache.entries[key]
	warmCache.mu.Unlock()

	if !exists {
		return nil
	}

	select {
	case <-entry.done:
		return entry.cookies
	case <-time.After(timeout):
		return nil
	}
}

// readBrowserCookiesDirect performs the actual kooky cookie read. This is
// the same logic as browserCookiesForURLWithHint but extracted so it can
// run in a background goroutine without recursive cache lookups.
func readBrowserCookiesDirect(targetURL, browserHint string) (out []*http.Cookie) {
	defer func() { out = permittedAfterRead(out) }()

	// Reached from the background pre-warm goroutine and the browser-hint path,
	// neither of which passes through browserCookiesForURL, so the opt-out is
	// checked here too rather than trusting a caller to have done it.
	if cookies.Disabled() {
		return nil
	}
	if os.Getenv("TRVL_ALLOW_BROWSER_COOKIES") == "" && isTestBinary() {
		return nil
	}

	u, err := url.Parse(targetURL)
	if err != nil || u.Host == "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), browserCookieLookupTimeout)
	defer cancel()

	host := u.Hostname()
	domainSuffix := registrableSuffix(host)

	if browserHint != "" {
		var kookyCookies []*kooky.Cookie
		switch strings.ToLower(browserHint) {
		case "brave":
			if path := findBraveCookiePath(); path != "" {
				kookyCookies, _ = brave.ReadCookies(ctx, path, kooky.Valid, kooky.DomainHasSuffix(domainSuffix))
			}
		case "chrome":
			if path := findChromeCookiePath(); path != "" {
				kookyCookies, _ = chrome.ReadCookies(ctx, path, kooky.Valid, kooky.DomainHasSuffix(domainSuffix))
			}
		}
		if len(kookyCookies) > 0 {
			result := make([]*http.Cookie, 0, len(kookyCookies))
			seen := make(map[string]struct{}, len(kookyCookies))
			for _, c := range kookyCookies {
				if c == nil || !cookieDomainMatchesHost(c.Domain, host) {
					continue
				}
				key := c.Name + "\x00" + c.Domain + "\x00" + c.Path
				if _, dup := seen[key]; dup {
					continue
				}
				seen[key] = struct{}{}
				cp := c.Cookie
				result = append(result, &cp)
			}
			return result
		}
	}

	// Fall through to all-browser auto-discovery.
	cookies, err := kooky.ReadCookies(ctx, kooky.Valid, kooky.DomainHasSuffix(domainSuffix))
	if err != nil && len(cookies) == 0 {
		return nil
	}

	result := make([]*http.Cookie, 0, len(cookies))
	type dedupEntry struct {
		cookie http.Cookie
		idx    int
	}
	seen := make(map[string]*dedupEntry, len(cookies))
	for _, c := range cookies {
		if c == nil || !cookieDomainMatchesHost(c.Domain, host) {
			continue
		}
		key := c.Name + "\x00" + c.Domain + "\x00" + c.Path
		if prev, dup := seen[key]; dup {
			if len(c.Value) > len(prev.cookie.Value) {
				prev.cookie = c.Cookie
				if prev.idx >= 0 {
					result[prev.idx] = &prev.cookie
				}
			}
			continue
		}
		cp := c.Cookie
		idx := len(result)
		result = append(result, &cp)
		seen[key] = &dedupEntry{cookie: cp, idx: idx}
	}
	return result
}

// InvalidateWarmCache removes the warm cache entry for the given URL and
// browser hint. Used after the browser escape hatch to force a fresh read.
func InvalidateWarmCache(targetURL, browserHint string) {
	key := warmCacheKey(targetURL, browserHint)
	warmCache.mu.Lock()
	delete(warmCache.entries, key)
	warmCache.mu.Unlock()
}

// permittedAfterRead is the post-read consent gate for every function in this
// package that returns cookies taken from the user's browser.
//
// Reading a browser takes seconds — Keychain unlock, a cold profile, a window
// the user has to clear by hand. The checks that guard the ENTRY to those reads
// therefore decide the question too early: a decline that arrives while the read
// is in flight loses, the read finishes, and the credentials go out anyway. That
// race is what rounds 8 and 11 of review found, in the vault and then in the
// Booking.com room fetch, and chasing it call site by call site is how a family
// of eleven bypasses gets built.
//
// So the readers below re-check on the way OUT, via a deferred call, on every
// return path including the warm cache. One place, every caller, no seam for a
// new call site to miss. It is a package-level helper rather than an inline
// cookies.Disabled() because several of those readers declare a local variable
// named cookies, and a check whose correctness depends on where the shadow
// begins is a check the next editor breaks.
func permittedAfterRead(list []*http.Cookie) []*http.Cookie {
	if cookies.Disabled() {
		// Say so. Dropping to nil silently makes a refusal look exactly like a
		// machine with no cookies for this site, which is the one thing a user
		// debugging "why is it not logged in" must be able to tell apart.
		if len(list) > 0 {
			slog.Debug("browser cookies dropped: the user has declined browser access",
				"env", cookies.DisableEnv, "dropped", len(list))
		}
		return nil
	}
	return list
}

// BrowserCookiesForURL reads matching browser cookies for targetURL using the
// same bounded, test-guarded path as provider search recovery.
func BrowserCookiesForURL(targetURL string) []*http.Cookie {
	return browserCookiesForURL(targetURL)
}

// browserCookiesForURLWithHint reads cookies from a specific browser's cookie
// store for the given URL's domain. When browserHint is non-empty (e.g. "brave",
// "chrome"), it bypasses kooky's auto-discovery and reads directly from that
// browser's default profile cookie file. This avoids cross-browser cookie
// contamination where stale Chrome sessions overwrite fresh Brave sessions (or
// vice versa) during auto-discovery deduplication.
//
// Falls back to browserCookiesForURL (all-browser auto-discovery) when the
// hint is empty or the specified browser's cookie store cannot be found.
func browserCookiesForURLWithHint(targetURL, browserHint string) (out []*http.Cookie) {
	defer func() { out = permittedAfterRead(out) }()

	if cookies.Disabled() {
		return nil
	}
	if browserHint == "" {
		return browserCookiesForURL(targetURL)
	}

	// Check warm cache first — returns instantly if pre-warmed.
	if cached := warmBrowserCookiesResult(targetURL, browserHint, browserCookieLookupTimeout); cached != nil {
		return cached
	}

	// Same test-binary guard as browserCookiesForURL.
	if os.Getenv("TRVL_ALLOW_BROWSER_COOKIES") == "" && isTestBinary() {
		return nil
	}

	u, err := url.Parse(targetURL)
	if err != nil || u.Host == "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), browserCookieLookupTimeout)
	defer cancel()

	host := u.Hostname()
	domainSuffix := registrableSuffix(host)

	var kookyCookies []*kooky.Cookie

	switch strings.ToLower(browserHint) {
	case "brave":
		if path := findBraveCookiePath(); path != "" {
			kookyCookies, _ = brave.ReadCookies(ctx, path, kooky.Valid, kooky.DomainHasSuffix(domainSuffix))
		}
	case "chrome":
		if path := findChromeCookiePath(); path != "" {
			kookyCookies, _ = chrome.ReadCookies(ctx, path, kooky.Valid, kooky.DomainHasSuffix(domainSuffix))
		}
	}

	if len(kookyCookies) == 0 {
		// Fallback to all-browser auto-discovery.
		return browserCookiesForURL(targetURL)
	}

	// Filter and deduplicate.
	result := make([]*http.Cookie, 0, len(kookyCookies))
	seen := make(map[string]struct{}, len(kookyCookies))
	for _, c := range kookyCookies {
		if c == nil {
			continue
		}
		if !cookieDomainMatchesHost(c.Domain, host) {
			continue
		}
		key := c.Name + "\x00" + c.Domain + "\x00" + c.Path
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		cp := c.Cookie
		result = append(result, &cp)
	}
	return result
}

// findBraveCookiePath returns the path to Brave's default profile cookie DB.
func findBraveCookiePath() string {
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	candidates := []string{
		filepath.Join(cfgDir, "BraveSoftware", "Brave-Browser", "Default", "Cookies"),
		filepath.Join(cfgDir, "BraveSoftware", "Brave-Browser", "Default", "Network", "Cookies"),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// findChromeCookiePath returns the path to Chrome's default profile cookie DB.
func findChromeCookiePath() string {
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	candidates := []string{
		filepath.Join(cfgDir, "Google", "Chrome", "Default", "Cookies"),
		filepath.Join(cfgDir, "Google", "Chrome", "Default", "Network", "Cookies"),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// registrableSuffix returns a suffix of host suitable for a DomainHasSuffix
// filter. For e.g. "www.booking.com" it returns "booking.com"; for short
// hosts it returns the original host. This is a heuristic — we filter
// precisely afterwards in cookieDomainMatchesHost.
func registrableSuffix(host string) string {
	host = strings.TrimPrefix(host, ".")
	parts := strings.Split(host, ".")
	if len(parts) <= 2 {
		return host
	}
	return strings.Join(parts[len(parts)-2:], ".")
}

// cookieDomainMatchesHost reports whether a cookie's Domain attribute applies
// to the given request host per RFC 6265: the cookie domain must equal the
// host or be a dot-prefixed parent domain.
func cookieDomainMatchesHost(cookieDomain, host string) bool {
	if cookieDomain == "" || host == "" {
		return false
	}
	cd := strings.ToLower(strings.TrimPrefix(cookieDomain, "."))
	h := strings.ToLower(host)
	if cd == h {
		return true
	}
	return strings.HasSuffix(h, "."+cd)
}

// isTestBinary detects if the current process is a Go test binary.
// Go test binaries have "-test." flags in os.Args (e.g. -test.run, -test.v).
func isTestBinary() bool {
	for _, arg := range os.Args {
		if strings.HasPrefix(arg, "-test.") {
			return true
		}
	}
	return false
}

// startAndReap starts cmd, looks briefly for an immediate failure, and reaps it in
// the background so a finished child does not linger as a zombie.
func startAndReap(cmd *exec.Cmd) error {
	return startAndReapWithin(cmd, launcherStartupWindow)
}

// launcherFailureWindow is how long to wait for the preferred-browser attempt to
// fail before assuming it succeeded.
//
// 500ms, because `open -a` resolves the application before doing anything and so
// fails within tens of milliseconds. It costs nothing on the success path either,
// since `open` hands the URL off and exits rather than staying with the browser, so
// this window is only ever spent on a failure.
//
// A var rather than a const so a test can substitute a value. Both windows are
// wall-clock budgets, and a test that leaves them at their production sizes is
// asserting against how promptly this machine happened to fork a shell; two of
// the tests below did exactly that and went red under load (#533). Nothing
// outside this file reads either name, and the tests that substitute them all
// call t.Setenv, which forbids t.Parallel, so no reader runs concurrently.
var launcherFailureWindow = 500 * time.Millisecond

// launcherStartupWindow is the same idea for the launchers with no fallback behind
// them, and it is far shorter because here the window IS spent on success: `xdg-open`
// can stay alive as long as the browser it started, so a persistent launcher pays
// this on every challenge.
//
// It is not zero, which was the previous answer and was wrong. The caller does use
// the error. mcp/tools_providers.go tells the user "Opened X in browser to warm
// cookies. Future searches will use these cookies automatically" on a nil error, so
// swallowing a launch failure produces a confident false statement that a browser
// opened when none did, followed by a wait for cookies that cannot arrive. A headless
// Linux box where `xdg-open` exists with no usable handler is the realistic case, and
// it fails in milliseconds.
//
// 150ms buys that at a cost nobody perceives, against a cookie wait measured in tens
// of seconds.
//
// A var for the reason given on launcherFailureWindow: 150ms is a production
// tradeoff, not a size any test should have to beat.
var launcherStartupWindow = 150 * time.Millisecond

// startAndReapWithin starts cmd and then looks briefly for an early failure.
//
// Sizing this window took three attempts, and the wrong answers are recorded rather
// than tidied away because each was wrong for a different reason.
//
// The original consulted only Start's error, which cannot see a launcher that starts
// and then exits. `open` exists whatever browser you name it, so an unavailable
// preference started fine, exited non-zero, was reported as success, and the plain
// `open` fallback on the next line was skipped. The user got no window.
//
// The second routed every launcher through a two-second window. Wrong because
// `xdg-open` can stay alive as long as the browser it started, so a persistent
// launcher paid the entire window on every challenge, which is the blocking this
// whole approach exists to avoid.
//
// The third confined the window to the preferred-browser attempt, on the grounds that
// the other launchers had no fallback and so lost nothing by being unwatched. Also
// wrong: the caller reports "Opened X in browser to warm cookies" on a nil error, so
// a swallowed failure is a confident false statement to the user, followed by a wait
// for cookies that cannot arrive.
//
// So every launcher is watched, with the window sized to what it actually costs.
// Callers pass launcherFailureWindow where the window is never spent on success, and
// launcherStartupWindow where it is.
//
// Three outcomes, each treated as what it is. Exited cleanly inside the window:
// success. Exited non-zero inside the window: failure, so the caller can fall back or
// report it. Still running when the window expires: success, with the wait continuing
// in the background so the child is reaped rather than left a zombie in a long-lived
// MCP server. A slow launch is therefore never misread as a failure; only an explicit
// non-zero exit is.
func startAndReapWithin(cmd *exec.Cmd, window time.Duration) error {
	if err := cmd.Start(); err != nil {
		return err
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		return err
	case <-time.After(window):
		// Still alive, so it launched. Keep waiting off to the side purely to reap.
		go func() { <-done }()
		return nil
	}
}
