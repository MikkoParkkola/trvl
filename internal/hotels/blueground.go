package hotels

// Blueground mid-term furnished-apartment provider.
//
// Blueground (https://www.theblueground.com) lists professionally-managed
// furnished apartments for monthly stays across Europe / US / MENA.
//
// Unauthenticated feasibility (recon-verified live 2026-06-18, Tier-B):
//   - Plain Go HTTP with a desktop Chrome UA; no cookie/CAPTCHA. MUST route
//     through providers.Tier1Client: a non-browser TLS fingerprint is 301'd to
//     the homepage, so the standard library client cannot read listing pages.
//
// CONTRACT SURPRISES vs the original spec (verified against captured fixtures):
//   - List URL is /furnished-apartments-{city}-{country} (e.g.
//     "furnished-apartments-athens-greece"), NOT /m/furnished-apartments/{city}-{ISO3};
//     the /m/ path 301s to the homepage.
//   - Per-listing data is in window.__INITIAL_STATE__
//     (.properties.allProperties.main[]), NOT a JSON-LD ItemList. The JSON-LD
//     Apartment blocks carry lat/lon but no price.
//   - Price is NOT on the list page (baseRent/rent amount are 0 there). It lives
//     on the detail page (/p/furnished-apartments/{id}) as "lowestRent":{amount,
//     currency}, alongside "minStayMonths". So a 2nd hop is required for PRICE
//     (and minStay), while lat/lon/sqm/beds/address come from the list page.
//
// To bound cost, only the top-N (blueGroundDetailLimit) list properties get the
// detail hop; the rest are dropped (logged) since a result without a price is
// not comparable.
//
// Property -> models.HotelResult:
//   code/path        -> HotelID + url (https://www.theblueground.com/{path})
//   name             -> Name
//   address.lat/lng  -> Lat / Lon
//   address fields   -> Address / Neighborhood
//   lotSize          -> sqm (folded into Description)
//   bedrooms         -> bed count (folded into Description)
//   lowestRent       -> Price / Currency      (detail hop)
//   minStayMonths    -> Description note       (detail hop)

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/logredact"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/providers"
	"golang.org/x/time/rate"
)

var bluegroundEnabled = true

var bluegroundBaseURL = "https://www.theblueground.com"

const bluegroundUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

// bluegroundDetailLimit caps the number of per-listing detail hops per search.
var bluegroundDetailLimit = 8

var bluegroundLimiter = rate.NewLimiter(rate.Every(500*time.Millisecond), 2)

var bluegroundClient providers.Fetcher
var bluegroundClientOnce sync.Once

func bluegroundFetcher() providers.Fetcher {
	if bluegroundClient != nil {
		return bluegroundClient
	}
	bluegroundClientOnce.Do(func() {
		if c, err := providers.NewTier1Client(); err == nil {
			bluegroundClient = c
		}
	})
	return bluegroundClient
}

var bluegroundStateRe = regexp.MustCompile(
	`(?s)window\.__INITIAL_STATE__\s*=\s*(\{.*?\})\s*;?\s*</script>`)
var bluegroundLowestRentRe = regexp.MustCompile(
	`"lowestRent"\s*:\s*\{\s*"amount"\s*:\s*([0-9]+(?:\.[0-9]+)?)\s*,\s*"currency"\s*:\s*"([A-Z]{3})"`)
var bluegroundMinStayRe = regexp.MustCompile(`"minStayMonths"\s*:\s*([0-9]+)`)

// ---- list-page state types (only the fields we consume) ----

type bluegroundState struct {
	Properties struct {
		AllProperties struct {
			Main []bluegroundProperty `json:"main"`
		} `json:"allProperties"`
	} `json:"properties"`
}

type bluegroundProperty struct {
	Code     string  `json:"code"`
	Name     string  `json:"name"`
	Path     string  `json:"path"`
	Bedrooms int     `json:"bedrooms"`
	LotSize  float64 `json:"lotSize"`
	Address  struct {
		Building string  `json:"building"`
		Lng      float64 `json:"lng"`
		Lat      float64 `json:"lat"`
		City     string  `json:"city"`
		Area     string  `json:"area"`
	} `json:"address"`
}

