package providers

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/consent"
	"github.com/MikkoParkkola/trvl/internal/cookies"
	"github.com/MikkoParkkola/trvl/internal/waf"
	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

// runPreflight performs a GET to the preflight URL and extracts auth values.
// The vars map allows search-specific placeholders (e.g. ${city_id}) to be
// resolved in the preflight URL, so WAF cookies are obtained for the actual
// target city rather than a hardcoded default. When the resolved URL differs
// from the last preflight (city changed), the auth cache is invalidated.
//
// Returns an immutable snapshot of the auth values that were valid for THIS
// preflight call. Callers MUST use the returned snapshot rather than re-reading
// pc.authValues later — between the call site and the read, a concurrent
// search to a different city can swap the values out from under us. See
// MIK-3070 for the race that motivated this signature.
func (rt *Runtime) runPreflight(ctx context.Context, pc *providerClient, vars map[string]string) (map[string]string, error) {
	// Before every cache read below, including the no-preflight early return.
	// The auth cache lives in memory and the MCP server is long-lived, so a jar
	// seeded from the user's browser outlives the moment it was permitted: the
	// cache hit returns without reaching loadCachedCookies, tryBrowserCookieRetry
	// or any other guarded reader, and the browser-derived cookies keep going out
	// on the wire. Discarding the seeded state costs a preflight; keeping it
	// costs the user the control they set.
	discardBrowserSeededAuth(pc)

	if pc.config.Auth == nil || pc.config.Auth.PreflightURL == "" {
		// No preflight needed — but the caller may still rely on existing
		// pc.authValues populated by other paths (header-based auth, env tokens).
		// Return a snapshot so the caller's later read is race-free.
		return snapshotAuthValues(pc), nil
	}

	// Resolve search-specific vars in the preflight URL so that ${city_id}
	// etc. produce a city-specific WAF session.
	resolvedURL := substituteVars(pc.config.Auth.PreflightURL, vars)

	pc.authMu.RLock()
	cacheValid := time.Now().Before(pc.authExpiry) && pc.lastPreflightURL == resolvedURL
	if cacheValid {
		// Snapshot under RLock so a concurrent invalidation cannot interleave.
		snap := copyAuthValues(pc.authValues)
		pc.authMu.RUnlock()
		return snap, nil
	}
	pc.authMu.RUnlock()

	pc.authMu.Lock()
	defer pc.authMu.Unlock()

	// Double-check after lock.
	if time.Now().Before(pc.authExpiry) && pc.lastPreflightURL == resolvedURL {
		return copyAuthValues(pc.authValues), nil
	}

	// Build a shallow copy of the auth config with the resolved URL so that
	// doPreflightRequest, cookie helpers, and WAF solver all see the
	// city-specific URL without mutating the shared config.
	resolvedAuth := *pc.config.Auth
	resolvedAuth.PreflightURL = resolvedURL

	// Tier 0: try loading persisted cookies from a previous successful session.
	// This makes browser escape hatch a one-time setup rather than per-search.
	// The file records which save site wrote each entry, so a user who has
	// declined browser cookie reads gets the site-issued entries and not the
	// harvested ones. Entries written before that field existed read as
	// browser-derived. loadCachedCookies records what it seeded on the vault,
	// in the same critical section it commits the cookies in.
	loadCachedCookies(pc.client, resolvedURL)

	resp, body, err := doPreflightRequest(ctx, pc.client, &resolvedAuth)
	if err != nil {
		return nil, err
	}

	extracted := applyExtractions(resolvedAuth.Extractions, resp, body, pc.authValues)
	// Stage 2: fetch any URL-based extractions (e.g. JS bundle for
	// persisted-query sha256Hash) using the now-populated cookie jar.
	extracted += applyURLExtractions(ctx, pc.client, resolvedAuth.Extractions, pc.authValues)

	// Fallback tier cascade:
	//   Tier 1: preflight request already ran above (extracted ok? done)
	//   Tier 3: read cookies straight from the user's browser via kooky.
	//   Tier 4: if Tier 3 didn't produce a working session AND the caller
	//           opted in (AuthConfig.BrowserEscapeHatch + WithInteractive ctx),
	//           open the preflight URL in the user's browser so they clear
	//           any JS/CAPTCHA challenge, then re-read cookies.
	// (Tier 2 — TLS-fingerprinted retry — is covered by the chrome HTTP
	// client selected in getOrCreateClient; it runs implicitly on every
	// request when cfg.TLS.Fingerprint == "chrome".)
	if needsBrowserCookieFallback(resp.StatusCode, extracted, resolvedAuth.Extractions) {
		// This whole cascade runs with pc.authMu held for writing (acquired
		// above), which is what keeps two concurrent searches from both opening
		// a browser window. The tiers therefore must not take the lock
		// themselves — they return the recovered values and we commit them here
		// with the Locked variant.
		//
		// Tier 3a: read cookies from user's browser (kooky).
		if vals, ok := tryBrowserCookieRetry(ctx, pc, &resolvedAuth); ok {
			commitAuthValuesLocked(pc, vals)
			saveCachedCookies(pc.client, resolvedURL)
			pc.lastPreflightURL = resolvedURL
			pc.authExpiry = time.Now().Add(pc.effectiveCacheTTL())
			return copyAuthValues(pc.authValues), nil
		}
		// Tier 3b: run WAF challenge.js in sobek JS engine (pure Go).
		if vals, ok := tryWAFSolve(ctx, pc, &resolvedAuth, resp.StatusCode, body); ok {
			commitAuthValuesLocked(pc, vals)
			saveCachedCookies(pc.client, resolvedURL)
			pc.lastPreflightURL = resolvedURL
			pc.authExpiry = time.Now().Add(pc.effectiveCacheTTL())
			return copyAuthValues(pc.authValues), nil
		}
		// Tier 4: last-resort escape hatch — open in browser.
		if resolvedAuth.BrowserEscapeHatch && isInteractive(ctx) {
			if vals, ok := tryBrowserEscapeHatch(ctx, pc, &resolvedAuth); ok {
				commitAuthValuesLocked(pc, vals)
				saveCachedCookies(pc.client, resolvedURL)
				pc.lastPreflightURL = resolvedURL
				pc.authExpiry = time.Now().Add(pc.effectiveCacheTTL())
				return copyAuthValues(pc.authValues), nil
			}
		}
	}

	// Tier 1 succeeded directly — persist cookies for future sessions.
	saveCachedCookies(pc.client, resolvedURL)
	pc.lastPreflightURL = resolvedURL
	pc.authExpiry = time.Now().Add(pc.effectiveCacheTTL())
	return copyAuthValues(pc.authValues), nil
}

