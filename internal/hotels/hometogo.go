package hotels

// HomeToGo vacation-rental aggregator provider.
//
// HomeToGo (https://www.hometogo.com) aggregates vacation rentals from Airbnb,
// Vrbo, Booking.com and local hosts -- high value for family apartments and
// whole-home stays that traditional hotel providers miss.
//
// Unauthenticated feasibility (verified 2026-05-25):
//   - No API key, headless browser, residential proxy, nor CAPTCHA bypass is
//     required. Plain HTTP with a desktop User-Agent suffices.
//   - The public search endpoint returns SSR JSON when ?_format=json is set:
//       GET /search/{locationId}?_format=json  ->  {"offers": [...]}
//   - The endpoint requires a HomeToGo *location ID*; raw lat/lon and free-text
//     query parameters are ignored and silently fall back to a default region
//     (Florida, id 5460aeac2e5b2). The location ID is resolved from the public
//     SEO landing page for a city slug:
//       GET /{city-slug}/  ->  SSR HTML containing "location":"{hexId}"
//   - Only the first ~20 offers in the response are price-hydrated; the
//     remainder are lazy-loaded stubs without price image. We map only the
//     hydrated offers (those carrying a displayed price), so every returned
//     HotelResult is actually bookable.
//
// Two-step flow, mirroring Trivago/Hostelworld's resolve-then-search pattern:
//  1. resolveHomeToGoLocation(slug) -> location ID (from SEO page SSR HTML)
//  2. fetchHomeToGoOffers(id)        -> offer JSON -> []models.HotelResult

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"golang.org/x/time/rate"
)

// hometogoEnabled controls whether SearchHomeToGo makes live HTTP requests.
// Disabled in the test suite (see testmain_test.go) so deterministic tests
// never fire real network calls; individual tests flip it on with a mock
// server injected via hometogoBaseURL.
var hometogoEnabled = true

// hometogoBaseURL is the root of the HomeToGo site. Overridable in tests so an
// httptest.Server can stand in for the live host.
var hometogoBaseURL = "https://www.hometogo.com"

// hometogoUserAgent is a desktop browser UA. HomeToGo serves the SSR JSON/HTML
// to ordinary desktop clients; no special headers are required.
const hometogoUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

// hometogoLimiter enforces a conservative request rate to avoid tripping the
// edge (Cloudflare) rate limits. Two requests per search (resolve + fetch).
var hometogoLimiter = rate.NewLimiter(rate.Every(500*time.Millisecond), 2)

// hometogoHTTPClient is a dedicated client with a sane timeout.
var hometogoHTTPClient = &http.Client{
	Timeout: 30 * time.Second,
}

// hometogoLocationRe extracts the location ID from the SEO landing page's SSR
// state, e.g. `"location":"5460aed009800"`. The ID is a lowercase hex token.
var hometogoLocationRe = regexp.MustCompile(`"location"\s*:\s*"([0-9a-f]{10,16})"`)

// hometogoPriceRe extracts the numeric amount from a displayed price such as
// "94 €", "1,234 €", "$1,099".
var hometogoPriceRe = regexp.MustCompile(`([0-9][0-9.,]*)`)

// ---- Response types (only the fields we consume) ----

type hometogoSearchResponse struct {
	Offers []hometogoOffer `json:"offers"`
}

type hometogoOffer struct {
	ID                string             `json:"id"`
	Title             string             `json:"title"`
	GeoLocation       *hometogoGeo       `json:"geoLocation"`
	LowestPriceInfo   *hometogoPriceInfo `json:"lowestPriceInfo"`
	DeepLink          string             `json:"deepLink"`
	ImageLinks        *hometogoImages    `json:"imageLinks"`
	Amenities         *hometogoAmenities `json:"amenities"`
	LocationTrailHead string             `json:"locationTrailHeader"`
}

type hometogoGeo struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

type hometogoPriceInfo struct {
	Display string `json:"display"`
}

type hometogoImages struct {
	Large  string `json:"large"`
	Medium string `json:"medium"`
	Small  string `json:"small"`
}

type hometogoAmenities struct {
	Common []hometogoAmenity `json:"common"`
}

