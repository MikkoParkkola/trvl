package hotels

// Landing (hellolanding.com) furnished mid-term apartment provider.
//
// Landing (https://www.hellolanding.com) is a US furnished-apartment platform
// for mid-term / month-to-month stays — fully-furnished units with flexible
// leases that traditional hotel and short-let providers do not surface. This
// fills the "monthly furnished apartment" gap for relocations, extended work
// trips, and digital-nomad stays.
//
// Unauthenticated feasibility (recon-verified 2026-06-18):
//   - No API key, no headless browser, no member login required. The public
//     city search pages browse openly despite member-oriented marketing.
//   - Landing is a Next.js app. Each city search page embeds the full search
//     payload as SSR state. The data is served two ways:
//       1. The HTML page  GET /s/{market}/apartments/furnished  carries a
//          <script id="__NEXT_DATA__"> blob whose "buildId" identifies the
//          current deployment. The buildId rotates on every Landing deploy, so
//          it must be resolved dynamically rather than hard-coded.
//       2. The client-navigation data endpoint
//          GET /_next/data/{buildId}/s/{market}/apartments/furnished.json
//          returns the same search payload as pure JSON (~344 KB), which is
//          cheaper to parse than the full HTML page.
//   - robots.txt blocks /api/ and /apartments/search*, but the /s/ search
//     pages and their /_next/data JSON are allowed — this provider only uses
//     the allowed /s/ path.
//
// Two-step flow, mirroring the HomeToGo resolve-then-fetch pattern:
//  1. resolveLandingBuild(slug)  -> (buildId, canonicalMarket) from the city
//     page's __NEXT_DATA__ (buildId + serverQuery.market).
//  2. fetchLandingSearch(buildId, market) -> _next/data JSON
//     -> serverData.home_groups[].homes -> []models.HotelResult.
//
// Listings carry a monthly rent ("price"), bedrooms, baths, square footage,
// address, market, slug, and images. Leases are month-to-month, so prices are
// monthly furnished rents, not per-night rates — mapped with PriceBasis
// "monthly" and PropertyType "apartment".

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

// landingEnabled controls whether SearchLanding makes live HTTP requests.
// Disabled in the test suite (see testmain_test.go) so deterministic tests
// never fire real network calls; individual tests flip it on with a mock
// server injected via landingBaseURL.
var landingEnabled = true

// landingBaseURL is the root of the Landing site. Overridable in tests so an
// httptest.Server can stand in for the live host.
var landingBaseURL = "https://www.hellolanding.com"

// landingUserAgent is a desktop browser UA. Landing serves SSR HTML/JSON to
// ordinary desktop clients.
const landingUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

// landingLimiter enforces a conservative request rate. Two requests per
// search (resolve build + fetch JSON).
var landingLimiter = rate.NewLimiter(rate.Every(500*time.Millisecond), 2)

// landingHTTPClient is a dedicated client with a sane timeout.
var landingHTTPClient = &http.Client{
	Timeout: 30 * time.Second,
}

// landingBuildIDRe extracts the Next.js buildId from the __NEXT_DATA__ SSR
// blob, e.g. `"buildId":"qUBGx-cGXFChLQ0lNmkV2"`.
var landingBuildIDRe = regexp.MustCompile(`"buildId"\s*:\s*"([A-Za-z0-9_-]{6,})"`)

// landingMarketRe extracts the canonical market slug Landing resolved the city
// to, from serverQuery, e.g. `"market":"austin-tx"`. This handles the case
// where a bare slug ("austin") is canonicalised to "austin-tx".
var landingMarketRe = regexp.MustCompile(`"market"\s*:\s*"([a-z0-9-]{2,})"`)

// ---- Response types (only the fields we consume) ----

// landingNextData is the city-page __NEXT_DATA__ blob; we only need buildId.
type landingNextData struct {
	BuildID string `json:"buildId"`
}

// landingSearchResponse is the /_next/data JSON envelope.
type landingSearchResponse struct {
	PageProps struct {
		ServerData landingServerData `json:"serverData"`
	} `json:"pageProps"`
}

type landingServerData struct {
	HomeGroups []landingHomeGroup `json:"home_groups"`
}

type landingHomeGroup struct {
	Key   string        `json:"key"`
	Homes []landingHome `json:"homes"`
}

type landingHome struct {
	ID                          int64             `json:"id"`
	Name                        string            `json:"name"`
	Bedrooms                    int               `json:"bedrooms"`
	Baths                       string            `json:"baths"`
	SquareFootage               int               `json:"square_footage"`
	Price                       float64           `json:"price"`
	DiscountPrice               float64           `json:"discount_price"`
	MonthlyRent                 float64           `json:"monthly_rent"`
	DisplayPriceWithoutDiscount float64           `json:"display_price_without_discount"`
	Monthly                     bool              `json:"monthly"`
	MinimumNightlyStay          int               `json:"minimum_nightly_stay"`
	Slug                        string            `json:"slug"`
	Address                     string            `json:"address"`
	Street                      string            `json:"street"`
	Market                      string            `json:"market"`
	PropertyName                string            `json:"property_name"`
	PetsAllowed                 bool              `json:"pets_allowed"`
	HomeImage                   string            `json:"home_image"`
	HomeImages                  []landingImage    `json:"home_images"`
	DynamicImages               []landingDynImage `json:"dynamic_images"`
}

