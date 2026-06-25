package hotels

// Expedia hotels provider (opt-in, endpoint-gated).
//
// Expedia (https://www.expedia.com) is one of the largest OTAs and surfaces
// room-level nightly rates that a meta-search of Google Hotels / Booking /
// Agoda can miss — particularly Expedia-exclusive package and member rates.
//
// UNLIKE HomeToGo (public SSR JSON reachable from a plain client), Expedia's
// hotel search and availability surface is bot-walled for any static Go client.
// Verified 2026-06-25 against a Paris availability query (~30 days out, 2
// guests):
//
//	GET https://www.expedia.com/Hotel-Search?destination=Paris...   -> HTTP 429
//	GET https://www.expedia.com/                                     -> HTTP 429
//	POST https://www.expedia.com/graphql                             -> HTTP 429
//
// A plain client (curl / static Go `http.Client` with an ordinary User-Agent)
// receives HTTP 429 with `x-page-id: wildcard-challenge-handler`,
// `x-app-info: captcha-pwa`, `server: istio-envoy`, and the full Akamai Bot
// Manager cookie suite — `_abck` (with a `~-1~` unsolved-sensor token),
// `bm_ss`, `bm_s`, `bm_so`, `bm_sz` — plus an `akamai-request-bc` header. The
// only path that returned a real 200 in the probe was a browser-grade
// anti-bot-evasion fetcher; a static binary is challenged/blocked. There is no
// clean public unauthenticated JSON endpoint reachable from a static Go binary.
//
// Per the locked repo decisions (no browser/headless deps; honest typed status
// over fabricated data; "API-first with optional opt-ins") we do NOT ship a
// fragile scraper that races Akamai Bot Manager / the captcha challenge.
// Instead this provider mirrors the easyJet / Vueling opt-in pattern: it is a
// no-op skip unless the operator supplies a reachable availability base URL via
// EXPEDIA_API_BASE (e.g. an authorised partner / Rapid API endpoint, or a
// self-hosted reverse proxy that handles the bot challenge). When configured it
// maps an ASSUMED availability JSON shape to canonical HotelResults tagged
// provider "expedia".
//
// SCHEMA NOT VERIFIED: the public endpoint is bot-walled, so this parser is
// written against a representative/assumed shape, NOT confirmed against a live
// response. The field tags below may need adjustment once an operator supplies a
// reachable endpoint; the parser is intentionally tolerant (lead-in and
// room-level price encodings).
//
// When UNconfigured, SearchHotels surfaces an honest "skipped / not_configured"
// ProviderStatus carrying the AKAMAI_BLOCK root cause and a fix hint — never a
// crash, never a fabricated empty "no Expedia hotels" result.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"golang.org/x/time/rate"
)

// expediaDefaultHost is the canonical Expedia host used to build human booking
// deep links. Its hotel search / availability surface is Akamai Bot
// Manager-defended (HTTP 429 challenge for plain clients), so it is never
// queried for rates by default — the operator must point EXPEDIA_API_BASE at a
// reachable endpoint for the availability path.
const expediaDefaultHost = "https" + "://" + "www.expedia.com"

var (
	expediaLimiter = rate.NewLimiter(rate.Every(500*time.Millisecond), 1)
	expediaClient  = &http.Client{Timeout: 25 * time.Second}
)

// expediaEnabled mirrors the other auxiliary providers' test gate. It is set to
// false in TestMain so deterministic tests never fire real network calls; tests
// that exercise the configured path inject a mock server via EXPEDIA_API_BASE.
var expediaEnabled = true

// expediaAPIBase returns the operator-supplied Expedia availability base URL, or
// "". When empty the provider is a no-op (honest skip), because the public
// endpoint is bot-walled for static clients.
func expediaAPIBase() string {
	return strings.TrimSpace(os.Getenv("EXPEDIA_API_BASE"))
}

// expediaConfigured reports whether an operator has opted in by supplying a
// reachable availability base URL. Mirrors easyjetConfigured() / vuelingConfigured().
func expediaConfigured() bool {
	return expediaAPIBase() != ""
}

// expediaRate is one price encoding of a property's nightly rate.
type expediaRate struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currencyCode"`
}

// expediaProperty is a single bookable property in the availability response.
// The shape is an ASSUMED/representative Expedia availability payload — NOT
// verified against the live API (the endpoint is bot-walled). The parser is
// intentionally tolerant of the two common price encodings (a structured
// per-night rate, or a flat lead-in number).
type expediaProperty struct {
	ID           string      `json:"propertyId"`
	Name         string      `json:"name"`
	Address      string      `json:"address"`
	Neighborhood string      `json:"neighborhood"`
	Star         float64     `json:"starRating"`
	Rating       float64     `json:"guestRating"` // 0-10 scale
	ReviewCount  int         `json:"reviewCount"`
	Latitude     float64     `json:"latitude"`
	Longitude    float64     `json:"longitude"`
	ImageURL     string      `json:"imageUrl"`
	DeepLink     string      `json:"deepLink"`
	Amenities    []string    `json:"amenities"`
	NightlyRate  expediaRate `json:"nightlyRate"`  // structured per-night rate (room-level)
	LeadInPrice  float64     `json:"leadInPrice"`  // flat lead-in fallback
	Currency     string      `json:"currencyCode"` // currency for LeadInPrice
}

type expediaAvailabilityResponse struct {
	Properties []expediaProperty `json:"properties"`
}

