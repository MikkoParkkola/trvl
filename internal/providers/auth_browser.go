package providers

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/consent"
	"github.com/MikkoParkkola/trvl/internal/cookies"
)

// Browser-cookie fallback and the interactive escape hatch.
//
// Split out of auth.go, which crossed the 800-line hygiene ceiling (trvl#560).
// This is the half that grew, and it is the half with its own threat model: it
// decides when trvl may read a user's browser cookies, which destinations those
// cookies may be sent to, and when an interactive browser may be opened at all.
// Keeping it in its own file makes that surface reviewable on its own terms
// rather than as a section of a longer file about request preflight.
//
// Moved verbatim. Any behaviour change here belongs in its own commit.

// tryBrowserCookieRetry is Tier 3: read cookies from the user's disk-backed
// browser stores, seed them into the client jar, and retry preflight. On
// success it returns the freshly extracted auth values and true; the caller
// commits them under its own lock discipline. The auth parameter carries the
// resolved (city-specific) preflight URL.
func tryBrowserCookieRetry(ctx context.Context, pc *providerClient, auth *AuthConfig) (map[string]string, bool) {
	if !applyBrowserCookies(pc, auth.PreflightURL, pc.config.Cookies.Browser) {
		return nil, false
	}
	resp2, body2, err2 := doPreflightRequest(ctx, pc.client, auth)
	if err2 != nil || resp2.StatusCode < 200 || resp2.StatusCode >= 300 {
		return nil, false
	}
	// Reject 202 challenge pages — they are in the 2xx range but are WAF
	// interstitials, not real responses.
	if isAkamaiChallenge(resp2.StatusCode, body2) {
		return nil, false
	}
	return extractAuthValues(ctx, pc, auth, resp2, body2), true
}

