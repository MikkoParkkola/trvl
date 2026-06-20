package hotels

// Anyplace mid-term / nomad furnished-apartment provider.
//
// Anyplace (https://www.anyplace.com) lists furnished apartments for stays of a
// month or more — the digital-nomad / relocation segment that traditional
// nightly-hotel providers (Google Hotels, Trivago, Booking) miss entirely. Its
// inventory is monthly-priced furnished rentals with a minimum stay (typically
// 30 nights), which makes it high-value for long-trip and relocation planning.
//
// Unauthenticated feasibility (recon-verified, Tier-A):
//   - No API key, headless browser, residential proxy, nor CAPTCHA bypass is
//     required. Plain HTTP with a desktop User-Agent suffices.
//   - The site is a Next.js app. Every page embeds a <script id="__NEXT_DATA__">
//     JSON blob carrying the deploy's "buildId". The structured listing data is
//     served from the Next.js data endpoint:
//       GET /_next/data/<buildId>/listings/{city}.json  ->  clean JSON
//     which returns the same pageProps the SSR HTML hydrates from (urql/GraphQL
//     underneath, but the _next/data JSON is already flattened and key-stable).
//   - City catalog is enumerable from the public sitemaps
//     (/sitemap-cities.xml + /sitemap-listings.xml); the search interface here
//     derives the city slug directly from the query, so sitemap crawling is not
//     needed on the hot path. See enumeration note below.
//
// BUILDID ROTATION (verified gotcha, mirrors internal/flights/wizzair.go's API
// version rotation): the <buildId> path segment changes on every Anyplace
// deploy. It is NOT hardcoded. It is resolved dynamically at request time from
// the __NEXT_DATA__ blob on a live page. Resolution order (first hit wins):
//
//  1. ANYPLACE_BUILD_ID env var (operator override, zero-deploy fix if the
//     auto-resolve path ever breaks).
//  2. Dynamic resolve: GET the city page HTML, parse __NEXT_DATA__, read buildId.
//
// There is deliberately NO last-known-good constant: a stale buildId 404s the
// data endpoint, so a hardcoded fallback would be worse than resolving fresh.
//
// Two-step flow, mirroring HomeToGo/Trivago's resolve-then-fetch pattern:
//  1. resolveAnyplaceBuildID(ctx, citySlug) -> buildId (from __NEXT_DATA__)
//  2. fetchAnyplaceCity(ctx, buildId, citySlug) -> listings JSON -> []HotelResult
//
// Field mapping (recon-confirmed Anyplace listing fields -> models.HotelResult):
//   id            -> HotelID
//   title         -> Name
//   price         -> Price            (headline, e.g. 4645)
//   monthlyPrice  -> Description note  (e.g. 4170 USD/mo) + PriceSource
//   currency      -> Currency         (e.g. USD)
//   minimumStay   -> Description note  (e.g. 30-night min)
//   bedroom       -> Description note  (e.g. 1 bed)
//   bathroom      -> Description note  (e.g. 1 bath)
//   lat / long    -> Lat / Lon
//   propertyType  -> PropertyType     ("furnished_apartment" -> "apartment")
//
// monthlyPrice, minimumStay and bedroom/bathroom have no dedicated HotelResult
// field, so they are folded into the user-visible Description string rather than
// dropped — they are the whole point of the mid-term segment.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"golang.org/x/time/rate"
)

// anyplaceEnabled controls whether SearchAnyplace makes live HTTP requests.
//
// DEPRECATED / OFF BY DEFAULT (2026-06-20): Anyplace restructured its site to
// client-side rendering. The former city routes (/listings/{city} and
// /_next/data/<buildId>/listings/{city}.json) now 308 / serve the homepage
// shell with empty pageProps — listings load via an opaque client-side API with
// no clean unauthenticated SSR or _next/data endpoint. Rather than ship a flaky
// scrape, this provider is disabled by default (honest skip, mirroring the
// easyJet AKAMAI_BLOCK precedent). The parser/resolver code is retained so a
// future endpoint can re-enable it; tests flip this on against fixtures/mocks.
// Opt back in by setting anyplaceEnabled=true once a stable data path returns.
var anyplaceEnabled = false

// anyplaceBaseURL is the root of the Anyplace site. Overridable in tests so an
// httptest.Server can stand in for the live host.
var anyplaceBaseURL = "https://www.anyplace.com"

// anyplaceUserAgent is a desktop browser UA. Anyplace serves the SSR HTML and
// _next/data JSON to ordinary desktop clients; no special headers are required.
const anyplaceUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

// anyplaceLimiter enforces a conservative request rate to avoid tripping the
// edge rate limits. Two requests per search (resolve buildId + fetch JSON).
// Mirrors the per-provider rate.Limiter convention in internal/flights/wizzair.go.
var anyplaceLimiter = rate.NewLimiter(rate.Every(500*time.Millisecond), 2)

// anyplaceHTTPClient is a dedicated client with a sane timeout.
var anyplaceHTTPClient = &http.Client{
	Timeout: 30 * time.Second,
}

