package hotels

// Uniplaces mid-term / student-housing provider.
//
// Uniplaces (https://www.uniplaces.com) lists rooms, studios and apartments for
// medium-term stays (weeks to months) — a segment traditional nightly-rate hotel
// providers (Google Hotels, Trivago, Booking) miss entirely. High value for
// relocation, student housing and extended remote-work stays.
//
// Unauthenticated feasibility (verified 2026-06-18):
//   - No API key, headless browser, residential proxy nor CAPTCHA bypass is
//     required. Plain HTTP with a desktop User-Agent suffices.
//   - The listing page is a Next.js app whose search data is exposed as static
//     SSR JSON at:
//       GET /_next/data/{buildId}/en/accommodation/{city}.json -> 200 JSON
//   - {buildId} rotates per deploy (mirrors the Wizz Air version-rotation
//     gotcha). It is NOT hardcoded: it is read at request time from the
//     "buildId" field of the __NEXT_DATA__ <script> blob on the listing page
//     (GET /accommodation/{city}), then substituted into the _next/data URL.
//
// Two-step flow, mirroring HomeToGo's resolve-then-fetch pattern:
//  1. resolveUniplacesBuildID(city) -> buildId   (from listing page __NEXT_DATA__)
//  2. fetchUniplacesOffers(buildId, city) -> offer JSON -> []models.HotelResult
//
// Response shape (verified, NOT the originally-assumed `units[]`):
//   pageProps.offers.data[] (48 per page), each:
//     id                                      -> offer id (HotelID + listing URL)
//     attributes.accommodation_offer.title    -> Name
//     attributes.accommodation_offer.price.amount        (CENTS -> /100)
//     attributes.accommodation_offer.price.currency_code -> Currency
//     attributes.property.coordinates          -> [lat, lon]  (VERIFIED order:
//        Lisbon = [38.75, -9.19]; index 0 is latitude, index 1 longitude)
//     attributes.property.type                 -> PropertyType ("apartment", ...)
//     attributes.property.neighbourhood.name   -> Neighborhood
//
// Listing URL is built from the offer id: /accommodation/{city}/{id} (verified
// 200). Photos carry only opaque hashes with no publicly-derivable CDN URL
// template (cdn.uniplaces.com/property-photos/<hash> returns 403, unverified),
// so ImageURL is deliberately left empty rather than shipping a guessed URL.

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

// uniplacesEnabled controls whether SearchUniplaces makes live HTTP requests.
// Disabled in the test suite (see testmain_test.go) so deterministic tests
// never fire real network calls; individual tests flip it on with a mock
// server injected via uniplacesBaseURL.
var uniplacesEnabled = true

// uniplacesBaseURL is the root of the Uniplaces site. Overridable in tests so
// an httptest.Server can stand in for the live host.
var uniplacesBaseURL = "https://www.uniplaces.com"

// uniplacesUserAgent is a desktop browser UA. Uniplaces serves the SSR JSON/HTML
// to ordinary desktop clients; no special headers are required.
const uniplacesUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

// uniplacesLimiter enforces a conservative request rate to avoid tripping the
// edge rate limits. Two requests per search (resolve buildId + fetch offers).
// Mirrors the per-provider rate.Limiter used by flights/wizzair.go.
var uniplacesLimiter = rate.NewLimiter(rate.Every(500*time.Millisecond), 2)

// uniplacesHTTPClient is a dedicated client with a sane timeout.
var uniplacesHTTPClient = &http.Client{
	Timeout: 30 * time.Second,
}

// uniplacesBuildIDRe extracts the Next.js build id from the listing page's
// __NEXT_DATA__ blob, e.g. `"buildId":"search-06e8c595..."`. The id rotates per
// deploy, so it is read dynamically rather than hardcoded.
var uniplacesBuildIDRe = regexp.MustCompile(`"buildId"\s*:\s*"([^"]+)"`)

// ---- Response types (only the fields we consume) ----