// tryBrowserEscapeHatch is Tier 4: open the preflight URL in the user's
// browser, wait for the cookie set to visibly change (meaning the WAF/JS
// challenge was solved), then retry preflight with the fresh cookies. Only
// fires when the caller has opted in both per-provider
// (AuthConfig.BrowserEscapeHatch) and per-call (WithInteractive context).
//
// When an ElicitConfirmFunc is present in the context (MCP sessions), the
// user is prompted before the browser opens — this replaces the old silent
// 15-second timeout that users never noticed. The auth parameter carries the
// resolved (city-specific) preflight URL.
//
// On success it returns the freshly extracted auth values and true; the caller
// commits them under its own lock discipline (runPreflight already holds the
// write lock, the search-path caller does not).
func tryBrowserEscapeHatch(ctx context.Context, pc *providerClient, auth *AuthConfig) (map[string]string, bool) {
	// Everything below this line reads the user's own browser: the visible path
	// opens their real, logged-in profile and then reads its cookie store twice
	// (browserCookiesForURL, waitForFreshCookies). TRVL_NO_BROWSER_COOKIES is
	// exactly the decline for that, so it has to be answered here rather than
	// only at the reads.
	//
	// Answering it only at the reads is what shipped before, and it fails in the
	// worst direction: the reads return nil for a declining user, but the browser
	// has already been opened by then. They get the window they refused, the wait
	// for a cookie change that can never be observed, and no search result at the
	// end of it. The decline has to stop the window, not just the read.
	//
	// Not gated on Tier2Declined. That decline governs the empty-profile headless
	// browser (internal/consent/consent.go:35-39); this path is the user's actual
	// profile, and treating the two as one control is a conflation this release
	// already had to undo once in internal/ground.
	if consent.CookiesDeclined() {
		slog.Info("browser escape hatch declined: user opted out of browser cookie access",
			"provider", pc.config.ID, "env", consent.CookiesEnv)
		return nil, false
	}

	targetURL := auth.PreflightURL
	browserPref := pc.config.Cookies.Browser

	// Headless-first (MIK-6218): try to clear the challenge SILENTLY by driving
	// the user's installed browser headless (no window, no focus steal). Only if
	// an interactive captcha remains do we fall through to the visible-window
	// path below. A cleared challenge means the user never sees a popup.
	if res, err := headlessFirstResolve(ctx, targetURL); err == nil && res != nil {
		switch res.Status {
		case ChallengeCleared:
			if vals, ok := finishEscapeHatch(ctx, pc, auth, res.Cookies); ok {
				slog.Info("browser escape hatch: cleared headlessly, no window shown",
					"provider", pc.config.ID)
				return vals, true
			}
			// Headless cleared the page but the preflight retry still failed —
			// fall through to the visible window as a last resort.
		case ChallengeNeedsHuman:
			slog.Info("browser escape hatch: interactive captcha detected, opening visible window",
				"provider", pc.config.ID, "captcha", res.Marker)
			// fall through to the visible-window path
		}
	}

	// If elicitation is available, ask the user to confirm before opening
	// the browser. This turns a silent 15s timeout into an explicit user
	// action that actually succeeds.
	if elicit := getElicit(ctx); elicit != nil {
		msg := fmt.Sprintf(
			"%s needs a browser visit to refresh its WAF session. "+
				"I'll open %s in your browser — please complete any challenge "+
				"(CAPTCHA, cookie consent) and then confirm here.",
			pc.config.Name, targetURL,
		)
		confirmed, err := elicit(msg)
		if err != nil || !confirmed {
			slog.Info("browser escape hatch: user declined or elicitation failed",
				"provider", pc.config.ID)
			return nil, false
		}
	}

	slog.Info("opening URL in browser to refresh WAF cookies, waiting up to 30s...",
		"provider", pc.config.ID,
		"url", targetURL,
		"browser", browserPref,
	)

	// Invalidate warm cache so the escape hatch reads fresh cookies
	// from the browser after the user completes the challenge.
	InvalidateWarmCache(targetURL, browserPref)

	prev := browserCookiesForURL(targetURL)
	if err := openURLInBrowser(targetURL, browserPref); err != nil {
		slog.Warn("browser escape hatch: open failed",
			"provider", pc.config.ID, "error", err.Error())
		return nil, false
	}

	// With elicitation the user explicitly confirmed they completed the
	// challenge, so extend the cookie-change wait to 30s. Without
	// elicitation, keep the original 15s.
	deadline := 15 * time.Second
	if getElicit(ctx) != nil {
		deadline = 30 * time.Second
	}

	fresh, changed := waitForFreshCookies(ctx, targetURL, prev, time.Second, deadline)
	if !changed {
		slog.Warn("browser escape hatch: no cookie change observed within deadline",
			"provider", pc.config.ID)
		return nil, false
	}

	if pc.client == nil || pc.client.Jar == nil {
		return nil, false
	}
	vals, ok := finishEscapeHatch(ctx, pc, auth, fresh)
	if !ok {
		return nil, false
	}
	slog.Info("browser escape hatch: preflight recovered", "provider", pc.config.ID)
	return vals, true
}

// finishEscapeHatch seeds the recovered cookies into the client jar, retries the
// preflight, and (on a clean 2xx that is not another challenge page) extracts
// fresh auth values. It is the shared tail of the Tier-4 escape hatch, used by
// both the silent headless-first path and the visible-window fallback.
// Returns the extracted values and true when the preflight retry succeeds; the
// caller commits them under its own lock discipline.
func finishEscapeHatch(ctx context.Context, pc *providerClient, auth *AuthConfig, fresh []*http.Cookie) (map[string]string, bool) {
	if pc.client == nil || pc.client.Jar == nil {
		return nil, false
	}
	u, err := url.Parse(auth.PreflightURL)
	if err != nil {
		return nil, false
	}
	if len(fresh) > 0 {
		// Recovered from the user's own browser window, so this is a browser
		// seed and is recorded as one whether or not the retry below succeeds.
		// A vault is required, and a decline during the window the user spent
		// clearing the challenge means these cookies are simply not taken.
		if !vaultOf(pc.client).seedFromBrowser(u, fresh) {
			return nil, false
		}
	}

	resp2, body2, err2 := doPreflightRequest(ctx, pc.client, auth)
	if err2 != nil || resp2.StatusCode < 200 || resp2.StatusCode >= 300 {
		slog.Warn("browser escape hatch: preflight retry still failed",
			"provider", pc.config.ID)
		return nil, false
	}
	// Reject 202 challenge pages — still a WAF interstitial despite being 2xx.
	if isAkamaiChallenge(resp2.StatusCode, body2) {
		slog.Warn("browser escape hatch: preflight retry returned another challenge page",
			"provider", pc.config.ID)
		return nil, false
	}
	return extractAuthValues(ctx, pc, auth, resp2, body2), true
}