type hometogoAmenity struct {
	Label   string `json:"label"`
	Amenity string `json:"amenity"`
}

// SearchHomeToGo searches HomeToGo for vacation rentals near a location.
// It is non-fatal by contract: callers treat any error as "zero results".
// Returns nil, nil when disabled (test mode).
func SearchHomeToGo(ctx context.Context, location string, opts HotelSearchOptions) ([]models.HotelResult, error) {
	if !hometogoEnabled {
		return nil, nil
	}
	if strings.TrimSpace(location) == "" {
		return nil, fmt.Errorf("hometogo: location is required")
	}

	// Step 1: resolve the free-text location to a HomeToGo location ID via the
	// public SEO landing page.
	slug := hometogoSlug(location)
	slog.Debug("hometogo resolve", "location", location, "slug", slug)
	locID, err := resolveHomeToGoLocation(ctx, slug)
	if err != nil {
		return nil, fmt.Errorf("hometogo resolve: %w", err)
	}

	// Step 2: fetch the SSR JSON offers for that location.
	slog.Debug("hometogo search", "location", location, "id", locID)
	raw, err := fetchHomeToGoOffers(ctx, locID)
	if err != nil {
		return nil, fmt.Errorf("hometogo offers: %w", err)
	}

	currency := strings.TrimSpace(opts.Currency)
	hotels, err := parseHomeToGoOffers(raw, currency)
	if err != nil {
		return nil, fmt.Errorf("hometogo parse: %w", err)
	}
	slog.Debug("hometogo results", "location", location, "count", len(hotels))
	return hotels, nil
}

// hometogoSlug converts a free-text location into the URL slug HomeToGo uses
// for its SEO landing pages: lowercase, spaces and commas collapsed to single
// hyphens, ASCII-only path-safe. e.g. "New York, USA" -> "new-york-usa".
func hometogoSlug(location string) string {
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

// resolveHomeToGoLocation fetches the SEO landing page for a slug and extracts
// the HomeToGo location ID embedded in the SSR state.
func resolveHomeToGoLocation(ctx context.Context, slug string) (string, error) {
	if slug == "" {
		return "", fmt.Errorf("empty slug")
	}
	body, err := hometogoGet(ctx, hometogoBaseURL+"/"+slug+"/", "text/html,application/xhtml+xml")
	if err != nil {
		return "", err
	}
	m := hometogoLocationRe.FindSubmatch(body)
	if m == nil {
		return "", fmt.Errorf("no location id found for slug %q", slug)
	}
	return string(m[1]), nil
}

// fetchHomeToGoOffers fetches the SSR JSON search payload for a location ID.
func fetchHomeToGoOffers(ctx context.Context, locID string) (json.RawMessage, error) {
	if locID == "" {
		return nil, fmt.Errorf("empty location id")
	}
	url := hometogoBaseURL + "/search/" + locID + "?_format=json"
	body, err := hometogoGet(ctx, url, "application/json")
	if err != nil {
		return nil, err
	}
	return json.RawMessage(body), nil
}

// hometogoGet performs a rate-limited GET with the desktop UA and returns the
// response body. Non-2xx responses are errors.
func hometogoGet(ctx context.Context, url, accept string) ([]byte, error) {
	if err := hometogoLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", hometogoUserAgent)
	req.Header.Set("Accept", accept)
	resp, err := hometogoHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("unexpected status %d for %s", resp.StatusCode, url)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20)) // 8 MiB cap
	if err != nil {
		return nil, err
	}
	return body, nil
}

