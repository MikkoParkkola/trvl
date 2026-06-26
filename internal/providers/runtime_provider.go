package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/waf"
)

func (rt *Runtime) searchProvider(ctx context.Context, cfg *ProviderConfig, location string, lat, lon float64, checkin, checkout, currency string, guests int, filters *HotelFilterParams) ([]models.HotelResult, error) {
	// Pick up on-disk edits without an MCP restart. If the file mtime has
	// advanced since we last parsed it, ReloadIfChanged swaps in the fresh
	// config; we then drop the cached providerClient so its HTTP client,
	// rate limiter and auth cache are rebuilt from the new config.
	var oldJar http.CookieJar
	if fresh := rt.registry.ReloadIfChanged(cfg.ID); fresh != nil && fresh != cfg {
		// Preserve the cookie jar so WAF tokens and session cookies survive
		// config reloads. The jar is installed on the new client below.
		rt.mu.Lock()
		if old := rt.clients[cfg.ID]; old != nil && old.client != nil {
			oldJar = old.client.Jar
		}
		delete(rt.clients, cfg.ID)
		rt.mu.Unlock()
		cfg = fresh
	}
	pc := rt.getOrCreateClient(cfg)
	if oldJar != nil && pc.client != nil {
		pc.client.Jar = oldJar
	}

	// Rate limit.
	if err := pc.limiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit: %w", err)
	}

	// Build variable map early — the preflight URL may contain ${city_id}
	// or other search-specific placeholders that must be resolved before
	// the preflight request fires. Without this, Booking's WAF rejects
	// requests because cookies obtained for one dest_id (e.g. Paris) are
	// tied to that city and fail when the actual search targets another.
	neLat := lat + boundingBoxOffset
	neLon := lon + boundingBoxOffset
	swLat := lat - boundingBoxOffset
	swLon := lon - boundingBoxOffset

	// Compute num_nights from checkin/checkout for providers that need it
	// (e.g. Hostelworld's num-nights query param).
	numNights := "1"
	if tIn, err := models.ParseDate(checkin); err == nil {
		if tOut, err := models.ParseDate(checkout); err == nil {
			if n := int(tOut.Sub(tIn).Hours() / 24); n > 0 {
				numNights = strconv.Itoa(n)
			}
		}
	}

	vars := map[string]string{
		"${checkin}":    checkin,
		"${checkout}":   checkout,
		"${currency}":   currency,
		"${guests}":     strconv.Itoa(guests),
		"${lat}":        strconv.FormatFloat(lat, 'f', 6, 64),
		"${lon}":        strconv.FormatFloat(lon, 'f', 6, 64),
		"${ne_lat}":     strconv.FormatFloat(neLat, 'f', 6, 64),
		"${ne_lon}":     strconv.FormatFloat(neLon, 'f', 6, 64),
		"${sw_lat}":     strconv.FormatFloat(swLat, 'f', 6, 64),
		"${sw_lon}":     strconv.FormatFloat(swLon, 'f', 6, 64),
		"${location}":   location,
		"${num_nights}": numNights,
	}

	// Resolve provider-specific city ID. First check the static lookup
	// table; if not found, fall back to the dynamic city_resolver API.
	if id := resolveCityID(cfg.CityLookup, location); id != "" {
		vars["${city_id}"] = id
		// When the endpoint uses ${location} rather than ${city_id} (e.g.
		// Airbnb embeds the location slug directly in the URL path), override
		// ${location} with the looked-up value so the provider gets a
		// URL-safe slug instead of raw user input.
		if !strings.Contains(cfg.Endpoint, "${city_id}") {
			vars["${location}"] = id
		}
	} else if cfg.CityResolver != nil {
		if id, err := resolveCityIDDynamic(ctx, cfg, pc.client, location, rt.registry); err != nil {
			slog.Warn("city_resolver failed, continuing without city_id",
				"provider", cfg.ID, "location", location, "error", err.Error())
		} else {
			vars["${city_id}"] = id
			if !strings.Contains(cfg.Endpoint, "${city_id}") {
				vars["${location}"] = id
			}
		}
	}

	// When cookies.source is "browser", unconditionally seed the client's
	// cookie jar with the user's real browser cookies BEFORE preflight.
	// This carries JS-written sensor cookies (Akamai bm_sz, PerimeterX
	// _pxhd) that bot-detection systems validate server-side. Without
	// them, providers like Booking.com classify the request as b_bot and
	// strip review scores from the SSR response.
	browserCookiesApplied := false
	if cfg.Cookies.Source == "browser" {
		endpointURL := cfg.Endpoint
		if cfg.Auth != nil && cfg.Auth.PreflightURL != "" {
			endpointURL = substituteVars(cfg.Auth.PreflightURL, vars)
		}
		browserCookiesApplied = applyBrowserCookies(pc.client, endpointURL, cfg.Cookies.Browser)

		// Fail loudly when a browser-cookie provider (e.g. Booking.com) has no
		// usable browser session. Without cookies the WAF strips data and the
		// search silently returns nothing, hiding the real cause from the user.
		// Returning a typed error here routes through the standard per-provider
		// error path, where classifyProviderError tags it BOOKING_COOKIES_MISSING
		// with an actionable fix hint.
		//
		// Gated on BrowserEscapeHatch: those providers treat browser cookies as
		// the auth mechanism with no server-side recovery, so missing cookies are
		// a hard, actionable failure. Providers without the escape hatch (e.g. a
		// WAF that can still be cleared on a retry) keep their existing recovery
		// path and are not short-circuited here.
		if !browserCookiesApplied && cfg.Auth != nil && cfg.Auth.BrowserEscapeHatch {
			return nil, fmt.Errorf("browser cookies missing for %s: no cookies found for the configured browser (kooky auto-detects from an installed, logged-in browser)", cfg.ID)
		}
	}

	// Preflight auth if needed. The preflight URL is resolved with
	// search-specific vars so that ${city_id} etc. produce a city-specific
	// WAF session rather than reusing a hardcoded one.
	//
	// When browser cookies were successfully loaded AND the auth config has
	// no extractions (i.e. preflight's only purpose is cookie seeding), skip
	// the preflight entirely. Running preflight with a non-fingerprinted HTTP
	// client causes the server to set new session cookies (via Set-Cookie) that
	// overwrite the browser's authenticated cookies in the jar — replacing a
	// real-user session with a bot-classified one. This is the root cause of
	// Booking.com returning 0 results despite having valid browser cookies.
	if cfg.Auth != nil && cfg.Auth.Type == "preflight" {
		skipPreflight := browserCookiesApplied && len(cfg.Auth.Extractions) == 0
		if skipPreflight {
			slog.Info("skipping preflight: browser cookies already loaded, no extractions needed",
				"provider", cfg.ID)
		} else if _, err := rt.runPreflight(ctx, pc, vars); err != nil {
			return nil, fmt.Errorf("preflight: %w", err)
		}
	}

	// Add filter variables when provided. These allow provider URL
	// templates and query params to reference ${min_price}, ${max_price},
	// ${property_type}, ${sort}, ${stars}, ${min_rating}, ${amenities},
	// ${free_cancellation}, and criteria-first occupancy/rate fields.
	applyFilterVars(filters, cfg, currency, vars)

	// Build composite filter parameters (e.g. Booking's nflt) from
	// individual filter vars. Only active (non-empty) parts are joined.
	if fc := cfg.FilterComposite; fc != nil && fc.TargetVar != "" {
		var parts []string
		for filterVar, prefix := range fc.Parts {
			if val := vars["${"+filterVar+"}"]; val != "" {
				// Apply scale if defined (e.g. min_rating × 10 for Booking's 0-100 scale).
				if scale, hasScale := fc.Scales[filterVar]; hasScale && scale != 0 {
					if f, err := strconv.ParseFloat(val, 64); err == nil {
						val = strconv.Itoa(int(f * scale))
					}
				}
				// Multi-value support: if the value contains commas (e.g.
				// amenity_ids "107,433"), expand to separate prefix+id parts
				// so Booking gets hotelfacility%3D107%3Bhotelfacility%3D433.
				if strings.Contains(val, ",") {
					for _, sub := range strings.Split(val, ",") {
						sub = strings.TrimSpace(sub)
						if sub != "" {
							parts = append(parts, prefix+sub)
						}
					}
				} else {
					parts = append(parts, prefix+val)
				}
			}
		}
		vars["${"+fc.TargetVar+"}"] = strings.Join(parts, fc.Separator)
	}

	// Add auth-extracted variables.
	pc.authMu.RLock()
	for k, v := range pc.authValues {
		vars["${"+k+"}"] = v
	}
	pc.authMu.RUnlock()

	// Build endpoint URL. After substitution, strip any remaining ${...}
	// placeholders and their preceding &/? separators so optional filter
	// params that weren't set don't produce malformed URLs (e.g.
	// "&nflt=${nflt}" → removed entirely when no filters are active).
	endpoint := substituteVars(cfg.Endpoint, vars)
	endpoint = stripUnresolvedPlaceholders(endpoint)

	// Build query params.
	if len(cfg.QueryParams) > 0 {
		u, err := url.Parse(endpoint)
		if err != nil {
			return nil, fmt.Errorf("parse endpoint: %w", err)
		}
		q := u.Query()
		for k, v := range cfg.QueryParams {
			resolved := substituteVars(v, vars)
			// Skip query params whose value still contains an unresolved
			// ${placeholder} — this happens when an optional filter (e.g.
			// ${property_type}, ${min_price}) was not set by the caller.
			// Sending a literal "${property_type}" as a query value would
			// confuse the provider's API.
			if strings.Contains(resolved, "${") {
				continue
			}
			// Also skip params that resolved to empty string when the
			// original template was a pure placeholder (e.g. "${sort}").
			// Sending sort= (empty) causes HTTP 400 on providers like
			// Hostelworld that validate sort values strictly.
			if resolved == "" && strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}") {
				continue
			}
			// Array params (e.g. "amenities[]"): if the key ends in [] and
			// the value contains commas, add each value as a separate param
			// so Airbnb gets amenities[]=4&amenities[]=7 instead of amenities[]=4,7.
			if strings.HasSuffix(k, "[]") && strings.Contains(resolved, ",") {
				for _, sub := range strings.Split(resolved, ",") {
					sub = strings.TrimSpace(sub)
					if sub != "" {
						q.Add(k, sub)
					}
				}
				continue
			}
			q.Set(k, resolved)
		}
		u.RawQuery = q.Encode()
		endpoint = u.String()
	}

	// Build body.
	var bodyReader io.Reader
	if cfg.Method == "POST" && cfg.BodyTemplate != "" {
		bodyReader = strings.NewReader(substituteVars(cfg.BodyTemplate, vars))
	}

	req, err := http.NewRequestWithContext(ctx, cfg.Method, endpoint, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	// Add headers in deterministic order when header_order is configured.
	// WAF/bot-detection systems (Booking.com, Akamai) fingerprint header
	// ordering. Go's map iteration is random, so without explicit ordering
	// every request has a different header sequence — a bot fingerprint.
	if len(cfg.HeaderOrder) > 0 {
		added := make(map[string]bool, len(cfg.HeaderOrder))
		for _, k := range cfg.HeaderOrder {
			if v, ok := cfg.Headers[k]; ok {
				req.Header.Set(k, substituteEnvVars(substituteVars(v, vars)))
				added[k] = true
			}
		}
		// Append any headers not listed in the order (safety net).
		for k, v := range cfg.Headers {
			if !added[k] {
				req.Header.Set(k, substituteEnvVars(substituteVars(v, vars)))
			}
		}
	} else {
		for k, v := range cfg.Headers {
			req.Header.Set(k, substituteEnvVars(substituteVars(v, vars)))
		}
	}

	// Log jar cookie count at debug level for diagnostics.
	if pc.client.Jar != nil {
		if u2, err2 := url.Parse(endpoint); err2 == nil {
			slog.Debug("jar cookies before search request",
				"provider", cfg.ID,
				"cookie_count", len(pc.client.Jar.Cookies(u2)))
		}
	}

	// Transparency header: identify the tool to the operator without
	// concealing its nature. Providers who object can block on this
	// header; providers who don't are implicitly tolerating personal-use
	// access. Note: this does not remove any User-Agent header the
	// config sets (some providers require a browser UA to avoid WAF
	// blocks), it adds alongside.
	//
	// Skip this header for browser-cookie providers: adding a non-standard
	// header breaks the browser-identical request fingerprint that makes
	// the session cookies valid. Booking.com's WAF correlates the session
	// cookie with the original request fingerprint — an unknown header
	// causes it to serve a degraded response (0 hotel results in the SSR
	// Apollo cache despite HTTP 200).
	if cfg.Cookies.Source != "browser" {
		req.Header.Set("X-Personal-Use", "trvl personal noncommercial https://github.com/MikkoParkkola/trvl")
	}

	// Send request.
	resp, err := pc.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := decompressBody(resp, maxResponseBytes)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	slog.Debug("search response", "provider", cfg.ID, "status", resp.StatusCode, "body_len", len(body),
		"content_encoding", resp.Header.Get("Content-Encoding"),
		"is_challenge", isAkamaiChallenge(resp.StatusCode, body))

	// Detect Akamai/AWS WAF challenge pages. HTTP 202 is in the 2xx range so
	// the generic status check below would accept it, but the body is an HTML
	// challenge page — not the real API response. When detected, run the same
	// Tier 3/4 escape-hatch cascade that runPreflight uses: browser cookies →
	// WAF JS solver → browser escape hatch. If any tier succeeds, retry the
	// main request with the fresh cookies.
	if isAkamaiChallenge(resp.StatusCode, body) {
		slog.Info("search response is an Akamai/WAF challenge page, attempting cookie recovery",
			"provider", cfg.ID, "status", resp.StatusCode)

		recovered := false

		// Tier 3a: re-read cookies from the user's browser.
		if applyBrowserCookies(pc.client, endpoint, cfg.Cookies.Browser) {
			resp2, body2, err2 := doSearchRequest(ctx, pc.client, req)
			if err2 == nil && !isAkamaiChallenge(resp2.StatusCode, body2) && resp2.StatusCode >= 200 && resp2.StatusCode < 300 {
				resp, body = resp2, body2
				recovered = true
				slog.Info("search challenge bypassed via browser cookies", "provider", cfg.ID)
			}
		}

		// Tier 3b: WAF JS solver.
		if !recovered {
			cookie, wafErr := waf.SolveAWSWAF(ctx, pc.client, endpoint, string(body), nil)
			if wafErr == nil && cookie != nil {
				if u, parseErr := url.Parse(endpoint); parseErr == nil {
					pc.client.Jar.SetCookies(u, []*http.Cookie{cookie})
				}
				resp2, body2, err2 := doSearchRequest(ctx, pc.client, req)
				if err2 == nil && !isAkamaiChallenge(resp2.StatusCode, body2) && resp2.StatusCode >= 200 && resp2.StatusCode < 300 {
					resp, body = resp2, body2
					recovered = true
					slog.Info("search challenge bypassed via WAF JS solver", "provider", cfg.ID)
				}
			}
		}

		// Tier 4: browser escape hatch.
		if !recovered && cfg.Auth != nil && cfg.Auth.BrowserEscapeHatch && isInteractive(ctx) {
			if tryBrowserEscapeHatch(ctx, pc, cfg.Auth) {
				resp2, body2, err2 := doSearchRequest(ctx, pc.client, req)
				if err2 == nil && !isAkamaiChallenge(resp2.StatusCode, body2) && resp2.StatusCode >= 200 && resp2.StatusCode < 300 {
					resp, body = resp2, body2
					recovered = true
					slog.Info("search challenge bypassed via browser escape hatch", "provider", cfg.ID)
				}
			}
		}

		if !recovered {
			return nil, fmt.Errorf("http %d: WAF/JS challenge page — all cookie recovery tiers failed (provider %s)", resp.StatusCode, cfg.ID)
		}
	}

	// Retry on HTTP 429 honouring the Retry-After header (MIK-3071).
	const maxRetries429 = 2
	for attempt := 0; attempt < maxRetries429 && resp.StatusCode == http.StatusTooManyRequests; attempt++ {
		delay := RetryAfterOrDefault(resp.Header.Get("Retry-After"), time.Now())
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		var retryErr error
		resp, body, retryErr = doSearchRequest(ctx, pc.client, req)
		if retryErr != nil {
			return nil, retryErr
		}
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("rate limit: %d retries exhausted (http 429): %s", maxRetries429, string(body[:min(len(body), 200)]))
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, string(body[:min(len(body), 200)]))
	}

	// If the provider embeds its API response inside an HTML body (e.g.
	// Booking SSR'd Apollo cache), apply the configured regex to pull the
	// JSON blob out first. Capture group 1 replaces `body` for JSON parsing.
	if pattern := cfg.ResponseMapping.BodyExtractPattern; pattern != "" {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("compile body_extract_pattern: %w", err)
		}
		m := re.FindSubmatch(body)
		if len(m) < 2 {
			slog.Debug("body_extract_pattern did not match",
				"provider", cfg.ID,
				"body_len", len(body),
				"body_prefix", string(body[:min(len(body), 300)]))
			return nil, fmt.Errorf("body_extract_pattern %q did not match response body", pattern)
		}
		slog.Debug("body_extract_pattern matched", "provider", cfg.ID, "extract_len", len(m[1]))
		body = m[1]
	}

	// Parse JSON.
	var raw any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse json: %w", err)
	}

	// Unwrap Airbnb Niobe SSR cache: {"niobeClientData":[[key, {data:...}]]}
	// into the inner payload so results_path can resolve normally.
	raw = unwrapNiobe(raw)

	// If the parsed JSON is an Apollo normalized cache (detected by a
	// top-level ROOT_QUERY key), resolve __ref pointers so that jsonPath
	// can traverse the data as a plain denormalized tree. This is required
	// for SSR-extracted providers like Booking.com where nested objects
	// (reviewScore, location, pricing) are stored as separate cache entries
	// linked via {"__ref": "BasicPropertyData:12345"}.
	if cache, ok := raw.(map[string]any); ok {
		if rootQuery, hasRoot := cache["ROOT_QUERY"]; hasRoot {
			// Only denormalize the ROOT_QUERY subtree, using the full cache
			// as the ref-lookup source. Denormalizing the entire top-level
			// cache would poison the `seen` set (cycle guard) with refs
			// encountered via different top-level keys, causing legitimate
			// multi-use refs (e.g. ReviewScore:42 used by both the top-level
			// entity AND the ROOT_QUERY chain) to appear circular.
			cache["ROOT_QUERY"] = denormalizeApollo(rootQuery, cache, nil)

			// Diagnostic: Booking.com moved hotel results to CSR in early 2026.
			// The Apollo SSR cache has search shell (filters, pagination) but
			// results[] is empty. Production config now uses dml/graphql
			// endpoint directly (booking.json). This SSR path remains as
			// fallback for any provider still using Apollo SSR rendering.
			if cfg.ID == "booking" {
				if rqMap, ok := cache["ROOT_QUERY"].(map[string]any); ok {
					slog.Debug("apollo cache diagnostic",
						"provider", cfg.ID,
						"root_keys", len(rqMap))
					// Check searchQueries for results count
					if sq, ok := rqMap["searchQueries"].(map[string]any); ok {
						slog.Debug("apollo searchQueries",
							"provider", cfg.ID,
							"keys", len(sq))
						// Scan search* keys for results array count.
						// Booking moved to CSR in 2026 — SSR results[] is
						// typically empty. Log at debug level to track when
						// Booking restores SSR rendering or changes the key
						// structure again. Production booking.json now uses
						// dml/graphql directly, bypassing this SSR path.
						for k, val := range sq {
							if !strings.HasPrefix(k, "search") {
								continue
							}
							inner, ok := val.(map[string]any)
							if !ok {
								continue
							}
							resultsVal, hasResults := inner["results"]
							if !hasResults {
								slog.Debug("apollo search: no results key",
									"provider", cfg.ID)
								continue
							}
							switch rv := resultsVal.(type) {
							case []any:
								slog.Debug("apollo search results",
									"provider", cfg.ID,
									"result_count", len(rv),
									"inner_keys", len(inner))
							case map[string]any:
								slog.Debug("apollo search results is object",
									"provider", cfg.ID,
									"object_keys", len(rv))
							default:
								slog.Debug("apollo search results unexpected type",
									"provider", cfg.ID,
									"type", fmt.Sprintf("%T", resultsVal))
							}
						}
					}
				}
			}
		}
	}

	// If the response carries a top-level "errors" field (GraphQL convention),
	// check whether this is a complete failure or a partial success.
	// GraphQL allows {"data": {...}, "errors": [...]} — partial results with
	// non-fatal errors (e.g. Booking returns data + errors from sub-resolvers
	// like hotelpage/district). Only abort when there is NO data at all.
	if topObj, ok := raw.(map[string]any); ok {
		if errs, hasErrs := topObj["errors"].([]any); hasErrs && len(errs) > 0 {
			_, hasData := topObj["data"]
			if !hasData {
				// No data at all — this is a complete failure.
				if firstErr, _ := errs[0].(map[string]any); firstErr != nil {
					msg, _ := firstErr["message"].(string)
					code := ""
					if ext, _ := firstErr["extensions"].(map[string]any); ext != nil {
						code, _ = ext["code"].(string)
					}
					if msg == "" && code == "" {
						msg = "unknown graphql error"
					}
					return nil, fmt.Errorf("graphql error: %s%s", msg, func() string {
						if code != "" {
							return " [" + code + "]"
						}
						return ""
					}())
				}
			}
			// Partial success: log the errors at debug level but continue
			// processing data. Booking.com's GraphQL often includes non-fatal
			// errors from sub-resolvers (hotelpage service) alongside valid
			// search results.
			slog.Debug("graphql partial errors (continuing with data)",
				"provider", cfg.ID,
				"error_count", len(errs))
		}
	}

	// Extract results array.
	resultsRaw := jsonPath(raw, cfg.ResponseMapping.ResultsPath)
	arr, ok := resultsRaw.([]any)
	slog.Debug("results_path resolution", "provider", cfg.ID,
		"path", cfg.ResponseMapping.ResultsPath,
		"resolved_type", fmt.Sprintf("%T", resultsRaw),
		"is_array", ok,
		"count", func() int {
			if ok {
				return len(arr)
			}
			return -1
		}())
	// For Apollo-cache providers (e.g. Booking), log empty-results at debug
	// level so operators can diagnose SSR-vs-CSR rendering issues.
	if ok && len(arr) == 0 {
		slog.Debug("results_path resolved to empty array",
			"provider", cfg.ID, "body_len", len(body),
			"path", cfg.ResponseMapping.ResultsPath)
	}
	// Booking.com CSR migration note (2026-04): Apollo SSR cache still
	// has the search shell (filters, pagination, sorters) but results[]
	// is empty. Diagnostic logging for this is in the Apollo denorm
	// block above. When Booking restores SSR or we switch to GraphQL,
	// the results_path will resolve normally again. Until then, Booking
	// returns 0 results and other providers (Google, Trivago, Airbnb,
	// Hostelworld) provide coverage.
	if !ok {
		// Include a body snippet + detected top-level keys so the LLM (and
		// human) can see what actually came back. This is the difference
		// between "mystery failure" and "ah, persistedQueryNotFound".
		snippet := string(body)
		if len(snippet) > 400 {
			snippet = snippet[:400] + "..."
		}
		var topKeys string
		if topObj, ok := raw.(map[string]any); ok {
			keys := make([]string, 0, len(topObj))
			for k := range topObj {
				keys = append(keys, k)
			}
			topKeys = fmt.Sprintf(" (top-level keys: %v)", keys)
		}
		return nil, fmt.Errorf("results_path %q did not resolve to an array%s; body: %s",
			cfg.ResponseMapping.ResultsPath, topKeys, snippet)
	}

	// Map each element to HotelResult and tag with provider source.
	hotels := make([]models.HotelResult, 0, len(arr))
	for _, item := range arr {
		h := mapHotelResult(item, cfg.ResponseMapping.Fields)
		// Normalize rating to 0-10 scale when the provider uses a different
		// range (e.g. Booking GraphQL returns 0-5, Hostelworld 0-100).
		if scale := cfg.ResponseMapping.RatingScale; scale > 0 && h.Rating > 0 {
			h.Rating = h.Rating * scale
		}
		src := models.PriceSource{
			Provider: cfg.ID,
			Price:    h.Price,
			Currency: h.Currency,
		}
		// Extract room-level price spread from Booking-style "blocks" array.
		if maxP, roomCt := extractBlocksPriceSpread(item); roomCt > 0 {
			src.MaxPrice = maxP
			src.RoomCount = roomCt
		}

		// Extract room types from Booking-style blocks/unitConfigurations.
		if len(h.RoomTypes) == 0 {
			if rt := extractRoomTypes(item); len(rt) > 0 {
				h.RoomTypes = rt
			}
		}

		// Extract image URL from Booking-style basicPropertyData.photos.
		if h.ImageURL == "" {
			if img := extractImageURL(item); img != "" {
				h.ImageURL = img
			}
		}

		// Extract property description from Booking-style fields.
		if h.Description == "" {
			if desc := extractDescription(item); desc != "" {
				h.Description = desc
			}
		}

		// Extract neighborhood from Booking-style location data.
		if h.Neighborhood == "" {
			if nb := extractNeighborhood(item); nb != "" {
				h.Neighborhood = nb
			}
		}

		// Construct booking URL from pageName + countryCode when available.
		// Booking.com SSR results contain basicPropertyData.pageName (e.g.
		// "aix-europe") and basicPropertyData.location.countryCode (e.g. "fr")
		// which combine into the canonical hotel URL:
		// https://www.booking.com/hotel/{cc}/{pageName}.html
		if h.BookingURL == "" {
			if pageName, _ := jsonPath(item, "basicPropertyData.pageName").(string); pageName != "" {
				cc, _ := jsonPath(item, "basicPropertyData.location.countryCode").(string)
				if cc == "" {
					cc = "xx" // fallback — Booking will redirect
				}
				h.BookingURL = "https://www.booking.com/hotel/" + cc + "/" + pageName + ".html"
				src.BookingURL = h.BookingURL
			}
		}

		// Construct Airbnb booking URL from hotel_id. Airbnb search results
		// expose demandStayListing.id but no booking URL field. The canonical
		// listing URL is https://www.airbnb.com/rooms/{id}.
		if h.BookingURL == "" && cfg.ID == "airbnb" && h.HotelID != "" {
			h.BookingURL = "https://www.airbnb.com/rooms/" + h.HotelID
			src.BookingURL = h.BookingURL
		}

		h.RoomTypes = tagProviderRoomTypes(h.RoomTypes, cfg, src, h.BookingURL, currency)
		h.Sources = []models.PriceSource{src}

		// Normalize top-level price to the requested currency so
		// cross-provider comparison works (e.g. USD Booking vs EUR Google).
		// Airbnb returns prices in the requested currency but leaves the
		// currency field empty — treat empty as already-correct.
		srcCurrency := h.Currency
		if srcCurrency == "" {
			srcCurrency = currency // assume price is in the requested currency
		}
		h.Price = normalizePrice(h.Price, srcCurrency, currency)
		h.Currency = currency

		// Update source currency too — it was captured before the fallback.
		if len(h.Sources) > 0 && h.Sources[0].Currency == "" {
			h.Sources[0].Currency = currency
		}

		// Normalize rating scales: Hostelworld uses 0-100, Booking 0-10,
		// Google 0-5. Detect and normalize to a consistent 0-10 scale for
		// cross-provider comparison. Hostelworld ratings > 10 are on the
		// 0-100 scale; divide by 10 to get 0-10.
		if h.Rating > 10 {
			h.Rating = h.Rating / 10.0
		}

		hotels = append(hotels, h)
	}

	// Rating enrichment: when hotels have a BookingURL but rating=0, fetch
	// the detail page to extract the JSON-LD aggregateRating. This only
	// fires for providers that produce booking URLs (currently Booking.com).
	// Capped at 5 enrichments per search to limit latency.
	enrichRatings(ctx, pc.client, hotels, cfg)

	// Description enrichment: Airbnb search results never contain listing
	// descriptions — they are only available on the individual listing (PDP)
	// pages. Fetch the top N Airbnb listing pages in parallel and extract
	// the description from the embedded Niobe SSR cache.
	if cfg.ID == "airbnb" {
		enrichAirbnbDescriptions(ctx, pc.client, hotels)
	}

	return hotels, nil
}