// needsBrowserCookieFallback reports whether the preflight outcome suggests a
// bot-detection block that browser cookies might bypass.
func needsBrowserCookieFallback(status, extracted int, extractions map[string]Extraction) bool {
	if status == http.StatusAccepted || status == http.StatusForbidden {
		return true
	}
	if len(extractions) > 0 && extracted == 0 {
		return true
	}
	return false
}

// providerCookieSite returns the registrable site of the endpoint the user
// approved. That endpoint's domain is the exact string the consent elicitation
// displays (mcp/tools_providers.go), so it is the only host in a provider
// config the user has actually seen and agreed to. Empty when the config is
// missing or its endpoint has no host, which fails the check closed.
func providerCookieSite(cfg *ProviderConfig) string {
	if cfg == nil {
		return ""
	}
	u, err := url.Parse(cfg.Endpoint)
	if err != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return ""
	}
	return registrableSuffix(host)
}

// cookieTargetPermitted reports whether targetURL may carry the user's browser
// cookies for this provider.
//
// The defect being closed is a confused deputy: PreflightURL rides in the same
// configure_provider call as Endpoint but is never shown in the consent
// elicitation, so a caller can name a host the user never approved and get the
// user's live session for it read and replayed. The property that fixes that is
// therefore SAME-SITE-AS-THE-CONSENTED-ENDPOINT, and nothing more.
//
// It is deliberately NOT cookies.IsHTTPSOnSite alone. That predicate carries a
// second, unrelated requirement — https plus an ordinary public DNS name — which
// is right for Booking.com, whose address trvl hardcodes, and wrong here, where
// the user types the endpoint themselves. Requiring it would refuse every
// self-hosted or on-LAN provider config while closing no part of the confused
// deputy, since those endpoints are same-site with their own preflight.
//
// So the rule splits on what the user approved:
//
//   - A public DNS endpoint keeps the full cookies.IsHTTPSOnSite check. Nothing
//     is relaxed for the hosts that matter.
//   - An IP-literal or localhost endpoint — only reachable because the user
//     entered it — requires an exact host match and forbids a scheme downgrade.
//     Same-site still holds; a foreign PreflightURL is still refused.
func cookieTargetPermitted(cfg *ProviderConfig, targetURL string) bool {
	site := providerCookieSite(cfg)
	if site == "" {
		return false
	}
	endpoint, err := url.Parse(cfg.Endpoint)
	if err != nil {
		return false
	}
	endpointHost := strings.ToLower(endpoint.Hostname())
	if !isLiteralOrLocalHost(endpointHost) {
		return cookies.IsHTTPSOnSite(targetURL, site)
	}

	target, err := url.Parse(targetURL)
	if err != nil {
		return false
	}
	// No downgrade: an https endpoint may not have its cookies replayed over
	// plaintext, even to itself.
	if endpoint.Scheme == "https" && target.Scheme != "https" {
		return false
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return false
	}
	// Exact match, not a suffix. "127.0.0.1" has no registrable site to be a
	// subdomain of, and a suffix rule over a bare literal is how host smuggling
	// gets back in.
	return strings.ToLower(target.Hostname()) == endpointHost
}