// The auth-value extraction and commit helpers used below live in
// auth_values.go: extractAuthValues (lock-free) and the commit pair that
// encodes the two lock disciplines this file's callers run under.

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

// tryWAFSolve is Tier 3b: if the preflight response looks like an AWS WAF
// challenge page (HTTP 202 with *.awswaf.com script refs), run challenge.js
// in the sobek JS engine to obtain an aws-waf-token cookie, then retry
// preflight. On success it returns the freshly extracted auth values and true;
// the caller commits them under its own lock discipline. The auth parameter
// carries the resolved (city-specific) preflight URL.
func tryWAFSolve(ctx context.Context, pc *providerClient, auth *AuthConfig, statusCode int, pageBody []byte) (map[string]string, bool) {
	// Only attempt on HTTP 202 (AWS WAF challenge) or 403 (some WAF variants).
	if statusCode != http.StatusAccepted && statusCode != http.StatusForbidden {
		return nil, false
	}

	pageURL := auth.PreflightURL
	cookie, err := waf.SolveAWSWAF(ctx, pc.client, pageURL, string(pageBody), nil)
	if err != nil {
		slog.Debug("waf solver did not produce a token", "provider", pc.config.ID, "error", err.Error())
		return nil, false
	}

	// Install the token cookie into the client jar.
	u, err := url.Parse(pageURL)
	if err != nil {
		return nil, false
	}
	pc.client.Jar.SetCookies(u, []*http.Cookie{cookie})
	slog.Info("waf solver obtained aws-waf-token via JS engine", "provider", pc.config.ID)

	// Retry preflight with the fresh token.
	resp2, body2, err2 := doPreflightRequest(ctx, pc.client, auth)
	if err2 != nil || resp2.StatusCode < 200 || resp2.StatusCode >= 300 {
		return nil, false
	}
	// Reject 202 challenge pages — still a WAF interstitial despite being 2xx.
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

// doSearchRequest clones the given request, executes it via client, reads the
// response body, and returns (resp, body, err). Used to retry the main search
// request after recovering cookies from the escape hatch. The original request
// body (if any) is not consumed by this helper — req.GetBody is used to obtain
// a fresh reader. The returned *http.Response must NOT be used for streaming;
// the body is already consumed and closed.
func doSearchRequest(ctx context.Context, client *http.Client, orig *http.Request) (*http.Response, []byte, error) {
	var bodyReader io.Reader
	if orig.GetBody != nil {
		b, err := orig.GetBody()
		if err != nil {
			return nil, nil, fmt.Errorf("search retry: get body: %w", err)
		}
		bodyReader = b
	}
	req, err := http.NewRequestWithContext(ctx, orig.Method, orig.URL.String(), bodyReader)
	if err != nil {
		return nil, nil, fmt.Errorf("search retry: create request: %w", err)
	}
	req.Header = orig.Header.Clone()

	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("search retry: http: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := decompressBody(resp, maxResponseBytes)
	if err != nil {
		return resp, nil, fmt.Errorf("search retry: read body: %w", err)
	}
	return resp, body, nil
}

// doPreflightRequest issues the preflight request described by auth using
// the given client and returns the response plus body bytes. The caller does
// not need to close the body — it is consumed before returning.
func doPreflightRequest(ctx context.Context, client *http.Client, auth *AuthConfig) (*http.Response, []byte, error) {
	preflightBody := substituteEnvVars(auth.PreflightBody)

	method := auth.PreflightMethod
	if method == "" {
		method = "GET"
	}

	var bodyReader io.Reader
	if preflightBody != "" {
		bodyReader = strings.NewReader(preflightBody)
	}

	req, err := http.NewRequestWithContext(ctx, method, auth.PreflightURL, bodyReader)
	if err != nil {
		return nil, nil, fmt.Errorf("preflight request: %w", err)
	}
	for k, v := range auth.PreflightHeaders {
		req.Header.Set(k, substituteEnvVars(v))
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("preflight http: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := decompressBody(resp, maxResponseBytes)
	if err != nil {
		return resp, nil, fmt.Errorf("preflight read: %w", err)
	}
	return resp, body, nil
}

// applyExtractions runs each configured regex extraction against the response
// body or a named header, writing matches into authValues. Returns the number
// of extractions that matched. Extractions with a non-empty URL are skipped
// here — they require a second HTTP request and are handled by
// applyURLExtractions, which the caller should invoke after this one.
func applyExtractions(extractions map[string]Extraction, resp *http.Response, body []byte, authValues map[string]string) int {
	matched := 0
	for name, extraction := range extractions {
		if extraction.URL != "" {
			continue // deferred to applyURLExtractions
		}
		source := string(body)
		if extraction.Header != "" {
			source = resp.Header.Get(extraction.Header)
		}
		re, err := regexp.Compile(extraction.Pattern)
		if err != nil {
			slog.Warn("preflight regex compile failed", "name", name, "pattern", extraction.Pattern, "error", err.Error())
			continue
		}
		m := re.FindStringSubmatch(source)
		if len(m) >= 2 {
			varName := extraction.Variable
			if varName == "" {
				varName = name
			}
			authValues[varName] = m[1]
			matched++
		} else if extraction.Default != "" {
			varName := extraction.Variable
			if varName == "" {
				varName = name
			}
			authValues[varName] = extraction.Default
			matched++
			slog.Debug("extraction no match; using default",
				"name", name, "pattern", extraction.Pattern)
		}
	}
	return matched
}

// applyURLExtractions handles the second-stage extractions: those whose URL
// field is set. Each URL is fetched with the provided HTTP client (reusing
// its cookie jar — critical, since bundled JS is usually served under the
// provider's own origin with the same WAF cookies as the HTML page) and the
// pattern is matched against the response body. ${var} placeholders in the
// URL are resolved from authValues so a stage-2 URL can be derived from a
// stage-1 extraction (e.g. "bundle_url" extracted from HTML → fetched as
// stage 2). Returns the number of new variables matched.
func applyURLExtractions(ctx context.Context, client *http.Client, extractions map[string]Extraction, authValues map[string]string) int {
	if client == nil {
		return 0
	}
	// Build substitution map once from already-extracted values.
	vars := make(map[string]string, len(authValues))
	for k, v := range authValues {
		vars["${"+k+"}"] = v
	}

	matched := 0
	for name, extraction := range extractions {
		if extraction.URL == "" {
			continue
		}
		resolvedURL := substituteVars(extraction.URL, vars)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, resolvedURL, nil)
		if err != nil {
			slog.Warn("stage-2 extraction: build request failed",
				"name", name, "url", resolvedURL, "error", err.Error())
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			slog.Warn("stage-2 extraction: fetch failed",
				"name", name, "url", resolvedURL, "error", err.Error())
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
		_ = resp.Body.Close()
		if err != nil {
			slog.Warn("stage-2 extraction: read failed",
				"name", name, "url", resolvedURL, "error", err.Error())
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			slog.Warn("stage-2 extraction: non-2xx",
				"name", name, "url", resolvedURL, "status", resp.StatusCode)
			continue
		}

		re, err := regexp.Compile(extraction.Pattern)
		if err != nil {
			slog.Warn("stage-2 extraction: regex compile failed",
				"name", name, "pattern", extraction.Pattern, "error", err.Error())
			continue
		}
		m := re.FindStringSubmatch(string(body))
		varName := extraction.Variable
		if varName == "" {
			varName = name
		}
		if len(m) >= 2 {
			authValues[varName] = m[1]
			// Make the newly-extracted value available to subsequent URL
			// substitutions in this same pass (enables N-stage chains).
			vars["${"+varName+"}"] = m[1]
			matched++
		} else if extraction.Default != "" {
			authValues[varName] = extraction.Default
			vars["${"+varName+"}"] = extraction.Default
			matched++
			slog.Warn("stage-2 extraction: no match; using default",
				"name", name, "url", resolvedURL, "pattern", extraction.Pattern)
		} else {
			slog.Warn("stage-2 extraction: no match",
				"name", name, "url", resolvedURL, "pattern", extraction.Pattern)
		}
	}
	return matched
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

// isAkamaiChallenge reports whether an HTTP response looks like an Akamai (or
// AWS WAF) JavaScript challenge page. These are characterised by HTTP 202
// status paired with body markers such as "window.aws", "reportChallengeError",
// or "challenge.js" script references. An HTTP 202 WITHOUT these markers is
// treated as a legitimate response (some APIs use 202 Accepted).
func isAkamaiChallenge(statusCode int, body []byte) bool {
	if statusCode != http.StatusAccepted {
		return false
	}
	// Short-circuit: if the body parses as valid JSON with no challenge markers,
	// it is a real 202 Accepted response (e.g. async job acknowledgement).
	// Challenge pages are always HTML, never JSON.
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
		return false
	}
	// Look for challenge page signatures in HTML.
	return bytes.Contains(body, []byte("challenge.js")) ||
		bytes.Contains(body, []byte("window.aws")) ||
		bytes.Contains(body, []byte("reportChallengeError")) ||
		bytes.Contains(body, []byte("awswaf"))
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

// decompressBody reads and decompresses the response body based on the
// Content-Encoding header. When the request explicitly sets Accept-Encoding
// (e.g. "gzip, deflate, br, zstd" to match Chrome), Go's http.Transport
// does NOT auto-decompress — it assumes the caller handles decompression.
// This function handles gzip, br (Brotli), and zstd transparently.
//
// When the transport (or an intermediate CDN/proxy) already decompressed the
// body but left the Content-Encoding header intact, the declared encoding
// won't match the actual payload. The gzip path buffers the body and falls
// back to raw bytes on header mismatch — this is the most common case in
// practice (e.g. Airbnb preflight via fhttp Chrome-fingerprinted transport).
func decompressBody(resp *http.Response, limit int64) ([]byte, error) {
	// When the transport already decompressed the body (e.g. Go's default
	// gzip handling), Uncompressed is true and the Content-Encoding header
	// may still be present. Reading raw is correct.
	if resp.Uncompressed {
		return io.ReadAll(io.LimitReader(resp.Body, limit))
	}

	encoding := resp.Header.Get("Content-Encoding")
	reader := io.LimitReader(resp.Body, limit)

	switch encoding {
	case "br":
		br := brotli.NewReader(reader)
		return io.ReadAll(br)
	case "gzip":
		// Buffer the body so we can fall back to raw bytes if the payload
		// is not actually gzip-encoded. This happens when the transport or
		// a CDN decompressed the body but left the Content-Encoding header,
		// or when the server advertises gzip but sends identity/Brotli.
		raw, err := io.ReadAll(reader)
		if err != nil {
			return nil, fmt.Errorf("gzip read raw: %w", err)
		}
		gr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			// Not valid gzip — return the raw bytes as-is.
			slog.Debug("Content-Encoding says gzip but body is not gzip, using raw",
				"error", err.Error(), "body_len", len(raw))
			return raw, nil
		}
		defer func() { _ = gr.Close() }()
		decoded, err := io.ReadAll(gr)
		if err != nil {
			// Gzip header valid but decompression failed mid-stream.
			slog.Debug("gzip decompression failed mid-stream, using raw",
				"error", err.Error(), "body_len", len(raw))
			return raw, nil
		}
		return decoded, nil
	case "zstd":
		zr, err := zstd.NewReader(reader)
		if err != nil {
			return nil, fmt.Errorf("zstd reader: %w", err)
		}
		defer zr.Close()
		return io.ReadAll(zr)
	default:
		// No encoding or "identity" — read raw.
		return io.ReadAll(reader)
	}
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
