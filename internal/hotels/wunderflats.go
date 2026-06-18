package hotels

// Wunderflats mid-term / furnished-apartment provider.
//
// Wunderflats (https://wunderflats.com) lists furnished apartments for
// mid-term stays (typically weeks-to-months). It complements the hotel and
// short-term vacation-rental providers with monthly-rental inventory those
// sources miss.
//
// Unauthenticated feasibility (recon-verified 2026-06-18):
//   - No API key, headless browser, residential proxy, nor CAPTCHA bypass is
//     required. A plain HTTP client with a realistic desktop User-Agent and an
//     Accept-Language header suffices at low volume. Cloudflare / DataDome are
//     present on the edge but are not triggered by single low-rate requests.
//   - The public city listing page server-side-renders its state into a
//     <script id="data-hydrant"> JSON blob:
//       GET /en/furnished-apartments/{city}?page={N}  (page is 0-based, 30/page)
//   - Two on-the-wire shapes have been observed for that blob:
//       (a) pageData embedded directly in the outer object, and
//       (b) a `result` key whose value is a DOUBLE-ENCODED JSON string
//           (a JSON string that itself contains JSON-encoded JSON).
//     resolveWunderflatsPageData handles both: it reads pageData directly when
//     present, otherwise repeatedly JSON-decodes the `result` string until it
//     yields the object carrying pageData.
//   - From pageData.listingResults we read .items[] (30/page), .total,
//     .itemsPerPage, .page and .region.
//
// Per-item GOTCHAS (handled in parseWunderflatsListings):
//   - price is IN CENTS               -> divided by 100
//   - address.location.coordinates is GeoJSON [lng, lat] -> SWAPPED to lat,lon
//   - title is localized ({en,de})    -> prefer en, fall back to de
//   - the booking URL is constructed from _id; the slug in the canonical URL is
//     cosmetic (the /x/ form 301-redirects to it), so we emit the stable /x/ URL.
//
// LIMITATION: the minimum-stay / lease length is NOT present in the list
// payload (it lives only on the detail page), so per-result min-stay is
// deliberately omitted here.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"golang.org/x/time/rate"
)

// wunderflatsEnabled controls whether SearchWunderflats makes live HTTP
// requests. Disabled in the test suite (see testmain_test.go) so deterministic
// tests never fire real network calls; individual tests flip it on with a mock
// server injected via wunderflatsBaseURL.
var wunderflatsEnabled = true

// wunderflatsBaseURL is the root of the Wunderflats site. Overridable in tests
// so an httptest.Server can stand in for the live host.
var wunderflatsBaseURL = "https://wunderflats.com"

// wunderflatsUserAgent is a realistic desktop browser UA. Wunderflats serves
// the SSR data-hydrant blob to ordinary desktop clients.
const wunderflatsUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"

// wunderflatsAcceptLanguage is sent on every request; the localized titles in
// the payload (title.en / title.de) are returned regardless, but a realistic
// Accept-Language keeps the request looking like a browser.
const wunderflatsAcceptLanguage = "en-US,en;q=0.9"

// wunderflatsLimiter enforces a conservative request rate to stay well under
// the Cloudflare / DataDome edge thresholds.
var wunderflatsLimiter = rate.NewLimiter(rate.Every(500*time.Millisecond), 2)

// wunderflatsHTTPClient is a dedicated client with a sane timeout.
var wunderflatsHTTPClient = &http.Client{
	Timeout: 30 * time.Second,
}

// wunderflatsHydrantRe extracts the JSON body of the SSR <script
// id="data-hydrant"> tag. The attribute order may vary, so the id is matched
// loosely and the type attribute (if any) is tolerated.
var wunderflatsHydrantRe = regexp.MustCompile(
	`(?s)<script[^>]*\bid="data-hydrant"[^>]*>(.*?)</script>`)

// ---- Response types (only the fields we consume) ----

type wunderflatsHydrant struct {
	PageData *wunderflatsPageData `json:"pageData"`
	// Result, when present, is a (possibly multiply) JSON-encoded string that
	// decodes to an object carrying pageData. See resolveWunderflatsPageData.
	Result json.RawMessage `json:"result"`
}

type wunderflatsPageData struct {
	ListingResults wunderflatsListingResults `json:"listingResults"`
}

type wunderflatsListingResults struct {
	Region       wunderflatsRegion    `json:"region"`
	Total        int                  `json:"total"`
	Page         int                  `json:"page"`
	ItemsPerPage int                  `json:"itemsPerPage"`
	Items        []wunderflatsListing `json:"items"`
}