// SearchBlueground searches Blueground for monthly furnished apartments. It is
// non-fatal by contract. Returns nil, nil when disabled (test mode).
func SearchBlueground(ctx context.Context, location string, opts HotelSearchOptions) ([]models.HotelResult, error) {
	if !bluegroundEnabled {
		return nil, nil
	}
	if strings.TrimSpace(location) == "" {
		return nil, fmt.Errorf("blueground: location is required")
	}
	slug := bluegroundSlug(location)
	if slug == "" {
		return nil, fmt.Errorf("blueground: could not derive slug from %q", location)
	}

	slog.Debug("blueground search", "location", location, "slug", slug)
	body, err := bluegroundGet(ctx, bluegroundBaseURL+"/"+slug)
	if err != nil {
		return nil, fmt.Errorf("blueground fetch: %w", err)
	}
	props, err := parseBluegroundList(body)
	if err != nil {
		return nil, fmt.Errorf("blueground parse: %w", err)
	}

	results := make([]models.HotelResult, 0, len(props))
	for i, p := range props {
		if i >= bluegroundDetailLimit {
			slog.Debug("blueground detail-hop limit reached", "limit", bluegroundDetailLimit, "dropped", len(props)-i)
			break
		}
		h := bluegroundBaseResult(p)
		// Detail hop for price + minStay.
		if dbody, derr := bluegroundGet(ctx, bluegroundDetailURL(p.Path)); derr == nil {
			price, currency, minStay := parseBluegroundDetail(dbody)
			if price > 0 {
				h.Price = price
				if currency != "" {
					h.Currency = currency
				}
				h.Sources = []models.PriceSource{{
					Provider:   "blueground",
					Price:      price,
					Currency:   h.Currency,
					BookingURL: h.BookingURL,
				}}
			}
			if minStay > 0 {
				h.Description = h.Description + fmt.Sprintf(" · %d-month min", minStay)
			}
		} else {
			slog.Debug("blueground detail hop failed", "path", logredact.Path(p.Path), "error", logredact.Err(derr))
		}
		if h.Price <= 0 {
			continue // not comparable without a price
		}
		results = append(results, h)
	}
	slog.Debug("blueground results", "location", location, "count", len(results))
	return results, nil
}

// bluegroundCountryISO2 maps full country names (the trailing token of a
// "City, Country" location) to the ISO 3166-1 alpha-2 code Blueground now uses
// in its listing slugs. As of 2026-06 the live pattern is
// "furnished-apartments-{city}-{iso2}" (e.g. "...-paris-fr"); the older
// full-country form ("...-paris-france") 404s. Covers Blueground's served
// markets; unmapped countries fall through to the verbatim token.
var bluegroundCountryISO2 = map[string]string{
	"usa": "us", "united states": "us", "united states of america": "us",
	"uk": "gb", "united kingdom": "gb", "england": "gb",
	"france": "fr", "germany": "de", "austria": "at", "luxembourg": "lu",
	"switzerland": "ch", "belgium": "be", "italy": "it", "netherlands": "nl",
	"norway": "no", "sweden": "se", "croatia": "hr", "poland": "pl",
	"denmark": "dk", "cyprus": "cy", "malta": "mt", "romania": "ro",
	"hungary": "hu", "iceland": "is", "ireland": "ie", "latvia": "lv",
	"algeria": "dz", "greece": "gr", "portugal": "pt", "spain": "es",
	"turkey": "tr", "türkiye": "tr", "mexico": "mx", "czechia": "cz",
	"czech republic": "cz", "uae": "ae", "united arab emirates": "ae",
	"saudi arabia": "sa", "egypt": "eg",
}

