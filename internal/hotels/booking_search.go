package hotels

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/logredact"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/providers"
	"golang.org/x/time/rate"
)

var bookingSearchLimiter = rate.NewLimiter(rate.Every(3*time.Second), 1)

const (
	bookingBaseURL    = "https://www.booking.com"
	bookingWAFCookie  = "aws-waf-token"
	bookingAcceptHTML = "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"
)

// defaultSearchBooking fetches Booking.com search results without requiring the
// user to "visit booking.com first". It harvests an aws-waf-token (from the
// ~/.trvl/cookies cache, the user's installed browser via kooky, or — as a
// last resort — a headless CDP token harvest), then performs a plain Chrome-
// fingerprinted GET and parses the embedded Apollo JSON store. A 202 response
// means the token is stale, so it re-harvests once and retries.
func defaultSearchBooking(ctx context.Context, location string, opts HotelSearchOptions) ([]models.HotelResult, error) {
	if err := bookingSearchLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("booking rate limiter: %w", err)
	}

	adults := opts.Guests
	if adults <= 0 {
		adults = 2
	}
	searchURL := buildBookingSearchURL(location, opts.CheckIn, opts.CheckOut, opts.Currency, adults, 0)

	body, err := fetchBookingSearch(ctx, searchURL)
	if err != nil {
		return nil, fmt.Errorf("fetch booking search page: %w", err)
	}

	blob := extractBookingApolloBlob(body)
	if blob == "" {
		return nil, fmt.Errorf("booking search: apollo store not found in page")
	}
	hotels, perr := parseBookingApollo(blob, opts.Currency)
	if perr != nil {
		return nil, perr
	}

	// Apply client-side filters.
	var filtered []models.HotelResult
	for _, h := range hotels {
		if opts.MaxPrice > 0 && h.Price > opts.MaxPrice {
			continue
		}
		if opts.MinRating > 0 && h.Rating < opts.MinRating {
			continue
		}
		filtered = append(filtered, h)
	}

	if len(filtered) == 0 && len(hotels) > 0 {
		slog.Debug("booking search: all hotels filtered out", "total", len(hotels), "location", location)
	}

	return filtered, nil
}

// buildBookingSearchURL builds a Booking.com searchresults.html URL. offset is
// the pagination offset (rowsPerPage 25); pass 0 for the first page.
func buildBookingSearchURL(location, checkIn, checkOut, currency string, adults, offset int) string {
	q := url.Values{}
	q.Set("ss", location)
	if checkIn != "" {
		q.Set("checkin", checkIn)
	}
	if checkOut != "" {
		q.Set("checkout", checkOut)
	}
	if adults <= 0 {
		adults = 2
	}
	q.Set("group_adults", strconv.Itoa(adults))
	q.Set("group_children", "0")
	q.Set("no_rooms", "1")
	if currency != "" {
		q.Set("selected_currency", strings.ToUpper(currency))
	}
	if offset > 0 {
		q.Set("offset", strconv.Itoa(offset))
	}
	return bookingBaseURL + "/searchresults.html?" + q.Encode()
}

// bookingAcquireTokenFn and bookingGetFn are indirection seams for tests so the
// 202/403/503 re-harvest-and-retry logic can be exercised deterministically
// without a live WAF token or HTTP round-trip. Production wiring is the real
// acquireBookingWAFToken / bookingGet.
var (
	bookingAcquireTokenFn = acquireBookingWAFToken
	bookingGetFn          = bookingGet
)

// bookingWAFChallenged reports whether an HTTP status indicates the Booking.com
// WAF rejected the request (challenge / stale token) and a token re-harvest is
// warranted.
func bookingWAFChallenged(status int) bool {
	return status == 202 || status == 403 || status == 503
}

