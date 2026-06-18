package hotels

// HousingAnywhere mid-term / medium-term rental provider.
//
// HousingAnywhere (https://housinganywhere.com) is a large mid-term rental
// marketplace (furnished rooms, studios, and apartments let by the month rather
// than the night). These lettings are invisible to nightly hotel/OTA providers,
// so they fill a real gap for relocations, student stays, and remote-work trips.
//
// Unauthenticated feasibility (recon-verified):
//   - Listing results are served by Algolia, NOT api.housinganywhere.com. The
//     public site is a thin client over a search-only Algolia index.
//   - Auth is a STATIC PUBLIC search-only Algolia app-id + search key. We do not
//     hardcode a key in source: keys rotate, and a literal would silently rot.
//     Instead we RUNTIME-HARVEST the pair from a public search page's inline SSR
//     config (regex for x-algolia-application-id / x-algolia-api-key or inline
//     appId/apiKey), cache it (TTL ~24h), and self-heal on rotation.
//   - The app-id is also the Algolia DSN subdomain (Y8L112MIBF), so even if the
//     harvest regex misses the id we fall back to the known DSN subdomain; only
//     the search key is strictly harvest-only.
//
// Two-step flow:
//  1. harvestAlgoliaCreds() -> {appID, apiKey} (from a public search page; cached)
//  2. queryAlgolia(creds, params) -> hits JSON -> []models.HotelResult
//
// Contract:
//   POST https://{appid}-dsn.algolia.net/1/indexes/*/queries
//   (fallback host y8l112mibf-3.algolianet.com)
//   Headers: X-Algolia-Application-Id, X-Algolia-API-Key
//   Body: {"requests":[{"indexName":"production_listings_rank_withOrpheus",
//          "params":"query=&hitsPerPage=20&page=0&facetFilters=[[\"city:Berlin\"]]
//                    &numericFilters=[\"priceEUR>=N\",\"priceEUR<=M\"]"}]}
//
// Index variants: rank_withOrpheus (default/relevance), price_low_to_high,
// price_high_to_low, most_recent.

import (
	"bytes"
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
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"golang.org/x/time/rate"
)

// housinganywhereEnabled controls whether SearchHousingAnywhere makes live HTTP
// requests. Disabled in the test suite (see testmain_test.go) so deterministic
// tests never fire real network calls; individual tests flip it on with mock
// servers injected via the *URL overrides below.
var housinganywhereEnabled = true

// haKnownAppID is the last-known Algolia application id. It doubles as the DSN
// subdomain. This is NOT a secret (the app-id is public and visible in every
// browser request); the search KEY is what we harvest at runtime. Kept as a
// fallback so a regex miss on the id alone does not break the provider.
const haKnownAppID = "Y8L112MIBF"

// Overridable endpoints (tests point these at httptest servers).
var (
	// haSearchPageURL is a public HousingAnywhere search page used to harvest
	// the Algolia credentials from its inline SSR config.
	haSearchPageURL = "https://housanywhere.invalid/placeholder" // set in init
	// haAlgoliaHostTmpl builds the Algolia POST host from the app-id. %s is the
	// lowercased app-id. Overridable in tests.
	haAlgoliaHostTmpl = "https://%s-dsn.algolia.net"
	// haGeonamesURL is the HousingAnywhere city geocode helper base.
	haGeonamesURL = "https://housinganywhere.com/api/v2/geonames/cities"
	// haListingBase is the absolute base for listing paths.
	haListingBase = "https://housinganywhere.com"
)

func init() {
	// Real search page used for credential harvest. Kept out of a const so it is
	// trivially overridable in tests and never accidentally hit when disabled.
	haSearchPageURL = haListingBase + "/s/Berlin--Germany"
}

// haUserAgent is a desktop browser UA. The SSR search page and Algolia both
// serve ordinary desktop clients without special headers.
const haUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

// haLimiter enforces a conservative per-provider request rate (mirrors the
// wizzair limiter). Up to two requests per search (harvest + query); the harvest
// is cached so steady-state is one request per search.
var haLimiter = rate.NewLimiter(rate.Every(500*time.Millisecond), 2)

