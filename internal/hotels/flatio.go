package hotels

// Flatio mid-term furnished-apartment provider.
//
// Flatio (https://www.flatio.com) lists furnished apartments for monthly stays —
// the relocation / remote-work segment nightly-rate providers miss.
//
// Unauthenticated feasibility (recon-verified live 2026-06-18, Tier-B):
//   - Plain Go HTTP with a desktop Chrome UA; no cookie, browser nor CAPTCHA.
//     Routed through providers.Tier1Client for TLS-fingerprint resilience.
//   - The listing page is SSR HTML:
//       GET /i/apartments-for-rent-{city}/monthly  ->  200 text/html
//     embedding a <script type="application/json" id="markerData"> whose body is
//     a JSON array of map markers. Pagination is ?page=N.
//
// Marker -> models.HotelResult (verified field names):
//   lat / lng   -> Lat / Lon
//   priceHtml   -> Price (nightly; "<span class=number>82</span> … €" -> 82, EUR)
//   uniqId      -> HotelID
//   link        -> BookingURL (absolute https://www.flatio.com/search/detail/…)
//
// CONTRACT NOTE: monthly-total, m² and title are NOT in the marker JSON — they
// live in per-card HTML blocks that are not reliably machine-parseable without a
// brittle DOM walk. They are intentionally omitted (price here is the nightly
// figure Flatio surfaces on the marker). Name falls back to the city + id.

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
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/providers"
	"golang.org/x/time/rate"
)

// flatioEnabled controls whether SearchFlatio makes live HTTP requests. Disabled
// in the test suite (see testmain_test.go).
var flatioEnabled = true

// flatioBaseURL is the root of the Flatio site. Overridable in tests.
var flatioBaseURL = "https://www.flatio.com"

const flatioUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

var flatioLimiter = rate.NewLimiter(rate.Every(500*time.Millisecond), 2)

// flatioClient is the Fetcher used for requests; nil means lazily build a
// providers.Tier1Client. Tests inject a plain *http.Client.
var flatioClient providers.Fetcher
var flatioClientOnce sync.Once

func flatioFetcher() providers.Fetcher {
	if flatioClient != nil {
		return flatioClient
	}
	flatioClientOnce.Do(func() {
		if c, err := providers.NewTier1Client(); err == nil {
			flatioClient = c
		}
	})
	return flatioClient
}

// flatioMarkerDataRe extracts the markerData JSON array from the SSR HTML.
var flatioMarkerDataRe = regexp.MustCompile(
	`(?s)<script[^>]*id="markerData"[^>]*>\s*(\[.*?\])\s*</script>`)

// flatioNumberRe extracts the integer price from priceHtml's number span.
var flatioNumberRe = regexp.MustCompile(`<span class="number">\s*([0-9.,]+)\s*</span>`)

type flatioMarker struct {
	Lat       float64         `json:"lat"`
	Lng       float64         `json:"lng"`
	Marker    string          `json:"marker"`
	PriceHTML string          `json:"priceHtml"`
	UniqID    json.RawMessage `json:"uniqId"`
	Link      string          `json:"link"`
}

// SearchFlatio searches Flatio for monthly furnished apartments in a city. It is
// non-fatal by contract. Returns nil, nil when disabled (test mode).
func SearchFlatio(ctx context.Context, location string, opts HotelSearchOptions) ([]models.HotelResult, error) {
	if !flatioEnabled {
		return nil, nil
	}
	if strings.TrimSpace(location) == "" {
		return nil, fmt.Errorf("flatio: location is required")
	}
	city := flatioCitySlug(location)
	if city == "" {
		return nil, fmt.Errorf("flatio: could not derive city slug from %q", location)
	}

	slog.Debug("flatio search", "location", location, "city", city)
	body, err := flatioGet(ctx, flatioBaseURL+"/i/apartments-for-rent-"+city+"/monthly")
	if err != nil {
		return nil, fmt.Errorf("flatio fetch: %w", err)
	}

	hotels, err := parseFlatioHTML(body, strings.TrimSpace(opts.Currency))
	if err != nil {
		return nil, fmt.Errorf("flatio parse: %w", err)
	}
	slog.Debug("flatio results", "location", location, "count", len(hotels))
	return hotels, nil
}

// flatioCitySlug reduces a free-text location to the single-token city slug
// Flatio uses (lowercase, first segment before any comma).
func flatioCitySlug(location string) string {
	return spotahomeCitySlug(location) // identical slug rules
}

func flatioGet(ctx context.Context, url string) ([]byte, error) {
	if err := flatioLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter: %w", err)
	}
	f := flatioFetcher()
	if f == nil {
		return nil, fmt.Errorf("no fetcher available")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", flatioUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	resp, err := f.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("unexpected status %d for %s", resp.StatusCode, url)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 16<<20))
}

// parseFlatioHTML extracts the markerData array and maps each apartment marker
// to a HotelResult. Markers without a positive price are skipped.
func parseFlatioHTML(html []byte, fallbackCurrency string) ([]models.HotelResult, error) {
	m := flatioMarkerDataRe.FindSubmatch(html)
	if m == nil {
		return nil, fmt.Errorf("no markerData script found")
	}
	var markers []flatioMarker
	if err := json.Unmarshal(m[1], &markers); err != nil {
		return nil, fmt.Errorf("decode markerData: %w", err)
	}
	currency := "EUR"
	if fallbackCurrency != "" {
		currency = strings.ToUpper(fallbackCurrency)
	}
	results := make([]models.HotelResult, 0, len(markers))
	for _, mk := range markers {
		if strings.TrimSpace(mk.Marker) != "apartment" {
			continue
		}
		price := flatioParsePrice(mk.PriceHTML)
		if price <= 0 {
			continue
		}
		id := flatioID(mk.UniqID)
		if id == "" {
			continue
		}
		h := models.HotelResult{
			Name:         "Flatio apartment " + id,
			HotelID:      id,
			Price:        price,
			Currency:     currency,
			Lat:          mk.Lat,
			Lon:          mk.Lng,
			BookingURL:   strings.TrimSpace(mk.Link),
			PropertyType: "apartment",
			Description:  "Furnished apartment · monthly stay",
		}
		h.Sources = []models.PriceSource{{
			Provider:   "flatio",
			Price:      price,
			Currency:   currency,
			BookingURL: h.BookingURL,
		}}
		results = append(results, h)
	}
	return results, nil
}

// flatioParsePrice strips priceHtml to its numeric value.
func flatioParsePrice(priceHTML string) float64 {
	m := flatioNumberRe.FindStringSubmatch(priceHTML)
	if m == nil {
		return 0
	}
	return parseDecimal(m[1])
}

// flatioID coerces the uniqId (number or string) to a string.
func flatioID(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err == nil {
		return strconv.FormatInt(n, 10)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	return ""
}