type uniplacesResponse struct {
	PageProps struct {
		Offers struct {
			Data []uniplacesOffer `json:"data"`
		} `json:"offers"`
	} `json:"pageProps"`
}

type uniplacesOffer struct {
	ID         string `json:"id"`
	Attributes struct {
		AccommodationOffer struct {
			Title          string  `json:"title"`
			Description    string  `json:"description"`
			ReviewsAverage float64 `json:"reviews_average"`
			ReviewsCount   int     `json:"reviews_count"`
			MaxGuests      int     `json:"max_guests"`
			ContractType   string  `json:"contract_type"`
			IsSoldOut      bool    `json:"is_sold_out"`
			Price          struct {
				Amount       int64  `json:"amount"` // CENTS
				CurrencyCode string `json:"currency_code"`
			} `json:"price"`
		} `json:"accommodation_offer"`
		Property struct {
			Type          string    `json:"type"`
			Coordinates   []float64 `json:"coordinates"` // [lat, lon]
			Neighbourhood struct {
				Name string `json:"name"`
			} `json:"neighbourhood"`
		} `json:"property"`
	} `json:"attributes"`
}

// SearchUniplaces searches Uniplaces for mid-term rooms/apartments in a city.
// It is non-fatal by contract: callers treat any error as "zero results".
// Returns nil, nil when disabled (test mode).
func SearchUniplaces(ctx context.Context, location string, opts HotelSearchOptions) ([]models.HotelResult, error) {
	if !uniplacesEnabled {
		return nil, nil
	}
	if strings.TrimSpace(location) == "" {
		return nil, fmt.Errorf("uniplaces: location is required")
	}

	city := uniplacesCitySlug(location)
	if city == "" {
		return nil, fmt.Errorf("uniplaces: could not derive city slug from %q", location)
	}

	// Step 1: read the rotating Next.js buildId from the listing page.
	slog.Debug("uniplaces resolve", "location", location, "city", city)
	buildID, err := resolveUniplacesBuildID(ctx, city)
	if err != nil {
		return nil, fmt.Errorf("uniplaces resolve: %w", err)
	}

	// Step 2: fetch the SSR JSON offers for that city.
	slog.Debug("uniplaces search", "location", location, "city", city, "buildId", buildID)
	raw, err := fetchUniplacesOffers(ctx, buildID, city)
	if err != nil {
		return nil, fmt.Errorf("uniplaces offers: %w", err)
	}

	currency := strings.TrimSpace(opts.Currency)
	hotels, err := parseUniplacesOffers(raw, city, currency)
	if err != nil {
		return nil, fmt.Errorf("uniplaces parse: %w", err)
	}
	slog.Debug("uniplaces results", "location", location, "count", len(hotels))
	return hotels, nil
}