var haHTTPClient = &http.Client{Timeout: 30 * time.Second}

// Credential-harvest regexes. Order is tried until a non-empty match is found.
// They tolerate single/double quotes and arbitrary whitespace, matching either
// HTTP-header-style keys (x-algolia-*) or inline JS config (appId/apiKey).
var (
	haAppIDRes = []*regexp.Regexp{
		regexp.MustCompile(`(?i)x-algolia-application-id["'\s:]+["']?([A-Z0-9]{8,16})`),
		regexp.MustCompile(`(?i)\bapp(?:lication)?[_-]?id["'\s:]+["']([A-Z0-9]{8,16})["']`),
		regexp.MustCompile(`(?i)algolia[^}]{0,80}?\bappId["'\s:]+["']([A-Z0-9]{8,16})["']`),
	}
	haAPIKeyRes = []*regexp.Regexp{
		regexp.MustCompile(`(?i)x-algolia-api-key["'\s:]+["']?([a-f0-9]{20,64})`),
		regexp.MustCompile(`(?i)\bapi[_-]?key["'\s:]+["']([a-f0-9]{20,64})["']`),
		regexp.MustCompile(`(?i)\bsearch[_-]?key["'\s:]+["']([a-f0-9]{20,64})["']`),
		regexp.MustCompile(`(?i)algolia[^}]{0,120}?\bapiKey["'\s:]+["']([a-f0-9]{20,64})["']`),
	}
)

// haCreds is a harvested Algolia credential pair.
type haCreds struct {
	appID  string
	apiKey string
}

// haCredCache memoises the harvested pair for ~24h so a rotation self-heals on
// the next expiry without per-search page fetches.
var haCredCache = struct {
	sync.Mutex
	creds     haCreds
	expiresAt time.Time
}{}

// haCredTTL is how long a harvested credential pair is cached.
const haCredTTL = 24 * time.Hour

// haNow is overridable in tests for deterministic TTL assertions.
var haNow = time.Now

// harvestAlgoliaCreds returns the Algolia {appID, apiKey} pair, fetching and
// caching it from a public search page when the cache is cold or expired. The
// app-id falls back to the known DSN subdomain when only the key is found; the
// key is harvest-only (no literal fallback) so a rotated key self-heals.
func harvestAlgoliaCreds(ctx context.Context) (haCreds, error) {
	haCredCache.Lock()
	if haCredCache.creds.apiKey != "" && haNow().Before(haCredCache.expiresAt) {
		c := haCredCache.creds
		haCredCache.Unlock()
		return c, nil
	}
	haCredCache.Unlock()

	body, err := haGet(ctx, haSearchPageURL, "text/html,application/xhtml+xml")
	if err != nil {
		return haCreds{}, fmt.Errorf("harvest fetch: %w", err)
	}
	creds, err := parseAlgoliaCreds(body)
	if err != nil {
		return haCreds{}, err
	}

	haCredCache.Lock()
	haCredCache.creds = creds
	haCredCache.expiresAt = haNow().Add(haCredTTL)
	haCredCache.Unlock()
	return creds, nil
}

// parseAlgoliaCreds extracts the Algolia credential pair from a search page
// body. The api key is required (harvest-only); the app-id falls back to the
// known DSN subdomain when the page does not expose it explicitly.
func parseAlgoliaCreds(body []byte) (haCreds, error) {
	apiKey := firstSubmatch(body, haAPIKeyRes)
	if apiKey == "" {
		return haCreds{}, fmt.Errorf("housinganywhere: no algolia api key in search page")
	}
	appID := firstSubmatch(body, haAppIDRes)
	if appID == "" {
		appID = haKnownAppID
	}
	return haCreds{appID: appID, apiKey: apiKey}, nil
}

// firstSubmatch returns the first capture group matched by any regex in res.
func firstSubmatch(body []byte, res []*regexp.Regexp) string {
	for _, re := range res {
		if m := re.FindSubmatch(body); m != nil {
			return string(m[1])
		}
	}
	return ""
}

