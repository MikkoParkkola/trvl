package hotels

// Flatio mid-term furnished-apartment provider.
//
// Flatio (https://www.flatio.com) lists furnished apartments for monthly stays —
// the relocation / remote-work segment nightly-rate providers miss.
//
// Unauthenticated feasibility (recon-verified live 2026-06-20, Tier-B):
//   - Plain Go HTTP with a desktop Chrome UA; no cookie, browser nor CAPTCHA.
//     Routed through providers.Tier1Client for TLS-fingerprint resilience.
//   - The search page is SSR HTML at the city route:
//       GET /s/{City}  ->  200 text/html
//     where {City} is Title_Case with underscores for spaces (e.g. /s/Paris,
//     /s/Abu_Dhabi). The page embeds a schema.org JSON-LD block:
//       <script type="application/ld+json"> { "@graph": [ { "@type":"ItemList",
//         "itemListElement": [ { "@type":"ListItem", "item": {
//           "@type":["Apartment","Product"], "name", "url", "image",
//           "offers": { "price", "priceCurrency", ... } } } ] } ] }
//
// NOTE (drift fix): the prior `/i/apartments-for-rent-{city}/monthly` +
// <script id="markerData"> path 404s as of 2026-06; the live page no longer
// emits markerData. We now parse the stable schema.org JSON-LD ItemList. The
// JSON-LD carries no lat/lng, so Lat/Lon are left zero (Flatio surfaces the
// nightly price; monthly total / m² are not in the structured block).

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

// flatioLDJSONRe extracts each schema.org JSON-LD block from the SSR HTML.
var flatioLDJSONRe = regexp.MustCompile(
	`(?s)<script[^>]*type="application/ld\+json"[^>]*>\s*(\{.*?\})\s*</script>`)

// flatioIDRe pulls the numeric listing id from a Flatio apartment URL such as
// https://www.flatio.com/rent/apartment/125151-paris -> 125151.
var flatioIDRe = regexp.MustCompile(`/apartment/(\d+)`)

// flatioLDDoc models the subset of the JSON-LD graph we consume.
type flatioLDDoc struct {
	Graph []flatioLDNode `json:"@graph"`
}

type flatioLDNode struct {
	Type        string             `json:"@type"`
	ItemListEle []flatioLDListItem `json:"itemListElement"`
}

type flatioLDListItem struct {
	Item flatioLDItem `json:"item"`
}

type flatioLDItem struct {
	Name   string         `json:"name"`
	URL    string         `json:"url"`
	Image  string         `json:"image"`
	Offers flatioLDOffers `json:"offers"`
}

type flatioLDOffers struct {
	Price         json.Number `json:"price"`
	PriceCurrency string      `json:"priceCurrency"`
	URL           string      `json:"url"`
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
	city := flatioLocationToken(location)
	if city == "" {
		return nil, fmt.Errorf("flatio: could not derive city token from %q", location)
	}

	slog.Debug("flatio search", "location", location, "city", city)
	body, err := flatioGet(ctx, flatioBaseURL+"/s/"+city)
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

// flatioLocationToken converts a free-text location to the Title_Case
// underscore-joined city token Flatio's /s/ route uses: "lisbon" -> "Lisbon",
// "abu dhabi, uae" -> "Abu_Dhabi".
func flatioLocationToken(location string) string {
	city := location
	if i := strings.IndexByte(location, ','); i >= 0 {
		city = location[:i]
	}
	city = strings.TrimSpace(city)
	if city == "" {
		return ""
	}
	fields := strings.Fields(city)
	for i, w := range fields {
		runes := []rune(strings.ToLower(w))
		if len(runes) > 0 {
			runes[0] = []rune(strings.ToUpper(string(runes[0])))[0]
		}
		fields[i] = string(runes)
	}
	return strings.Join(fields, "_")
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

// parseFlatioHTML finds the schema.org JSON-LD ItemList and maps each apartment
// to a HotelResult. Items without a positive price are skipped. It returns an
// error only when no ItemList block is present at all.
func parseFlatioHTML(html []byte, fallbackCurrency string) ([]models.HotelResult, error) {
	matches := flatioLDJSONRe.FindAllSubmatch(html, -1)
	if matches == nil {
		return nil, fmt.Errorf("no ld+json script found")
	}
	defCurrency := "EUR"
	if fallbackCurrency != "" {
		defCurrency = strings.ToUpper(fallbackCurrency)
	}
	var results []models.HotelResult
	foundList := false
	for _, m := range matches {
		var doc flatioLDDoc
		if err := json.Unmarshal(m[1], &doc); err != nil {
			continue
		}
		for _, node := range doc.Graph {
			if node.Type != "ItemList" {
				continue
			}
			foundList = true
			for _, li := range node.ItemListEle {
				h, ok := flatioItemToHotel(li.Item, defCurrency)
				if ok {
					results = append(results, h)
				}
			}
		}
	}
	if !foundList {
		return nil, fmt.Errorf("no ItemList in ld+json")
	}
	return results, nil
}

// flatioItemToHotel maps a single JSON-LD apartment item to a HotelResult.
func flatioItemToHotel(item flatioLDItem, defCurrency string) (models.HotelResult, bool) {
	price := parseDecimal(item.Offers.Price.String())
	if price <= 0 {
		return models.HotelResult{}, false
	}
	url := strings.TrimSpace(item.URL)
	if url == "" {
		url = strings.TrimSpace(item.Offers.URL)
	}
	id := ""
	if m := flatioIDRe.FindStringSubmatch(url); m != nil {
		id = m[1]
	}
	if id == "" {
		return models.HotelResult{}, false
	}
	currency := strings.ToUpper(strings.TrimSpace(item.Offers.PriceCurrency))
	if currency == "" {
		currency = defCurrency
	}
	name := strings.TrimSpace(item.Name)
	if name == "" {
		name = "Flatio apartment " + id
	}
	h := models.HotelResult{
		Name:         name,
		HotelID:      id,
		Price:        price,
		Currency:     currency,
		BookingURL:   url,
		PropertyType: "apartment",
		Description:  "Furnished apartment · monthly stay",
		ImageURL:     strings.TrimSpace(item.Image),
	}
	h.Sources = []models.PriceSource{{
		Provider:   "flatio",
		Price:      price,
		Currency:   currency,
		BookingURL: url,
	}}
	return h, true
}