// bluegroundSlug builds the listing slug from a "City, Country" location:
// "Athens, Greece" -> "furnished-apartments-athens-gr". The country is mapped to
// its ISO-3166 alpha-2 code (the form Blueground's listing pages now use);
// unmapped countries keep their verbatim slug token. A bare city (no country)
// yields a country-less slug that may 404 (handled non-fatally).
func bluegroundSlug(location string) string {
	loc := strings.TrimSpace(location)
	city := loc
	country := ""
	if i := strings.LastIndex(loc, ","); i >= 0 {
		city = strings.TrimSpace(loc[:i])
		country = strings.TrimSpace(loc[i+1:])
	}
	citySlug := bluegroundToken(city)
	if citySlug == "" {
		return ""
	}
	if country == "" {
		return "furnished-apartments-" + citySlug
	}
	countrySlug := bluegroundToken(country)
	if iso, ok := bluegroundCountryISO2[strings.ToLower(country)]; ok {
		countrySlug = iso
	}
	if countrySlug == "" {
		return "furnished-apartments-" + citySlug
	}
	return "furnished-apartments-" + citySlug + "-" + countrySlug
}

// bluegroundToken slugifies a single free-text token to lowercase hyphenated
// ASCII (dropping accents/punctuation).
func bluegroundToken(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
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
		}
	}
	return strings.Trim(b.String(), "-")
}

func bluegroundDetailURL(path string) string {
	return bluegroundBaseURL + "/" + strings.TrimPrefix(strings.TrimSpace(path), "/")
}

func bluegroundGet(ctx context.Context, url string) ([]byte, error) {
	if err := bluegroundLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter: %w", err)
	}
	f := bluegroundFetcher()
	if f == nil {
		return nil, fmt.Errorf("no fetcher available")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", bluegroundUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	resp, err := f.Do(req)
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
	return io.ReadAll(io.LimitReader(resp.Body, 16<<20))
}

// parseBluegroundList extracts properties from the list page __INITIAL_STATE__.
func parseBluegroundList(html []byte) ([]bluegroundProperty, error) {
	m := bluegroundStateRe.FindSubmatch(html)
	if m == nil {
		return nil, fmt.Errorf("no __INITIAL_STATE__ found")
	}
	var state bluegroundState
	if err := json.Unmarshal(m[1], &state); err != nil {
		return nil, fmt.Errorf("decode state: %w", err)
	}
	return state.Properties.AllProperties.Main, nil
}

// bluegroundBaseResult maps a list property (sans price) to a HotelResult.
func bluegroundBaseResult(p bluegroundProperty) models.HotelResult {
	id := strings.TrimSpace(p.Code)
	name := strings.TrimSpace(p.Name)
	if name == "" {
		name = "Blueground apartment"
	}
	addrParts := make([]string, 0, 3)
	if p.Address.Area != "" {
		addrParts = append(addrParts, p.Address.Area)
	}
	if p.Address.City != "" {
		addrParts = append(addrParts, p.Address.City)
	}

	descParts := []string{"Furnished apartment"}
	if p.Bedrooms > 0 {
		descParts = append(descParts, fmt.Sprintf("%d bed", p.Bedrooms))
	}
	if p.LotSize > 0 {
		descParts = append(descParts, fmt.Sprintf("%.0f m²", p.LotSize))
	}

	return models.HotelResult{
		Name:         name,
		HotelID:      id,
		Currency:     "EUR",
		Address:      strings.Join(addrParts, ", "),
		Neighborhood: strings.TrimSpace(p.Address.Area),
		Lat:          p.Address.Lat,
		Lon:          p.Address.Lng,
		PropertyType: "apartment",
		BookingURL:   bluegroundDetailURL(p.Path),
		Description:  strings.Join(descParts, " · "),
	}
}

// parseBluegroundDetail extracts price, currency and minStay (months) from a
// detail page's embedded JSON.
func parseBluegroundDetail(html []byte) (price float64, currency string, minStayMonths int) {
	if m := bluegroundLowestRentRe.FindSubmatch(html); m != nil {
		price = parseDecimal(string(m[1]))
		currency = strings.ToUpper(string(m[2]))
	}
	if m := bluegroundMinStayRe.FindSubmatch(html); m != nil {
		minStayMonths = int(parseDecimal(string(m[1])))
	}
	return price, currency, minStayMonths
}