// isLiteralOrLocalHost reports whether host is an IP literal or a loopback name
// rather than an ordinary public DNS name. Such a host reaches a provider config
// only by the user typing it.
func isLiteralOrLocalHost(host string) bool {
	if host == "" {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	return host == "localhost" || strings.HasSuffix(host, ".localhost")
}

// applyBrowserCookies reads cookies from the user's browsers for the given
// URL and seeds them into the client's cookie jar. When browserHint is
// non-empty, reads only from that specific browser to avoid cross-browser
// cookie contamination. Returns true if any cookies were applied.
// It takes the providerClient rather than the bare client because marking the
// jar as browser-seeded IS the point of this function's second half: round 7 of
// review found the flag set at three call sites and missing at four others,
// which is the caller-instead-of-seam mistake every round of this branch has
// caught. There is one place browser cookies enter a provider jar, and this is
// it, so the provenance is recorded here where it cannot be forgotten.
func applyBrowserCookies(pc *providerClient, targetURL, browserHint string) bool {
	if pc == nil || pc.client == nil {
		return false
	}
	// The URL this runs for is caller-supplied and is NOT always the endpoint
	// the user consented to: cfg.Auth.PreflightURL rides in the same
	// configure_provider call, is never shown in the consent elicitation, and
	// reaches this function at four of the five call sites. Reading cookies for
	// it means reading whatever live session the user holds for a host the
	// caller named, then sending it there and returning a body snippet — a
	// confused deputy, not a cross-origin leak, which is why a target-derived
	// site would be a tautology and the site is pinned to the endpoint instead.
	if !cookieTargetPermitted(pc.config, targetURL) {
		slog.Debug("refusing browser cookies: target is not https on the consented provider site",
			"url", targetURL, "site", providerCookieSite(pc.config))
		return false
	}
	// Fail closed: browser cookies only enter a jar that can revoke them. A
	// plain jar would take them and keep them past a decline, which is the
	// whole failure this branch exists to remove.
	vault := vaultOf(pc.client)
	if vault == nil {
		return false
	}
	cookies := browserCookiesForURLWithHint(targetURL, browserHint)
	slog.Debug("applyBrowserCookies", "url", targetURL, "browser", browserHint, "count", len(cookies))
	if len(cookies) == 0 {
		return false
	}
	u, err := url.Parse(targetURL)
	if err != nil {
		return false
	}
	// The read above takes seconds; the user may have declined during it. The
	// vault re-checks that under the same lock it commits under, so a read that
	// began before the opt-out cannot land after it.
	if !vault.seedFromBrowser(u, cookies) {
		return false
	}
	slog.Debug("applied browser cookies to preflight client", "url", targetURL, "count", len(cookies))
	return true
}

// discardBrowserSeededAuth throws away cached auth state that came from the
// user's browser, once the user has declined browser access.
//
// The seventh path of this family, and the first that is purely in-memory: the
// six before it were a browser launch, an env-var reading, or a file on disk.
// Here the material has already been harvested into a live http.Client, and the
// auth cache serves it without consulting anything. In a CLI run that window is
// short. Under `trvl mcp`, which is the shipping default, the process outlives
// many searches, so the window is the process.
//
// The jar is REPLACED rather than emptied: net/http/cookiejar has no clear, and
// reaching into it to expire entries one by one would be a second
// implementation of a thing the standard library already gets right. A nil jar
// is not used either — a client without one silently drops Set-Cookie, which
// would turn a privacy control into a broken session.
//
// Only browser-seeded state is discarded. A session established by an ordinary
// preflight never touched the user's browser, and refusing it would punish a
// user for a setting that says nothing about it.
func discardBrowserSeededAuth(pc *providerClient) {
	if pc == nil || !cookies.Disabled() {
		return
	}

	pc.authMu.Lock()
	defer pc.authMu.Unlock()

	// The jar goes first, and it decides. It holds the provenance under the same
	// lock it commits cookies under, so this cannot observe "not seeded" while a
	// browser read that is already in flight is about to make it so: that read
	// re-checks the decline before committing and will now take nothing.
	if !vaultOf(pc.client).discardBrowserSeeded() {
		return
	}

	pc.authValues = nil
	pc.authExpiry = time.Time{}
	pc.lastPreflightURL = ""

	slog.Debug("discarded browser-seeded auth state after opt-out", "provider", pc.config.ID)
}