type wunderflatsRegion struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type wunderflatsListing struct {
	ID           string             `json:"_id"`
	Title        map[string]string  `json:"title"`
	Area         float64            `json:"area"`
	Price        int64              `json:"price"` // IN CENTS
	Currency     string             `json:"currency"`
	Rooms        float64            `json:"rooms"`
	Beds         int                `json:"beds"`
	Accommodates int                `json:"accommodates"`
	Address      wunderflatsAddress `json:"address"`
	Images       []wunderflatsImage `json:"images"`
}

type wunderflatsAddress struct {
	Street   string              `json:"street"`
	City     string              `json:"city"`
	Location wunderflatsGeoPoint `json:"location"`
}

type wunderflatsGeoPoint struct {
	Type        string    `json:"type"`
	Coordinates []float64 `json:"coordinates"` // GeoJSON [lng, lat]
}

type wunderflatsImage struct {
	Name string               `json:"name"`
	URLs wunderflatsImageURLs `json:"urls"`
}

type wunderflatsImageURLs struct {
	Original  string `json:"original"`
	Thumbnail string `json:"thumbnail"`
}

// SearchWunderflats searches Wunderflats for furnished mid-term apartments in a
// city. It is non-fatal by contract: callers treat any error as "zero results".
// Returns nil, nil when disabled (test mode).
func SearchWunderflats(ctx context.Context, location string, opts HotelSearchOptions) ([]models.HotelResult, error) {
	if !wunderflatsEnabled {
		return nil, nil
	}
	if strings.TrimSpace(location) == "" {
		return nil, fmt.Errorf("wunderflats: location is required")
	}

	slug := wunderflatsSlug(location)
	if slug == "" {
		return nil, fmt.Errorf("wunderflats: empty city slug for %q", location)
	}
	slog.Debug("wunderflats search", "location", location, "slug", slug)

	body, err := wunderflatsFetchCity(ctx, slug, 0)
	if err != nil {
		return nil, fmt.Errorf("wunderflats fetch: %w", err)
	}

	pd, err := extractWunderflatsPageData(body)
	if err != nil {
		return nil, fmt.Errorf("wunderflats extract: %w", err)
	}

	currency := strings.TrimSpace(opts.Currency)
	hotels := parseWunderflatsListings(pd.ListingResults, currency)
	slog.Debug("wunderflats results", "location", location,
		"count", len(hotels), "total", pd.ListingResults.Total)
	return hotels, nil
}

// wunderflatsSlug converts a free-text city into the URL slug Wunderflats uses
// for its listing pages: lowercase, ASCII-only, spaces/commas/punctuation
// collapsed to single hyphens. e.g. "München" -> "munchen", "Berlin, DE" ->
// "berlin-de".
func wunderflatsSlug(location string) string {
	s := strings.ToLower(strings.TrimSpace(location))
	var b strings.Builder
	prevHyphen := false
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevHyphen = false
		case r == ' ' || r == ',' || r == '-' || r == '/' || r == '.':
			if !prevHyphen && b.Len() > 0 {
				b.WriteByte('-')
				prevHyphen = true
			}
		default:
			// drop other characters (accents, punctuation)
		}
	}
	return strings.Trim(b.String(), "-")
}

// wunderflatsFetchCity fetches the listing page HTML for a city slug and page
// (0-based). The returned body is the raw HTML containing the data-hydrant blob.
func wunderflatsFetchCity(ctx context.Context, slug string, page int) ([]byte, error) {
	url := fmt.Sprintf("%s/en/furnished-apartments/%s?page=%d", wunderflatsBaseURL, slug, page)
	return wunderflatsGet(ctx, url)
}

// wunderflatsGet performs a rate-limited GET with a realistic desktop UA and
// Accept-Language, returning the response body. Non-2xx responses are errors.
func wunderflatsGet(ctx context.Context, url string) ([]byte, error) {
	if err := wunderflatsLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", wunderflatsUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", wunderflatsAcceptLanguage)
	resp, err := wunderflatsHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("unexpected status %d for %s", resp.StatusCode, url)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20)) // 16 MiB cap
	if err != nil {
		return nil, err
	}
	return body, nil
}

// extractWunderflatsPageData pulls the data-hydrant JSON out of an SSR HTML
// page and resolves it to the embedded pageData (handling the double-encoded
// `result` form).
func extractWunderflatsPageData(html []byte) (*wunderflatsPageData, error) {
	m := wunderflatsHydrantRe.FindSubmatch(html)
	if m == nil {
		return nil, fmt.Errorf("no data-hydrant script found")
	}
	return resolveWunderflatsPageData(m[1])
}