type landingImage struct {
	URL string `json:"url"`
}

type landingDynImage struct {
	DynamicImageURL string `json:"dynamic_image_url"`
}

// SearchLanding searches Landing for furnished mid-term apartments in a US
// city. It is non-fatal by contract: callers treat any error as "zero
// results". Returns nil, nil when disabled (test mode).
func SearchLanding(ctx context.Context, location string, opts HotelSearchOptions) ([]models.HotelResult, error) {
	if !landingEnabled {
		return nil, nil
	}
	if strings.TrimSpace(location) == "" {
		return nil, fmt.Errorf("landing: location is required")
	}

	// Step 1: resolve the Next.js buildId + canonical market slug from the
	// public city search page's __NEXT_DATA__.
	slug := landingSlug(location)
	slog.Debug("landing resolve", "location", location, "slug", slug)
	buildID, market, err := resolveLandingBuild(ctx, slug)
	if err != nil {
		return nil, fmt.Errorf("landing resolve: %w", err)
	}

	// Step 2: fetch the _next/data search JSON for that build + market.
	slog.Debug("landing search", "location", location, "buildId", buildID, "market", market)
	raw, err := fetchLandingSearch(ctx, buildID, market)
	if err != nil {
		return nil, fmt.Errorf("landing search: %w", err)
	}

	hotels, err := parseLandingHomes(raw)
	if err != nil {
		return nil, fmt.Errorf("landing parse: %w", err)
	}
	slog.Debug("landing results", "location", location, "count", len(hotels))
	return hotels, nil
}