// haInvalidateCreds clears the cached pair so the next search re-harvests. Used
// when Algolia rejects the harvested key (rotation between harvest and use).
func haInvalidateCreds() {
	haCredCache.Lock()
	haCredCache.creds = haCreds{}
	haCredCache.expiresAt = time.Time{}
	haCredCache.Unlock()
}

// ---- Algolia request / response ----

type algoliaRequest struct {
	Requests []algoliaQuery `json:"requests"`
}

type algoliaQuery struct {
	IndexName string `json:"indexName"`
	Params    string `json:"params"`
}

type algoliaResponse struct {
	Results []algoliaResult `json:"results"`
}

type algoliaResult struct {
	Hits   []algoliaHit `json:"hits"`
	NbHits int          `json:"nbHits"`
}

type algoliaGeoloc struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

type algoliaHit struct {
	ObjectID          string        `json:"objectID"`
	PriceEUR          float64       `json:"priceEUR"`
	Currency          string        `json:"currency"`
	MinimumStayMonths int           `json:"minimumStayMonths"`
	MaximumStay       int           `json:"maximumStay"`
	Geoloc            algoliaGeoloc `json:"_geoloc"`
	City              string        `json:"city"`
	Country           string        `json:"country"`
	PropertyType      string        `json:"propertyType"`
	PropertySize      float64       `json:"propertySize"`
	Path              string        `json:"path"`
	DateFrom          string        `json:"dateFrom"`
}

// haIndexForSort maps a trvl sort option to the matching Algolia index variant.
func haIndexForSort(sort string) string {
	switch strings.ToLower(strings.TrimSpace(sort)) {
	case "cheapest", "price", "price_low_to_high":
		return "production_listings_price_low_to_high"
	case "price_high_to_low", "expensive":
		return "production_listings_price_high_to_low"
	case "newest", "recent", "most_recent":
		return "production_listings_most_recent"
	default:
		return "production_listings_rank_withOrpheus"
	}
}

// haCityFromLocation extracts the bare city token from a free-text location for
// the Algolia city facet, e.g. "Berlin, Germany" -> "Berlin", "  Paris " ->
// "Paris".
func haCityFromLocation(location string) string {
	city := location
	if i := strings.IndexAny(location, ",-"); i >= 0 {
		city = location[:i]
	}
	return strings.TrimSpace(city)
}

// buildAlgoliaParams assembles the URL-encoded Algolia params string: empty
// query, hitsPerPage/page, a city facet filter, and optional priceEUR numeric
// bounds. priceEUR is whole EUR (not cents), so min/max map straight through.
func buildAlgoliaParams(city string, hitsPerPage, page int, minPrice, maxPrice float64) string {
	v := url.Values{}
	v.Set("query", "")
	v.Set("hitsPerPage", strconv.Itoa(hitsPerPage))
	v.Set("page", strconv.Itoa(page))
	if city != "" {
		v.Set("facetFilters", fmt.Sprintf(`[["city:%s"]]`, city))
	}
	var nums []string
	if minPrice > 0 {
		nums = append(nums, fmt.Sprintf("priceEUR>=%d", int(minPrice)))
	}
	if maxPrice > 0 {
		nums = append(nums, fmt.Sprintf("priceEUR<=%d", int(maxPrice)))
	}
	if len(nums) > 0 {
		// Build the JSON array literally rather than via json.Marshal, which
		// HTML-escapes '>' and '<' to >/<. Algolia accepts both, but
		// the literal form matches the documented contract and is human-legible.
		quoted := make([]string, len(nums))
		for i, n := range nums {
			quoted[i] = `"` + n + `"`
		}
		v.Set("numericFilters", "["+strings.Join(quoted, ",")+"]")
	}
	return v.Encode()
}

// haAlgoliaURL builds the Algolia multi-query endpoint URL for an app-id.
func haAlgoliaURL(appID string) string {
	return fmt.Sprintf(haAlgoliaHostTmpl, strings.ToLower(appID)) + "/1/indexes/*/queries"
}