// fetchBookingSearch performs the token-seeded GET against Booking.com. On a
// 202 (WAF challenge / stale token) it re-harvests the token once and retries.
func fetchBookingSearch(ctx context.Context, searchURL string) (string, error) {
	token, err := bookingAcquireTokenFn(ctx, searchURL, false)
	if err != nil {
		return "", fmt.Errorf("acquire aws-waf-token: %w", err)
	}

	status, body, err := bookingGetFn(ctx, searchURL, token)
	if err != nil {
		return "", err
	}
	if status == 200 {
		return string(body), nil
	}

	// 202 (and 403/503) mean the WAF rejected the token. Re-harvest once.
	if bookingWAFChallenged(status) {
		slog.Debug("booking search: WAF challenge, re-harvesting token", "status", status)
		token, err = bookingAcquireTokenFn(ctx, searchURL, true)
		if err != nil {
			return "", fmt.Errorf("re-harvest aws-waf-token after status %d: %w", status, err)
		}
		status, body, err = bookingGetFn(ctx, searchURL, token)
		if err != nil {
			return "", err
		}
		if status == 200 {
			return string(body), nil
		}
		return "", fmt.Errorf("booking search returned status %d after token re-harvest", status)
	}

	return "", fmt.Errorf("booking search returned status %d", status)
}

// bookingGet issues the actual listings GET with a real Chrome User-Agent,
// Accept: text/html, and the aws-waf-token cookie. This is a plain (Tier-1)
// HTTP fetch — the browser is only ever used for token harvest, never to render.
func bookingGet(ctx context.Context, searchURL, token string) (int, []byte, error) {
	client := DefaultClient()
	headers := map[string]string{
		"Accept":          bookingAcceptHTML,
		"Accept-Language": "en-US,en;q=0.9",
	}
	if token != "" {
		headers["Cookie"] = bookingWAFCookie + "=" + token
	}
	return client.GetWithHeaders(ctx, searchURL, headers)
}

// acquireBookingWAFToken returns an aws-waf-token for booking.com. Order:
//
//	(a) the persisted ~/.trvl/cookies cache (token lifetime ~days);
//	(b) the user's installed browser cookies via kooky;
//	(c) a headless CDP harvest using the user's installed Chrome (token gets
//	    persisted to the cache by RefreshCookiesViaCDP for reuse).
//
// When forceRefresh is true, (a) and (b) are skipped and a fresh CDP harvest
// is performed (used on a 202 re-harvest and by the fixture-capture tool).
func acquireBookingWAFToken(ctx context.Context, searchURL string, forceRefresh bool) (string, error) {
	if !forceRefresh {
		if tok := awsWAFToken(providers.CachedCookiesForURL(bookingBaseURL)); tok != "" {
			return tok, nil
		}
		if tok := awsWAFToken(providers.BrowserCookiesForURLContext(ctx, bookingBaseURL)); tok != "" {
			return tok, nil
		}
	}

	cookies, err := providers.RefreshCookiesViaCDP(ctx, searchURL)
	if err != nil {
		return "", fmt.Errorf("cdp token harvest: %w", err)
	}
	if tok := awsWAFToken(cookies); tok != "" {
		return tok, nil
	}
	return "", fmt.Errorf("aws-waf-token not present after cdp harvest")
}

// awsWAFToken returns the aws-waf-token value from a cookie slice, or "".
func awsWAFToken(cookies []*http.Cookie) string {
	for _, c := range cookies {
		if c != nil && c.Name == bookingWAFCookie && c.Value != "" {
			return c.Value
		}
	}
	return ""
}

// extractBookingApolloBlob extracts the JSON payload of the
// <script data-capla-store-data="apollo" type="application/json">…</script>
// element from a Booking.com search page.
func extractBookingApolloBlob(page string) string {
	const marker = `data-capla-store-data="apollo"`
	mi := strings.Index(page, marker)
	if mi < 0 {
		return ""
	}
	// Find the end of the opening <script ...> tag.
	open := strings.Index(page[mi:], ">")
	if open < 0 {
		return ""
	}
	start := mi + open + 1
	end := strings.Index(page[start:], "</script>")
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(page[start : start+end])
}