// anyplaceNextDataRe extracts the __NEXT_DATA__ JSON blob from a Next.js page.
// The script tag is emitted verbatim by Next.js on every page; the capture group
// is the JSON object that carries buildId.
var anyplaceNextDataRe = regexp.MustCompile(
	`(?s)<script id="__NEXT_DATA__" type="application/json">(.*?)</script>`)

// ---- Response types (only the fields we consume) ----

// anyplaceNextData is the shape of the __NEXT_DATA__ blob; we read only buildId.
type anyplaceNextData struct {
	BuildID string `json:"buildId"`
}

// anyplaceDataResponse is the _next/data JSON envelope: Next.js wraps
// getStaticProps/getServerSideProps output under "pageProps".
type anyplaceDataResponse struct {
	PageProps anyplacePageProps `json:"pageProps"`
}

type anyplacePageProps struct {
	Listings []anyplaceListing `json:"listings"`
}

// anyplaceListing is a single furnished-apartment listing.
type anyplaceListing struct {
	ID           string  `json:"id"`
	Slug         string  `json:"slug"`
	Title        string  `json:"title"`
	Price        float64 `json:"price"`
	MonthlyPrice float64 `json:"monthlyPrice"`
	Currency     string  `json:"currency"`
	MinimumStay  int     `json:"minimumStay"`
	Bedroom      int     `json:"bedroom"`
	Bathroom     int     `json:"bathroom"`
	PropertyType string  `json:"propertyType"`
	Lat          float64 `json:"lat"`
	Long         float64 `json:"long"`
	Photo        string  `json:"photo"`
	Address      string  `json:"address"`
}

// SearchAnyplace searches Anyplace for mid-term furnished apartments near a
// location. It is non-fatal by contract: callers treat any error as "zero
// results". Returns nil, nil when disabled (test mode).
func SearchAnyplace(ctx context.Context, location string, opts HotelSearchOptions) ([]models.HotelResult, error) {
	if !anyplaceEnabled {
		return nil, nil
	}
	if strings.TrimSpace(location) == "" {
		return nil, fmt.Errorf("anyplace: location is required")
	}

	citySlug := anyplaceCitySlug(location)

	// Step 1: resolve the deploy's buildId (env override, else dynamic from the
	// city page's __NEXT_DATA__ blob).
	slog.Debug("anyplace resolve buildId", "location", location, "slug", citySlug)
	buildID, err := resolveAnyplaceBuildID(ctx, citySlug)
	if err != nil {
		return nil, fmt.Errorf("anyplace resolve buildId: %w", err)
	}

	// Step 2: fetch the _next/data listings JSON for that city.
	slog.Debug("anyplace fetch city", "location", location, "buildId", buildID)
	raw, err := fetchAnyplaceCity(ctx, buildID, citySlug)
	if err != nil {
		return nil, fmt.Errorf("anyplace fetch city: %w", err)
	}

	currency := strings.TrimSpace(opts.Currency)
	hotels, err := parseAnyplaceListings(raw, currency)
	if err != nil {
		return nil, fmt.Errorf("anyplace parse: %w", err)
	}
	slog.Debug("anyplace results", "location", location, "count", len(hotels))
	return hotels, nil
}

