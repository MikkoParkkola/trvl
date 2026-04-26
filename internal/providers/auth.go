package providers

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

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
	if pc.config.Auth == nil || pc.config.Auth.PreflightURL == "" {
		// No preflight needed — but the caller may still rely on existing
		// pc.authValues populated by other paths (header-based auth, env tokens).
		// Return a snapshot so the caller's later read is race-free.
		return snapshotAuthValuesLocked(pc), nil
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
		// Tier 3a: read cookies from user's browser (kooky).
		if tryBrowserCookieRetry(ctx, pc, &resolvedAuth) {
			saveCachedCookies(pc.client, resolvedURL)
			pc.lastPreflightURL = resolvedURL
			pc.authExpiry = time.Now().Add(pc.effectiveCacheTTL())
			return copyAuthValues(pc.authValues), nil
		}
		// Tier 3b: run WAF challenge.js in sobek JS engine (pure Go).
		if tryWAFSolve(ctx, pc, &resolvedAuth, resp.StatusCode, body) {
			saveCachedCookies(pc.client, resolvedURL)
			pc.lastPreflightURL = resolvedURL
			pc.authExpiry = time.Now().Add(pc.effectiveCacheTTL())
			return copyAuthValues(pc.authValues), nil
		}
		// Tier 4: last-resort escape hatch — open in browser.
		if resolvedAuth.BrowserEscapeHatch && isInteractive(ctx) {
			if tryBrowserEscapeHatch(ctx, pc, &resolvedAuth) {
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

// copyAuthValues returns a defensive copy of m. Always called under either
// pc.authMu read or write lock to avoid concurrent-map-iteration. Returns a
// non-nil map even when m is empty/nil so callers can iterate freely.
func copyAuthValues(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// snapshotAuthValuesLocked acquires the read lock to take a defensive copy.
// Used in the no-preflight code path where callers expected pc.authValues
// directly; the snapshot eliminates the cross-call race in MIK-3070.
func snapshotAuthValuesLocked(pc *providerClient) map[string]string {
	pc.authMu.RLock()
	defer pc.authMu.RUnlock()
	return copyAuthValues(pc.authValues)
}

// tryBrowserCookieRetry is Tier 3: read cookies from the user's disk-backed
// browser stores, seed them into the client jar, and retry preflight. Returns
// true on HTTP 2xx + successful extraction. The auth parameter carries the
// resolved (city-specific) preflight URL.
func tryBrowserCookieRetry(ctx context.Context, pc *providerClient, auth *AuthConfig) bool {
	if !applyBrowserCookies(pc.client, auth.PreflightURL, pc.config.Cookies.Browser) {
		return false
	}
	resp2, body2, err2 := doPreflightRequest(ctx, pc.client, auth)
	if err2 != nil || resp2.StatusCode < 200 || resp2.StatusCode >= 300 {
		return false
	}
	// Reject 202 challenge pages — they are in the 2xx range but are WAF
	// interstitials, not real responses.
	if isAkamaiChallenge(resp2.StatusCode, body2) {
		return false
	}
	for k := range pc.authValues {
		delete(pc.authValues, k)
	}
	applyExtractions(auth.Extractions, resp2, body2, pc.authValues)
	applyURLExtractions(ctx, pc.client, auth.Extractions, pc.authValues)
	return true
}

// tryWAFSolve is Tier 3b: if the preflight response looks like an AWS WAF
// challenge page (HTTP 202 with *.awswaf.com script refs), run challenge.js
// in the sobek JS engine to obtain an aws-waf-token cookie, then retry
// preflight. Returns true on success. The auth parameter carries the
// resolved (city-specific) preflight URL.
func tryWAFSolve(ctx context.Context, pc *providerClient, auth *AuthConfig, statusCode int, pageBody []byte) bool {
	// Only attempt on HTTP 202 (AWS WAF challenge) or 403 (some WAF variants).
	if statusCode != http.StatusAccepted && statusCode != http.StatusForbidden {
		return false
	}

	pageURL := auth.PreflightURL
	cookie, err := waf.SolveAWSWAF(ctx, pc.client, pageURL, string(pageBody), nil)
	if err != nil {
		slog.Debug("waf solver did not produce a token", "provider", pc.config.ID, "error", err.Error())
		return false
	}

	// Install the token cookie into the client jar.
	u, err := url.Parse(pageURL)
	if err != nil {
		return false
	}
	pc.client.Jar.SetCookies(u, []*http.Cookie{cookie})
	slog.Info("waf solver obtained aws-waf-token via JS engine", "provider", pc.config.ID)

	// Retry preflight with the fresh token.
	resp2, body2, err2 := doPreflightRequest(ctx, pc.client, auth)
	if err2 != nil || resp2.StatusCode < 200 || resp2.StatusCode >= 300 {
		return false
	}
	// Reject 202 challenge pages — still a WAF interstitial despite being 2xx.
	if isAkamaiChallenge(resp2.StatusCode, body2) {
		return false
	}
	for k := range pc.authValues {
		delete(pc.authValues, k)
	}
	applyExtractions(auth.Extractions, resp2, body2, pc.authValues)
	applyURLExtractions(ctx, pc.client, auth.Extractions, pc.authValues)
	return true
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
func tryBrowserEscapeHatch(ctx context.Context, pc *providerClient, auth *AuthConfig) bool {
	targetURL := auth.PreflightURL
	browserPref := pc.config.Cookies.Browser

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
			return false
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
		return false
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
		return false
	}

	if pc.client == nil || pc.client.Jar == nil {
		return false
	}
	u, err := url.Parse(targetURL)
	if err != nil {
		return false
	}
	pc.client.Jar.SetCookies(u, fresh)

	resp2, body2, err2 := doPreflightRequest(ctx, pc.client, auth)
	if err2 != nil || resp2.StatusCode < 200 || resp2.StatusCode >= 300 {
		slog.Warn("browser escape hatch: preflight retry still failed",
			"provider", pc.config.ID)
		return false
	}
	// Reject 202 challenge pages — still a WAF interstitial despite being 2xx.
	if isAkamaiChallenge(resp2.StatusCode, body2) {
		slog.Warn("browser escape hatch: preflight retry returned another challenge page",
			"provider", pc.config.ID)
		return false
	}
	for k := range pc.authValues {
		delete(pc.authValues, k)
	}
	applyExtractions(auth.Extractions, resp2, body2, pc.authValues)
	applyURLExtractions(ctx, pc.client, auth.Extractions, pc.authValues)
	slog.Info("browser escape hatch: preflight recovered", "provider", pc.config.ID)
	return true
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
	defer resp.Body.Close()

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
	defer resp.Body.Close()

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
		resp.Body.Close()
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

// applyBrowserCookies reads cookies from the user's browsers for the given
// URL and seeds them into the client's cookie jar. When browserHint is
// non-empty, reads only from that specific browser to avoid cross-browser
// cookie contamination. Returns true if any cookies were applied.
func applyBrowserCookies(client *http.Client, targetURL, browserHint string) bool {
	if client == nil || client.Jar == nil {
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
	client.Jar.SetCookies(u, cookies)
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
		defer gr.Close()
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