// parseBookingApollo parses the Apollo store JSON and returns hotel results.
// It walks ROOT_QUERY → searchQueries → search(<input>) → results[], resolving
// Apollo normalized __ref pointers against the top-level cache as needed.
func parseBookingApollo(blob, currency string) ([]models.HotelResult, error) {
	var cache map[string]json.RawMessage
	if err := json.Unmarshal([]byte(blob), &cache); err != nil {
		// The apollo store <script> was present but its JSON did not parse — the
		// page shape rotated underneath us. Surface a typed parse failure so the
		// caller records it against the circuit breaker instead of a false
		// healthy-empty result.
		slog.Debug("booking apollo: top-level unmarshal failed", "error", logredact.Err(err))
		return nil, fmt.Errorf("booking apollo: store JSON could not be parsed: %w", models.ErrParseFailed)
	}

	resolver := apolloResolver{cache: cache}

	results := resolver.findSearchResults()
	if len(results) == 0 {
		// No results array anywhere in the cache. This is ambiguous between a
		// genuinely empty search and a rotated apollo path, and Booking exposes
		// no claimed-total to disambiguate, so treat it as an honest no-hit.
		// ponytail: a total page-shape loss is already caught upstream by the
		// empty-apollo-blob guard in defaultSearchBooking; only a vanished
		// results array within an otherwise-valid store reaches here.
		return nil, nil
	}

	out := make([]models.HotelResult, 0, len(results))
	seen := make(map[string]bool)
	for _, raw := range results {
		node := resolver.resolve(raw)
		if node == nil {
			continue
		}
		h := bookingResultToHotel(resolver, node, currency)
		if h.Name == "" {
			continue
		}
		key := h.Name + "|" + h.BookingURL
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, h)
	}
	if perr := bookingParseFailure(len(out), len(results)); perr != nil {
		return nil, perr
	}
	return out, nil
}

// bookingParseFailure reports a typed parse failure when Booking's Apollo store
// carried result entries but none decoded into a usable hotel — the per-result
// node shape rotated underneath bookingResultToHotel. It mirrors the flights
// parseFailureIfAllDropped discriminator: entries present and zero parsed is a
// provider-unreliable signal that must count toward the breaker, never a
// healthy empty result.
func bookingParseFailure(parsed, rawResults int) error {
	if rawResults > 0 && parsed == 0 {
		return fmt.Errorf("booking apollo: decoded 0 of %d result entries: %w", rawResults, models.ErrParseFailed)
	}
	return nil
}

// apolloResolver resolves Apollo normalized references against the flat cache.
type apolloResolver struct {
	cache map[string]json.RawMessage
}

// resolve turns a raw JSON value into an object map, following a single
// {"__ref":"…"} indirection into the top-level cache when present.
func (r apolloResolver) resolve(raw json.RawMessage) map[string]json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil
	}
	if refRaw, ok := obj["__ref"]; ok {
		var ref string
		if err := json.Unmarshal(refRaw, &ref); err == nil {
			if target, ok := r.cache[ref]; ok {
				var resolved map[string]json.RawMessage
				if err := json.Unmarshal(target, &resolved); err == nil {
					return resolved
				}
			}
		}
	}
	return obj
}

// field returns a resolved child object of node under key.
func (r apolloResolver) field(node map[string]json.RawMessage, key string) map[string]json.RawMessage {
	if node == nil {
		return nil
	}
	v, ok := node[key]
	if !ok {
		return nil
	}
	return r.resolve(v)
}