// resolveWunderflatsPageData decodes the data-hydrant JSON and returns the
// pageData object. It tolerates two shapes:
//   - pageData embedded directly in the outer object, and
//   - a `result` value that is a JSON string which itself JSON-decodes (one or
//     more times) into an object carrying pageData.
func resolveWunderflatsPageData(raw json.RawMessage) (*wunderflatsPageData, error) {
	var h wunderflatsHydrant
	if err := json.Unmarshal(raw, &h); err != nil {
		return nil, fmt.Errorf("decode hydrant: %w", err)
	}
	if h.PageData != nil {
		return h.PageData, nil
	}
	if len(h.Result) > 0 {
		decoded, err := decodeNestedJSON(h.Result)
		if err != nil {
			return nil, fmt.Errorf("decode result: %w", err)
		}
		var inner wunderflatsHydrant
		if err := json.Unmarshal(decoded, &inner); err != nil {
			return nil, fmt.Errorf("decode result payload: %w", err)
		}
		if inner.PageData != nil {
			return inner.PageData, nil
		}
	}
	return nil, fmt.Errorf("no pageData in hydrant")
}

// decodeNestedJSON unwraps a value that may be a JSON-encoded string wrapping
// further JSON (the "double-encoded" case), returning the innermost JSON bytes.
// It peels string layers until the value is no longer a JSON string, capping
// the depth to avoid pathological input.
func decodeNestedJSON(raw json.RawMessage) (json.RawMessage, error) {
	cur := json.RawMessage(strings.TrimSpace(string(raw)))
	for i := 0; i < 5; i++ {
		if len(cur) == 0 || cur[0] != '"' {
			return cur, nil // not a JSON string layer; this is the payload
		}
		var s string
		if err := json.Unmarshal(cur, &s); err != nil {
			return nil, err
		}
		cur = json.RawMessage(strings.TrimSpace(s))
	}
	return cur, nil
}

// parseWunderflatsListings maps a listingResults payload to HotelResults.
// fallbackCurrency is used when a listing carries no currency (defaults to
// EUR, Wunderflats' default market currency).
func parseWunderflatsListings(lr wunderflatsListingResults, fallbackCurrency string) []models.HotelResult {
	results := make([]models.HotelResult, 0, len(lr.Items))
	for _, it := range lr.Items {
		if strings.TrimSpace(it.ID) == "" {
			continue
		}
		// GOTCHA: price is in cents.
		price := float64(it.Price) / 100.0
		if price <= 0 {
			continue
		}
		currency := strings.ToUpper(strings.TrimSpace(it.Currency))
		if currency == "" {
			if fallbackCurrency != "" {
				currency = strings.ToUpper(fallbackCurrency)
			} else {
				currency = "EUR"
			}
		}

		h := models.HotelResult{
			Name:         wunderflatsTitle(it.Title),
			HotelID:      it.ID,
			Price:        price,
			Currency:     currency,
			BookingURL:   wunderflatsListingURL(it.ID),
			ImageURL:     wunderflatsImageURL(it.Images),
			Address:      wunderflatsAddressLine(it.Address),
			Neighborhood: strings.TrimSpace(it.Address.City),
			PropertyType: "apartment",
		}
		// GOTCHA: coordinates are GeoJSON [lng, lat] -> swap to lat, lon.
		if len(it.Address.Location.Coordinates) == 2 {
			h.Lon = it.Address.Location.Coordinates[0]
			h.Lat = it.Address.Location.Coordinates[1]
		}
		if h.Name == "" {
			h.Name = "Wunderflats furnished apartment"
		}
		h.Sources = []models.PriceSource{{
			Provider:   "wunderflats",
			Price:      price,
			Currency:   currency,
			BookingURL: h.BookingURL,
		}}
		results = append(results, h)
	}
	return results
}

// wunderflatsTitle prefers the English localized title, falling back to German
// then any available locale.
func wunderflatsTitle(title map[string]string) string {
	if title == nil {
		return ""
	}
	for _, k := range []string{"en", "de"} {
		if v := strings.TrimSpace(title[k]); v != "" {
			return v
		}
	}
	for _, v := range title {
		if t := strings.TrimSpace(v); t != "" {
			return t
		}
	}
	return ""
}

// wunderflatsListingURL builds the stable listing URL from the listing _id.
// The /x/ form 301-redirects to the canonical slugged URL, so the slug is
// cosmetic and we emit the durable id-only link.
func wunderflatsListingURL(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	return wunderflatsBaseURL + "/en/furnished-apartment/x/" + id
}

// wunderflatsImageURL returns the first listing image's original URL.
func wunderflatsImageURL(images []wunderflatsImage) string {
	for _, img := range images {
		if u := strings.TrimSpace(img.URLs.Original); u != "" {
			return u
		}
	}
	return ""
}

// wunderflatsAddressLine builds a human-readable address from the street and
// city fields. Either may be empty.
func wunderflatsAddressLine(a wunderflatsAddress) string {
	street := strings.TrimSpace(a.Street)
	city := strings.TrimSpace(a.City)
	switch {
	case street != "" && city != "":
		return street + ", " + city
	case street != "":
		return street
	default:
		return city
	}
}