// anyplaceCitySlug converts a free-text location into the URL slug Anyplace uses
// for its city listing pages: lowercase, spaces/commas collapsed to single
// hyphens, ASCII-only path-safe. e.g. "Lisbon, Portugal" -> "lisbon-portugal".
func anyplaceCitySlug(location string) string {
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

// resolveAnyplaceBuildID returns the Next.js buildId for the current deploy.
// The ANYPLACE_BUILD_ID env var takes precedence (operator zero-deploy override);
// otherwise it is resolved dynamically from the city page's __NEXT_DATA__ blob.
func resolveAnyplaceBuildID(ctx context.Context, citySlug string) (string, error) {
	if v := strings.TrimSpace(os.Getenv("ANYPLACE_BUILD_ID")); v != "" {
		return v, nil
	}
	if citySlug == "" {
		return "", fmt.Errorf("empty city slug")
	}
	body, err := anyplaceGet(ctx, anyplaceBaseURL+"/listings/"+citySlug, "text/html,application/xhtml+xml")
	if err != nil {
		return "", err
	}
	m := anyplaceNextDataRe.FindSubmatch(body)
	if m == nil {
		return "", fmt.Errorf("no __NEXT_DATA__ blob found for slug %q", citySlug)
	}
	var nd anyplaceNextData
	if err := json.Unmarshal(m[1], &nd); err != nil {
		return "", fmt.Errorf("decode __NEXT_DATA__: %w", err)
	}
	if strings.TrimSpace(nd.BuildID) == "" {
		return "", fmt.Errorf("empty buildId in __NEXT_DATA__ for slug %q", citySlug)
	}
	return nd.BuildID, nil
}

// fetchAnyplaceCity fetches the _next/data listings JSON for a city.
func fetchAnyplaceCity(ctx context.Context, buildID, citySlug string) (json.RawMessage, error) {
	if buildID == "" {
		return nil, fmt.Errorf("empty buildId")
	}
	if citySlug == "" {
		return nil, fmt.Errorf("empty city slug")
	}
	url := anyplaceBaseURL + "/_next/data/" + buildID + "/listings/" + citySlug + ".json"
	body, err := anyplaceGet(ctx, url, "application/json")
	if err != nil {
		return nil, err
	}
	return json.RawMessage(body), nil
}

// anyplaceGet performs a rate-limited GET with the desktop UA and returns the
// response body. Non-2xx responses are errors.
func anyplaceGet(ctx context.Context, url, accept string) ([]byte, error) {
	if err := anyplaceLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", anyplaceUserAgent)
	req.Header.Set("Accept", accept)
	resp, err := anyplaceHTTPClient.Do(req)
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

// parseAnyplaceListings maps an Anyplace _next/data listings payload to
// HotelResults. Listings without a usable headline price are skipped so every
// returned result is actually comparable. fallbackCurrency is used when a
// listing carries no currency token; when empty it defaults to USD (Anyplace's
// default display currency).
func parseAnyplaceListings(raw json.RawMessage, fallbackCurrency string) ([]models.HotelResult, error) {
	var resp anyplaceDataResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	results := make([]models.HotelResult, 0, len(resp.PageProps.Listings))
	for _, l := range resp.PageProps.Listings {
		if l.Price <= 0 {
			continue // no headline price -> not comparable, skip
		}
		currency := anyplaceCurrency(l.Currency, fallbackCurrency)

		h := models.HotelResult{
			Name:         strings.TrimSpace(l.Title),
			HotelID:      l.ID,
			Price:        l.Price,
			Currency:     currency,
			Address:      strings.TrimSpace(l.Address),
			ImageURL:     anyplaceAbsURL(l.Photo),
			BookingURL:   anyplaceListingURL(l.Slug),
			PropertyType: anyplacePropertyType(l.PropertyType),
			Lat:          l.Lat,
			Lon:          l.Long,
			Description:  anyplaceDescription(l),
		}
		if h.Name == "" {
			h.Name = "Anyplace furnished apartment"
		}
		h.Sources = []models.PriceSource{{
			Provider:   "anyplace",
			Price:      l.Price,
			Currency:   currency,
			BookingURL: h.BookingURL,
		}}
		results = append(results, h)
	}
	return results, nil
}

// anyplaceDescription folds the mid-term-specific fields (monthly price, minimum
// stay, bed/bath counts) — which have no dedicated HotelResult field — into a
// compact, user-visible summary. e.g.
// "Furnished apartment · 1 bed · 1 bath · 30-night min · 4170 USD/mo".
func anyplaceDescription(l anyplaceListing) string {
	currency := strings.ToUpper(strings.TrimSpace(l.Currency))
	if currency == "" {
		currency = "USD"
	}
	parts := make([]string, 0, 5)
	parts = append(parts, "Furnished apartment")
	if l.Bedroom > 0 {
		parts = append(parts, fmt.Sprintf("%d bed", l.Bedroom))
	}
	if l.Bathroom > 0 {
		parts = append(parts, fmt.Sprintf("%d bath", l.Bathroom))
	}
	if l.MinimumStay > 0 {
		parts = append(parts, fmt.Sprintf("%d-night min", l.MinimumStay))
	}
	if l.MonthlyPrice > 0 {
		parts = append(parts, fmt.Sprintf("%.0f %s/mo", l.MonthlyPrice, currency))
	}
	return strings.Join(parts, " · ")
}

// anyplaceCurrency normalises a listing currency to an ISO 4217 code, falling
// back to the provided default, then USD (Anyplace's default display currency).
func anyplaceCurrency(currency, fallback string) string {
	if c := strings.ToUpper(strings.TrimSpace(currency)); c != "" {
		return c
	}
	if fallback != "" {
		return strings.ToUpper(strings.TrimSpace(fallback))
	}
	return "USD"
}

// anyplacePropertyType normalises Anyplace's propertyType to the HotelResult
// vocabulary (hotel|hostel|apartment|vacation_rental|...). Anyplace's inventory
// is "furnished_apartment", which maps to "apartment".
func anyplacePropertyType(pt string) string {
	switch strings.ToLower(strings.TrimSpace(pt)) {
	case "", "furnished_apartment", "apartment":
		return "apartment"
	default:
		return strings.ToLower(strings.TrimSpace(pt))
	}
}

// anyplaceListingURL builds the public listing URL from a slug.
func anyplaceListingURL(slug string) string {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return ""
	}
	return anyplaceBaseURL + "/listing/" + slug
}

// anyplaceAbsURL turns a relative Anyplace link into an absolute URL.
func anyplaceAbsURL(link string) string {
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
	return anyplaceBaseURL + link
}