// uniplacesCitySlug reduces a free-text location to the single-token city slug
// Uniplaces uses in its accommodation URLs: lowercase, first path segment only
// (Uniplaces routes are /accommodation/{city}, not multi-word). e.g.
// "Lisbon, Portugal" -> "lisbon", "New York" -> "new-york".
func uniplacesCitySlug(location string) string {
	s := strings.ToLower(strings.TrimSpace(location))
	// Take the part before the first comma (drop ", Portugal" etc).
	if i := strings.IndexByte(s, ','); i >= 0 {
		s = s[:i]
	}
	var b strings.Builder
	prevHyphen := false
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevHyphen = false
		case r == ' ' || r == '-' || r == '/' || r == '.':
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

// resolveUniplacesBuildID fetches the city listing page and extracts the
// rotating Next.js buildId from its __NEXT_DATA__ blob.
func resolveUniplacesBuildID(ctx context.Context, city string) (string, error) {
	if city == "" {
		return "", fmt.Errorf("empty city")
	}
	body, err := uniplacesGet(ctx, uniplacesBaseURL+"/accommodation/"+city, "text/html,application/xhtml+xml")
	if err != nil {
		return "", err
	}
	m := uniplacesBuildIDRe.FindSubmatch(body)
	if m == nil {
		return "", fmt.Errorf("no buildId found on listing page for city %q", city)
	}
	return string(m[1]), nil
}

// fetchUniplacesOffers fetches the SSR JSON search payload for a city using the
// resolved buildId.
func fetchUniplacesOffers(ctx context.Context, buildID, city string) (json.RawMessage, error) {
	if buildID == "" {
		return nil, fmt.Errorf("empty buildId")
	}
	url := uniplacesBaseURL + "/_next/data/" + buildID + "/en/accommodation/" + city + ".json"
	body, err := uniplacesGet(ctx, url, "application/json")
	if err != nil {
		return nil, err
	}
	return json.RawMessage(body), nil
}

// uniplacesGet performs a rate-limited GET with the desktop UA and returns the
// response body. Non-2xx responses are errors.
func uniplacesGet(ctx context.Context, url, accept string) ([]byte, error) {
	if err := uniplacesLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", uniplacesUserAgent)
	req.Header.Set("Accept", accept)
	resp, err := uniplacesHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := validateDestinationResponseURL(req.URL, resp.Request.URL); err != nil {
		return nil, fmt.Errorf("destination scope: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("unexpected status %d for %s", resp.StatusCode, url)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20)) // 8 MiB cap
	if err != nil {
		return nil, err
	}
	return body, nil
}

// parseUniplacesOffers maps a Uniplaces search JSON payload to HotelResults.
// Offers without a positive price or that are sold out are skipped so every
// returned result is actually comparable and bookable. fallbackCurrency is used
// when an offer's price carries no currency code; when empty it defaults to EUR
// (Uniplaces' default for the public endpoint).
func parseUniplacesOffers(raw json.RawMessage, city, fallbackCurrency string) ([]models.HotelResult, error) {
	var resp uniplacesResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	offers := resp.PageProps.Offers.Data
	results := make([]models.HotelResult, 0, len(offers))
	for _, o := range offers {
		ao := o.Attributes.AccommodationOffer
		if ao.IsSoldOut {
			continue
		}
		// Price is in cents; convert to a major-unit amount.
		price := float64(ao.Price.Amount) / 100.0
		if price <= 0 {
			continue
		}
		currency := strings.ToUpper(strings.TrimSpace(ao.Price.CurrencyCode))
		if currency == "" {
			if fallbackCurrency != "" {
				currency = strings.ToUpper(fallbackCurrency)
			} else {
				currency = "EUR"
			}
		}

		h := models.HotelResult{
			Name:         strings.TrimSpace(ao.Title),
			HotelID:      o.ID,
			Price:        price,
			Currency:     currency,
			Description:  strings.TrimSpace(ao.Description),
			Rating:       ao.ReviewsAverage,
			ReviewCount:  ao.ReviewsCount,
			PropertyType: strings.TrimSpace(o.Attributes.Property.Type),
			Neighborhood: strings.TrimSpace(o.Attributes.Property.Neighbourhood.Name),
			BookingURL:   uniplacesListingURL(city, o.ID),
		}
		// coordinates are [lat, lon] (verified order).
		if coords := o.Attributes.Property.Coordinates; len(coords) == 2 {
			h.Lat = coords[0]
			h.Lon = coords[1]
		}
		if h.Name == "" {
			h.Name = "Uniplaces accommodation"
		}
		h.Sources = []models.PriceSource{{
			Provider:   "uniplaces",
			Price:      price,
			Currency:   currency,
			BookingURL: h.BookingURL,
		}}
		results = append(results, h)
	}
	return results, nil
}

// uniplacesListingURL builds the canonical listing URL for an offer from its id.
// Verified: /accommodation/{city}/{id} returns 200.
func uniplacesListingURL(city, id string) string {
	if id == "" {
		return ""
	}
	if city == "" {
		return uniplacesBaseURL + "/accommodation/" + id
	}
	return uniplacesBaseURL + "/accommodation/" + city + "/" + id
}