// landingSlug converts a free-text location into the URL slug Landing uses for
// its city search pages: lowercase, spaces/commas/dots collapsed to single
// hyphens, ASCII-only path-safe. e.g. "Austin, TX" -> "austin-tx".
func landingSlug(location string) string {
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

// resolveLandingBuild fetches the city search page for a slug and extracts the
// Next.js buildId plus the canonical market slug from its SSR state. The HTTP
// client follows redirects, so a bare slug ("austin") that Landing canonicalises
// to "austin-tx" still resolves; the canonical market is read back from the
// page's serverQuery so the follow-up JSON request targets the right path.
func resolveLandingBuild(ctx context.Context, slug string) (buildID, market string, err error) {
	if slug == "" {
		return "", "", fmt.Errorf("empty slug")
	}
	url := landingBaseURL + "/s/" + slug + "/apartments/furnished"
	body, err := landingGet(ctx, url, "text/html,application/xhtml+xml")
	if err != nil {
		return "", "", err
	}

	// buildId: prefer a strict JSON decode of the __NEXT_DATA__ blob, fall
	// back to a regex scan if the blob shape shifts.
	buildID = landingExtractBuildID(body)
	if buildID == "" {
		return "", "", fmt.Errorf("no buildId found for slug %q", slug)
	}

	// Canonical market slug from serverQuery; fall back to the input slug.
	market = slug
	if m := landingMarketRe.FindSubmatch(body); m != nil {
		market = string(m[1])
	}
	return buildID, market, nil
}

// landingExtractBuildID pulls the buildId out of a city page, decoding the
// __NEXT_DATA__ script blob when present and falling back to a regex.
func landingExtractBuildID(body []byte) string {
	const open = `<script id="__NEXT_DATA__" type="application/json">`
	if i := strings.Index(string(body), open); i >= 0 {
		rest := body[i+len(open):]
		if j := strings.Index(string(rest), "</script>"); j >= 0 {
			var nd landingNextData
			if err := json.Unmarshal(rest[:j], &nd); err == nil && nd.BuildID != "" {
				return nd.BuildID
			}
		}
	}
	if m := landingBuildIDRe.FindSubmatch(body); m != nil {
		return string(m[1])
	}
	return ""
}

// fetchLandingSearch fetches the _next/data search JSON for a build + market.
func fetchLandingSearch(ctx context.Context, buildID, market string) (json.RawMessage, error) {
	if buildID == "" || market == "" {
		return nil, fmt.Errorf("empty buildId or market")
	}
	url := fmt.Sprintf("%s/_next/data/%s/s/%s/apartments/furnished.json?market=%s&searchType=furnished",
		landingBaseURL, buildID, market, market)
	body, err := landingGet(ctx, url, "application/json")
	if err != nil {
		return nil, err
	}
	return json.RawMessage(body), nil
}

// landingGet performs a rate-limited GET with the desktop UA and returns the
// response body. Non-2xx responses are errors.
func landingGet(ctx context.Context, url, accept string) ([]byte, error) {
	if err := landingLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", landingUserAgent)
	req.Header.Set("Accept", accept)
	resp, err := landingHTTPClient.Do(req)
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

// parseLandingHomes maps a Landing _next/data search payload to HotelResults.
// Every home in every group is mapped; homes without a positive price are
// skipped so each returned result is actually comparable. Prices are monthly
// furnished rents (USD), tagged PriceBasis "monthly".
func parseLandingHomes(raw json.RawMessage) ([]models.HotelResult, error) {
	var resp landingSearchResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	var results []models.HotelResult
	for _, g := range resp.PageProps.ServerData.HomeGroups {
		for _, h := range g.Homes {
			price := landingPrice(h)
			if price <= 0 {
				continue // no usable price -> not comparable, skip
			}
			name := strings.TrimSpace(h.Name)
			if name == "" {
				name = strings.TrimSpace(h.PropertyName)
			}
			if name == "" {
				name = "Landing furnished apartment"
			}
			res := models.HotelResult{
				Name:         name,
				HotelID:      landingID(h),
				Price:        price,
				Currency:     "USD",
				Address:      landingAddress(h),
				Description:  landingDescription(h),
				BookingURL:   landingBookingURL(h.Slug),
				ImageURL:     landingImageURL(h),
				Lat:          0, // not present in payload; aggregator geocodes
				Lon:          0,
				PropertyType: "apartment",
				PriceBasis:   "monthly",
			}
			res.Amenities = landingAmenities(h)
			res.Sources = []models.PriceSource{{
				Provider:   "landing",
				Price:      price,
				Currency:   "USD",
				BookingURL: res.BookingURL,
				PriceBasis: "monthly",
			}}
			results = append(results, res)
		}
	}
	return results, nil
}

// landingPrice picks the best monthly rent for a home, preferring the
// (possibly discounted) "price", then discount_price, then monthly_rent.
func landingPrice(h landingHome) float64 {
	switch {
	case h.Price > 0:
		return h.Price
	case h.DiscountPrice > 0:
		return h.DiscountPrice
	case h.MonthlyRent > 0:
		return h.MonthlyRent
	}
	return 0
}

// landingID returns a stable provider-scoped home id.
func landingID(h landingHome) string {
	if h.ID > 0 {
		return fmt.Sprintf("landing-%d", h.ID)
	}
	if h.Slug != "" {
		return "landing-" + h.Slug
	}
	return ""
}

// landingAddress prefers the full address, falling back to street + market.
func landingAddress(h landingHome) string {
	if a := strings.TrimSpace(h.Address); a != "" {
		return a
	}
	parts := make([]string, 0, 2)
	if s := strings.TrimSpace(h.Street); s != "" {
		parts = append(parts, s)
	}
	if m := strings.TrimSpace(h.Market); m != "" {
		parts = append(parts, m)
	}
	return strings.Join(parts, ", ")
}

// landingBookingURL builds the public listing URL for a home slug.
func landingBookingURL(slug string) string {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return ""
	}
	return landingBaseURL + "/homes/" + slug
}

// landingImageURL picks the best available image. Landing image links are
// already absolute (https://files.hellolanding.com/...).
func landingImageURL(h landingHome) string {
	if u := strings.TrimSpace(h.HomeImage); u != "" {
		return u
	}
	for _, im := range h.HomeImages {
		if u := strings.TrimSpace(im.URL); u != "" {
			return u
		}
	}
	for _, im := range h.DynamicImages {
		if u := strings.TrimSpace(im.DynamicImageURL); u != "" {
			return u
		}
	}
	return ""
}

// landingAmenities surfaces a few structured facts as amenity tags so the
// merge/render layers can show them. Furnished is implicit for every Landing
// unit; month-to-month flags the flexible mid-term lease.
func landingAmenities(h landingHome) []string {
	am := []string{"Furnished", "Month-to-month"}
	if h.PetsAllowed {
		am = append(am, "Pets allowed")
	}
	return am
}

// landingDescription summarises the unit's size facts (bedrooms, baths, area)
// as a short human-readable tagline. Returns "" when no facts are present.
func landingDescription(h landingHome) string {
	parts := make([]string, 0, 3)
	if h.Bedrooms > 0 {
		parts = append(parts, fmt.Sprintf("%d bedroom", h.Bedrooms))
	} else if h.Bedrooms == 0 {
		parts = append(parts, "Studio")
	}
	if b := strings.TrimSpace(h.Baths); b != "" && b != "0" && b != "0.0" {
		parts = append(parts, b+" bath")
	}
	if h.SquareFootage > 0 {
		parts = append(parts, fmt.Sprintf("%d sq ft", h.SquareFootage))
	}
	return strings.Join(parts, ", ")
}