// findSearchResults locates the results array under
// ROOT_QUERY → searchQueries → search(<input>). Falls back to a recursive scan
// for any "results" array if the canonical path is not present.
func (r apolloResolver) findSearchResults() []json.RawMessage {
	if root := r.resolve(r.cache["ROOT_QUERY"]); root != nil {
		// searchQueries may be inline or a __ref; its search(<input>) child
		// holds the results.
		for k, v := range root {
			if !strings.HasPrefix(k, "searchQueries") {
				continue
			}
			sq := r.resolve(v)
			if sq == nil {
				continue
			}
			for sk, sv := range sq {
				if !strings.HasPrefix(sk, "search") {
					continue
				}
				if res := r.resultsField(sv); len(res) > 0 {
					return res
				}
			}
		}
		// Flattened shape: ROOT_QUERY may hold "searchQueries.search(...)".
		for k, v := range root {
			if strings.Contains(k, "search") {
				if res := r.resultsField(v); len(res) > 0 {
					return res
				}
			}
		}
	}

	// Last-resort: scan the whole cache for a results array.
	for _, v := range r.cache {
		if res := r.resultsField(v); len(res) > 0 {
			return res
		}
	}
	return nil
}

// resultsField returns the "results" array of a resolved node, if any.
func (r apolloResolver) resultsField(raw json.RawMessage) []json.RawMessage {
	node := r.resolve(raw)
	if node == nil {
		return nil
	}
	resRaw, ok := node["results"]
	if !ok {
		return nil
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(resRaw, &arr); err != nil {
		return nil
	}
	return arr
}

// bookingResultToHotel maps one resolved search result node to a HotelResult.
func bookingResultToHotel(r apolloResolver, result map[string]json.RawMessage, currency string) models.HotelResult {
	h := models.HotelResult{Currency: currency}

	bpd := r.field(result, "basicPropertyData")
	if bpd != nil {
		h.HotelID = jsonStr(bpd["id"])
	}

	// Display name.
	if dn := r.field(result, "displayName"); dn != nil {
		h.Name = jsonStr(dn["text"])
	}
	if h.Name == "" && bpd != nil {
		h.Name = jsonStr(bpd["name"])
	}

	if bpd != nil {
		// URL from pageName + country segment.
		pageName := jsonStr(bpd["pageName"])
		country := ""
		if loc := r.field(bpd, "location"); loc != nil {
			country = strings.ToLower(jsonStr(loc["countryCode"]))
			h.Lat = jsonFloat(loc["latitude"])
			h.Lon = jsonFloat(loc["longitude"])
			h.Address = jsonStr(loc["address"])
		}
		if pageName != "" {
			seg := country
			if seg == "" {
				seg = "de" // documented fallback country segment
			}
			h.BookingURL = bookingBaseURL + "/hotel/" + seg + "/" + pageName + ".html"
		}

		// Reviews and rating.
		if rv := r.field(bpd, "reviews"); rv != nil {
			h.Rating = jsonFloat(rv["totalScore"])
			h.ReviewCount = int(jsonFloat(rv["reviewsCount"]))
		}
		if sr := r.field(bpd, "starRating"); sr != nil {
			h.Stars = int(jsonFloat(sr["value"]))
		}

		// Photo.
		if ph := r.field(bpd, "photos"); ph != nil {
			if main := r.field(ph, "main"); main != nil {
				if hi := r.field(main, "highResUrl"); hi != nil {
					if rel := jsonStr(hi["relativeUrl"]); rel != "" {
						h.ImageURL = "https://cf.bstatic.com" + rel
					}
				}
			}
		}
	}

	// Price from blocks[0].finalPrice (total for stay).
	if blocksRaw, ok := result["blocks"]; ok {
		var blocks []json.RawMessage
		if err := json.Unmarshal(blocksRaw, &blocks); err == nil && len(blocks) > 0 {
			if block := r.resolve(blocks[0]); block != nil {
				if fp := r.field(block, "finalPrice"); fp != nil {
					if amt := jsonFloat(fp["amount"]); amt > 0 {
						h.Price = amt
					}
					if cur := jsonStr(fp["currency"]); cur != "" {
						h.Currency = cur
					}
				}
			}
		}
	}

	return h
}

// --- small JSON helpers ---

func jsonStr(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return ""
}

func jsonFloat(raw json.RawMessage) float64 {
	if len(raw) == 0 {
		return 0
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil {
		return f
	}
	// Numeric value encoded as a string.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if v, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
			return v
		}
	}
	return 0
}