// SearchExpedia queries an operator-configured Expedia availability endpoint for
// the given location and stay dates, returning canonical HotelResults tagged
// provider "expedia". It is a no-op (returns nil, nil) when disabled (test mode)
// or when no base URL is configured, mirroring the easyJet / Vueling opt-in
// pattern — the public Expedia endpoint is Akamai Bot Manager-defended and
// unreachable from a static client.
func SearchExpedia(ctx context.Context, location string, opts HotelSearchOptions) ([]models.HotelResult, error) {
	if !expediaEnabled {
		return nil, nil
	}
	base := expediaAPIBase()
	if base == "" {
		return nil, nil
	}
	if strings.TrimSpace(location) == "" {
		return nil, fmt.Errorf("expedia: location is required")
	}
	currency := strings.TrimSpace(opts.Currency)
	if currency == "" {
		currency = "USD"
	}
	if err := expediaLimiter.Wait(ctx); err != nil {
		return nil, err
	}

	q := url.Values{}
	q.Set("destination", location)
	q.Set("checkIn", opts.CheckIn)
	q.Set("checkOut", opts.CheckOut)
	q.Set("currency", currency)
	q.Set("adults", fmt.Sprintf("%d", max(opts.Guests, 1)))
	reqURL := strings.TrimRight(base, "/") + "/availability/query?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("expedia: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "trvl/1.0")

	resp, err := expediaClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("expedia: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusForbidden ||
		resp.StatusCode == http.StatusUnauthorized ||
		resp.StatusCode == http.StatusTooManyRequests {
		// The default public host is Akamai Bot Manager-defended; a 403/429 here
		// means the configured base is still serving a bot challenge, not real
		// JSON.
		return nil, fmt.Errorf("expedia: blocked (status %d) — EXPEDIA_API_BASE must reach the JSON availability API, not the bot-defended public site", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("expedia: unexpected status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("expedia: read body: %w", err)
	}

	hotels, err := parseExpediaProperties(body, currency)
	if err != nil {
		return nil, fmt.Errorf("expedia: decode: %w", err)
	}
	return hotels, nil
}

// parseExpediaProperties maps an Expedia availability JSON payload to
// HotelResults. Only price-bearing properties are mapped (zero-priced stubs are
// skipped) so every returned result is actually comparable. A structured
// per-night rate is treated as room-level/verified; a flat lead-in number is
// tagged lead_in/unverified so the price-trust sort never leads with an
// unverified headline over a real room rate.
func parseExpediaProperties(raw []byte, fallbackCurrency string) ([]models.HotelResult, error) {
	var resp expediaAvailabilityResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	results := make([]models.HotelResult, 0, len(resp.Properties))
	for _, p := range resp.Properties {
		price, currency, basis, confidence := expediaPrice(p, fallbackCurrency)
		if price <= 0 {
			continue // no comparable price -> skip
		}
		bookingURL := expediaBookingURL(p)
		h := models.HotelResult{
			Name:            strings.TrimSpace(p.Name),
			HotelID:         p.ID,
			Price:           price,
			Currency:        currency,
			Address:         strings.TrimSpace(p.Address),
			Neighborhood:    strings.TrimSpace(p.Neighborhood),
			Stars:           int(p.Star),
			Rating:          p.Rating,
			ReviewCount:     p.ReviewCount,
			Lat:             p.Latitude,
			Lon:             p.Longitude,
			ImageURL:        strings.TrimSpace(p.ImageURL),
			Amenities:       expediaAmenities(p.Amenities),
			BookingURL:      bookingURL,
			PriceBasis:      basis,
			PriceConfidence: confidence,
		}
		if h.Name == "" {
			h.Name = "Expedia property"
		}
		h.Sources = []models.PriceSource{{
			Provider:        "expedia",
			Price:           price,
			Currency:        currency,
			BookingURL:      bookingURL,
			PriceBasis:      basis,
			PriceConfidence: confidence,
		}}
		results = append(results, h)
	}
	return results, nil
}

// expediaPrice resolves the comparable price, currency, basis and confidence for
// a property. A structured per-night rate (NightlyRate.Amount) is room-level and
// verified; a flat lead-in number is unverified lead_in.
func expediaPrice(p expediaProperty, fallbackCurrency string) (price float64, currency, basis, confidence string) {
	if p.NightlyRate.Amount > 0 {
		return p.NightlyRate.Amount,
			firstNonEmptyExpedia(p.NightlyRate.Currency, fallbackCurrency),
			models.PriceBasisRoomNightly,
			models.PriceConfidenceRoomLevel
	}
	if p.LeadInPrice > 0 {
		return p.LeadInPrice,
			firstNonEmptyExpedia(p.Currency, fallbackCurrency),
			models.PriceBasisLeadIn,
			models.PriceConfidenceUnverified
	}
	return 0, firstNonEmptyExpedia(p.Currency, fallbackCurrency), "", ""
}

func firstNonEmptyExpedia(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return strings.ToUpper(s)
		}
	}
	return "USD"
}

// expediaAmenities trims and drops empty amenity labels.
func expediaAmenities(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, a := range in {
		if s := strings.TrimSpace(a); s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// expediaBookingURL returns the property deep link, falling back to the public
// host so a result is always bookable by a human.
func expediaBookingURL(p expediaProperty) string {
	link := strings.TrimSpace(p.DeepLink)
	switch {
	case link == "":
		return expediaDefaultHost
	case strings.HasPrefix(link, "http://"), strings.HasPrefix(link, "https://"):
		return link
	case strings.HasPrefix(link, "//"):
		return "https:" + link
	case strings.HasPrefix(link, "/"):
		return expediaDefaultHost + link
	default:
		return expediaDefaultHost + "/" + link
	}
}