// queryAlgolia POSTs a single index query and returns the decoded result. A 401/
// 403 (rotated/invalid key) invalidates the cached creds so the caller can
// re-harvest and retry once.
func queryAlgolia(ctx context.Context, creds haCreds, index, params string) (*algoliaResult, error) {
	reqBody := algoliaRequest{Requests: []algoliaQuery{{IndexName: index, Params: params}}}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	if err := haLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, haAlgoliaURL(creds.appID), bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", haUserAgent)
	req.Header.Set("X-Algolia-Application-Id", creds.appID)
	req.Header.Set("X-Algolia-API-Key", creds.apiKey)

	resp, err := haHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		haInvalidateCreds()
		return nil, fmt.Errorf("housinganywhere: algolia rejected key (status %d)", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("housinganywhere: algolia status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	var parsed algoliaResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("housinganywhere: decode: %w", err)
	}
	if len(parsed.Results) == 0 {
		return &algoliaResult{}, nil
	}
	return &parsed.Results[0], nil
}

// haGet performs a rate-limited GET and returns the body. Non-2xx is an error.
func haGet(ctx context.Context, rawurl, accept string) ([]byte, error) {
	if err := haLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawurl, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", haUserAgent)
	req.Header.Set("Accept", accept)
	resp, err := haHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("unexpected status %d for %s", resp.StatusCode, rawurl)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

// SearchHousingAnywhere searches HousingAnywhere's Algolia index for mid-term
// rentals near a location. It is non-fatal by contract: callers treat any error
// as "zero results". Returns nil, nil when disabled (test mode).
//
// Credentials are runtime-harvested and cached; on an auth rejection (rotated
// key) it invalidates the cache and retries once with a fresh harvest.
func SearchHousingAnywhere(ctx context.Context, location string, opts HotelSearchOptions) ([]models.HotelResult, error) {
	if !housinganywhereEnabled {
		return nil, nil
	}
	if strings.TrimSpace(location) == "" {
		return nil, fmt.Errorf("housinganywhere: location is required")
	}

	city := haCityFromLocation(location)
	index := haIndexForSort(opts.Sort)
	hitsPerPage := 20
	params := buildAlgoliaParams(city, hitsPerPage, 0, opts.MinPrice, opts.MaxPrice)

	res, err := haSearchOnce(ctx, index, params)
	if err != nil {
		// One retry on auth rejection: the cache was invalidated inside
		// queryAlgolia, so this re-harvests a fresh key.
		if strings.Contains(err.Error(), "rejected key") {
			slog.Debug("housinganywhere re-harvest after key rejection")
			res, err = haSearchOnce(ctx, index, params)
		}
		if err != nil {
			return nil, err
		}
	}

	currency := strings.TrimSpace(opts.Currency)
	results := make([]models.HotelResult, 0, len(res.Hits))
	for _, h := range res.Hits {
		if mapped, ok := mapHAHit(h, currency); ok {
			results = append(results, mapped)
		}
	}
	slog.Debug("housinganywhere results", "location", location, "count", len(results), "nbHits", res.NbHits)
	return results, nil
}

// haSearchOnce harvests creds (cached) and runs a single Algolia query.
func haSearchOnce(ctx context.Context, index, params string) (*algoliaResult, error) {
	creds, err := harvestAlgoliaCreds(ctx)
	if err != nil {
		return nil, fmt.Errorf("housinganywhere harvest: %w", err)
	}
	return queryAlgolia(ctx, creds, index, params)
}

// mapHAHit converts a single Algolia listing hit to a HotelResult. Returns
// ok=false for hits with no usable price (priceEUR is whole EUR, not cents).
func mapHAHit(h algoliaHit, fallbackCurrency string) (models.HotelResult, bool) {
	if h.PriceEUR <= 0 {
		return models.HotelResult{}, false
	}
	currency := strings.TrimSpace(h.Currency)
	if currency == "" {
		if fallbackCurrency != "" {
			currency = strings.ToUpper(fallbackCurrency)
		} else {
			currency = "EUR"
		}
	}

	name := haDisplayName(h)
	out := models.HotelResult{
		Name:         name,
		HotelID:      h.ObjectID,
		Price:        h.PriceEUR,
		Currency:     currency,
		Lat:          h.Geoloc.Lat,
		Lon:          h.Geoloc.Lng,
		Address:      haAddress(h),
		PropertyType: strings.ToLower(strings.TrimSpace(h.PropertyType)),
		BookingURL:   haListingURL(h.Path),
		Description:  haDescription(h),
	}
	out.Sources = []models.PriceSource{{
		Provider:   "housinganywhere",
		Price:      h.PriceEUR,
		Currency:   currency,
		BookingURL: out.BookingURL,
		PriceBasis: "room_total", // monthly mid-term rent, not a nightly rate
	}}
	return out, true
}

// haDisplayName builds a human label from property type + size + city, since
// HousingAnywhere listings have no free-text title in the Algolia hit.
func haDisplayName(h algoliaHit) string {
	parts := make([]string, 0, 3)
	if h.PropertySize > 0 {
		parts = append(parts, fmt.Sprintf("%.0f m²", h.PropertySize))
	}
	if pt := strings.TrimSpace(h.PropertyType); pt != "" {
		parts = append(parts, pt)
	}
	if c := strings.TrimSpace(h.City); c != "" {
		parts = append(parts, "in "+c)
	}
	if len(parts) == 0 {
		return "HousingAnywhere rental"
	}
	return strings.Join(parts, " ")
}

// haAddress joins city and country for a compact address line.
func haAddress(h algoliaHit) string {
	parts := make([]string, 0, 2)
	if c := strings.TrimSpace(h.City); c != "" {
		parts = append(parts, c)
	}
	if c := strings.TrimSpace(h.Country); c != "" {
		parts = append(parts, c)
	}
	return strings.Join(parts, ", ")
}

// haDescription summarises the lease window (min/max months) and availability
// date — the mid-term traits that distinguish these from nightly stays.
func haDescription(h algoliaHit) string {
	var b strings.Builder
	b.WriteString("Mid-term rental")
	switch {
	case h.MinimumStayMonths > 0 && h.MaximumStay > 0:
		fmt.Fprintf(&b, " · %d–%d months", h.MinimumStayMonths, h.MaximumStay)
	case h.MinimumStayMonths > 0:
		fmt.Fprintf(&b, " · min %d months", h.MinimumStayMonths)
	case h.MaximumStay > 0:
		fmt.Fprintf(&b, " · max %d months", h.MaximumStay)
	}
	if d := strings.TrimSpace(h.DateFrom); d != "" {
		fmt.Fprintf(&b, " · available from %s", d)
	}
	return b.String()
}

// haListingURL makes a listing path absolute against the HousingAnywhere host.
func haListingURL(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return haListingBase + path
}

// ---- City geocode helper ----

type haGeoname struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

type haGeonamesResponse struct {
	Cities []haGeoname `json:"cities"`
}

// ResolveHousingAnywhereCity resolves a "City--Country" slug to coordinates via
// HousingAnywhere's public geonames helper. Best-effort: callers fall back to
// the standard Nominatim geocoder on error. Returns the first city match.
func ResolveHousingAnywhereCity(ctx context.Context, city, country string) (lat, lon float64, err error) {
	q := strings.TrimSpace(city)
	if country != "" {
		q = q + "--" + strings.TrimSpace(country)
	}
	if q == "" {
		return 0, 0, fmt.Errorf("housinganywhere geonames: empty query")
	}
	u, err := url.Parse(haGeonamesURL)
	if err != nil {
		return 0, 0, err
	}
	vals := u.Query()
	vals.Set("query", q)
	u.RawQuery = vals.Encode()

	body, err := haGet(ctx, u.String(), "application/json")
	if err != nil {
		return 0, 0, err
	}
	var parsed haGeonamesResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		// The endpoint may return a bare array; try that shape too.
		var arr []haGeoname
		if err2 := json.Unmarshal(body, &arr); err2 == nil {
			parsed.Cities = arr
		} else {
			return 0, 0, fmt.Errorf("housinganywhere geonames decode: %w", err)
		}
	}
	if len(parsed.Cities) == 0 {
		return 0, 0, fmt.Errorf("housinganywhere geonames: no match for %q", q)
	}
	return parsed.Cities[0].Lat, parsed.Cities[0].Lng, nil
}