// parseHomeToGoOffers maps a HomeToGo search JSON payload to HotelResults.
// Only price-hydrated offers are mapped (lazy-loaded stubs without a displayed
// price are skipped) so every returned result is actually comparable.
// fallbackCurrency is used when the displayed price carries no currency token;
// when empty it defaults to EUR (HomeToGo's default for the public endpoint).
func parseHomeToGoOffers(raw json.RawMessage, fallbackCurrency string) ([]models.HotelResult, error) {
	var resp hometogoSearchResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	results := make([]models.HotelResult, 0, len(resp.Offers))
	for _, o := range resp.Offers {
		if o.LowestPriceInfo == nil || strings.TrimSpace(o.LowestPriceInfo.Display) == "" {
			continue // lazy stub: no price -> not comparable, skip
		}
		price, currency := parseHomeToGoPrice(o.LowestPriceInfo.Display, fallbackCurrency)
		if price <= 0 {
			continue
		}

		h := models.HotelResult{
			Name:       strings.TrimSpace(o.Title),
			HotelID:    o.ID,
			Price:      price,
			Currency:   currency,
			BookingURL: hometogoAbsURL(o.DeepLink),
			ImageURL:   hometogoImageURL(o.ImageLinks),
			Address:    strings.TrimSpace(o.LocationTrailHead),
			Amenities:  hometogoAmenityLabels(o.Amenities),
		}
		if o.GeoLocation != nil {
			h.Lat = o.GeoLocation.Lat
			h.Lon = o.GeoLocation.Lon
		}
		if h.Name == "" {
			h.Name = "HomeToGo vacation rental"
		}
		h.Sources = []models.PriceSource{{
			Provider:   "hometogo",
			Price:      price,
			Currency:   currency,
			BookingURL: h.BookingURL,
		}}
		results = append(results, h)
	}
	return results, nil
}

// parseHomeToGoPrice parses a displayed price like "94 €", "1,234 €", "$1,099"
// into a numeric amount and an ISO-ish currency code. The amount uses ',' as a
// thousands separator (HomeToGo's display convention). When no currency token
// is present, fallback (then EUR) is used.
func parseHomeToGoPrice(display, fallback string) (float64, string) {
	display = strings.TrimSpace(display)
	currency := hometogoCurrency(display, fallback)
	m := hometogoPriceRe.FindString(display)
	if m == "" {
		return 0, currency
	}
	// Strip thousands separators; HomeToGo displays integers (no decimals).
	cleaned := strings.ReplaceAll(m, ",", "")
	cleaned = strings.ReplaceAll(cleaned, ".", "")
	amount, err := strconv.ParseFloat(cleaned, 64)
	if err != nil {
		return 0, currency
	}
	return amount, currency
}

// hometogoCurrency maps a currency symbol token in the display string to an
// ISO 4217 code. Falls back to the provided default, then EUR.
func hometogoCurrency(display, fallback string) string {
	switch {
	case strings.Contains(display, "€") || strings.Contains(strings.ToUpper(display), "EUR"):
		return "EUR"
	case strings.Contains(display, "$") || strings.Contains(strings.ToUpper(display), "USD"):
		return "USD"
	case strings.Contains(display, "£") || strings.Contains(strings.ToUpper(display), "GBP"):
		return "GBP"
	}
	if fallback != "" {
		return strings.ToUpper(fallback)
	}
	return "EUR"
}

// hometogoAbsURL turns a relative HomeToGo link into an absolute URL.
func hometogoAbsURL(link string) string {
	link = strings.TrimSpace(link)
	if link == "" {
		return ""
	}
	if strings.HasPrefix(link, "http://") || strings.HasPrefix(link, "https://") {
		return link
	}
	if strings.HasPrefix(link, "//") {
		return "https:" + link
	}
	if !strings.HasPrefix(link, "/") {
		link = "/" + link
	}
	return hometogoBaseURL + link
}

// hometogoImageURL picks the best available image and makes it absolute.
// HomeToGo CDN links are protocol-relative ("//cdn.hometogo.net/...").
func hometogoImageURL(img *hometogoImages) string {
	if img == nil {
		return ""
	}
	for _, c := range []string{img.Large, img.Medium, img.Small} {
		if strings.TrimSpace(c) != "" {
			return hometogoAbsURL(c)
		}
	}
	return ""
}

// hometogoAmenityLabels extracts the human-readable amenity labels.
func hometogoAmenityLabels(a *hometogoAmenities) []string {
	if a == nil || len(a.Common) == 0 {
		return nil
	}
	labels := make([]string, 0, len(a.Common))
	for _, am := range a.Common {
		if l := strings.TrimSpace(am.Label); l != "" {
			labels = append(labels, l)
		}
	}
	if len(labels) == 0 {
		return nil
	}
	return labels
}
