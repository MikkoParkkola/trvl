package flights

// Norwegian Air (DY) flight provider (opt-in, endpoint-gated).
//
// Norwegian Air Shuttle (DY) is, like Ryanair, Wizz Air, easyJet and Vueling, a
// low-cost carrier that sells largely through its own channel and is omitted or
// under-represented in Google Flights / GDS aggregation — so a meta-search
// misses its frequently-cheapest Nordic / intra-European short-haul fares.
//
// UNLIKE Ryanair (public Fare Finder JSON) and Wizz Air (public unauthenticated
// timetable JSON), Norwegian Air has NO clean public unauthenticated JSON
// availability endpoint reachable from a static Go binary. Its site and booking
// funnel sit behind Cloudflare's bot-management challenge:
//
//	GET https://www.norwegian.com/... (and historic fare/availability API paths)
//
// is served by `server: cloudflare` and, for any plain programmatic client,
// returns HTTP 403 with `cf-mitigated: challenge`, an "Are you human? | Norwegian"
// interstitial HTML body, and a `__cf_bm` bot-management cookie — verified
// 2026-06-25 against OSL→LGW ~30 days out from a static client. Every probed
// path (fare-calendar, flydata/availability, booking/search/availability,
// booking.norwegian.com, and the site root itself) returned the same Cloudflare
// 403 challenge. A browser-grade fetcher with residential cookies reaches the
// host, but the repo forbids browser/headless dependencies and the data is NOT
// reachable from curl / Go's net/http — so there is no clean static-client path.
//
// Per the locked repo decisions (no browser/headless deps; honest typed status
// over fabricated data; "API-first with optional opt-ins") we do NOT ship a
// fragile scraper that races Cloudflare Bot Management. Instead this provider
// mirrors the easyJet / Vueling opt-in pattern: it is a no-op skip unless the
// operator supplies a reachable availability base URL via NORWEGIAN_API_BASE
// (e.g. an authorised partner endpoint or a self-hosted reverse proxy that
// handles the bot challenge). When configured it maps an ASSUMED availability
// JSON shape to canonical FlightResults tagged provider "norwegian" with carrier
// code "DY".
//
// SCHEMA NOT VERIFIED: the public endpoint is Cloudflare-blocked, so this parser
// is written against a representative/assumed shape, NOT confirmed against a live
// response. The field tags below may need adjustment once an operator supplies a
// reachable endpoint; the parser is intentionally tolerant (two price encodings),
// matching the easyJet / Vueling adapters.
//
// When UNconfigured, searchFlightsCore surfaces an honest "skipped /
// not_configured" ProviderStatus carrying the CLOUDFLARE_BLOCK root cause and a
// fix hint — never a crash, never a fabricated empty "no Norwegian flights"
// result.

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

// norwegianDefaultHost is the canonical Norwegian host used to build human
// booking deep links. Its site/booking funnel is Cloudflare bot-defended (403
// `cf-mitigated: challenge` for plain clients), so it is never queried for fares
// by default — the operator must point NORWEGIAN_API_BASE at a reachable
// endpoint for the fare-search path.
const norwegianDefaultHost = "https" + "://" + "www.norwegian.com"

var (
	norwegianLimiter = rate.NewLimiter(rate.Every(500*time.Millisecond), 1)
	norwegianClient  = &http.Client{Timeout: 25 * time.Second}
)

// norwegianAPIBase returns the operator-supplied Norwegian availability base
// URL, or "". When empty the provider is a no-op (honest skip), because the
// public endpoint is Cloudflare-blocked for static clients.
func norwegianAPIBase() string {
	return strings.TrimSpace(os.Getenv("NORWEGIAN_API_BASE"))
}

// norwegianConfigured reports whether an operator has opted in by supplying a
// reachable availability base URL. Mirrors easyjetConfigured().
func norwegianConfigured() bool {
	return norwegianAPIBase() != ""
}

// norwegianFare is the lead-in price of an availability slice.
type norwegianFare struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currencyCode"`
}

// norwegianFlight is a single bookable flight in the availability response. The
// shape is an ASSUMED/representative Norwegian availability payload — NOT
// verified against the live API (the endpoint is Cloudflare-blocked). The parser
// is intentionally tolerant of the two common price encodings.
type norwegianFlight struct {
	FlightNumber  string        `json:"flightNumber"`
	DepartureIata string        `json:"departureAirport"`
	ArrivalIata   string        `json:"arrivalAirport"`
	Departure     string        `json:"departureDateTime"`
	Arrival       string        `json:"arrivalDateTime"`
	Fare          norwegianFare `json:"fare"`
	LowestFare    float64       `json:"lowestFare"`
	Currency      string        `json:"currencyCode"`
}

type norwegianAvailabilityResponse struct {
	Flights []norwegianFlight `json:"flights"`
}

// SearchNorwegian queries an operator-configured Norwegian availability endpoint
// for one-way fares on the given route and date, returning canonical
// FlightResults tagged provider "norwegian". It is a no-op (returns nil, nil)
// when no base URL is configured, mirroring the easyJet / Vueling opt-in
// pattern — the public Norwegian endpoint is Cloudflare bot-defended and
// unreachable from a static client.
func SearchNorwegian(ctx context.Context, origin, destination, date, currency string, opts SearchOptions) ([]models.FlightResult, error) {
	base := norwegianAPIBase()
	if base == "" {
		return nil, nil
	}
	if currency == "" {
		currency = "EUR"
	}
	if err := norwegianLimiter.Wait(ctx); err != nil {
		return nil, err
	}

	q := url.Values{}
	q.Set("DepartureIata", strings.ToUpper(origin))
	q.Set("ArrivalIata", strings.ToUpper(destination))
	q.Set("DepartureDateFrom", date)
	q.Set("DepartureDateTo", date)
	q.Set("Currency", currency)
	q.Set("NumberOfAdults", fmt.Sprintf("%d", max(opts.Adults, 1)))
	reqURL := strings.TrimRight(base, "/") + "/availability/query?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("norwegian: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "trvl/1.0")

	resp, err := norwegianClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("norwegian: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		// The default public host is Cloudflare bot-defended; a 403 here means
		// the configured base is still serving a bot challenge, not real JSON.
		return nil, fmt.Errorf("norwegian: blocked (status %d) — NORWEGIAN_API_BASE must reach the JSON availability API, not the Cloudflare-defended public site", resp.StatusCode)
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("norwegian: rate-limited (status %d)", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("norwegian: unexpected status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("norwegian: read body: %w", err)
	}

	var parsed norwegianAvailabilityResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("norwegian: decode: %w", err)
	}

	results := make([]models.FlightResult, 0, len(parsed.Flights))
	for _, f := range parsed.Flights {
		if mapped, ok := mapNorwegianFlight(f, date, currency); ok {
			results = append(results, mapped)
		}
	}
	return results, nil
}

// norwegianTimeLayout is the local datetime Norwegian returns (no zone).
const norwegianTimeLayout = "2006-01-02T15:04:05"

// mapNorwegianFlight converts a single availability flight to a FlightResult,
// keeping only flights whose local departure day matches wantDate. Returns
// ok=false for off-date flights or zero-priced placeholders.
func mapNorwegianFlight(f norwegianFlight, wantDate, fallbackCurrency string) (models.FlightResult, bool) {
	if wantDate != "" && !strings.HasPrefix(f.Departure, wantDate) {
		return models.FlightResult{}, false
	}
	price, cur := norwegianPrice(f, fallbackCurrency)
	if price <= 0 {
		return models.FlightResult{}, false
	}
	flightNo := strings.TrimSpace(f.FlightNumber)
	if flightNo != "" && !strings.HasPrefix(strings.ToUpper(flightNo), "DY") {
		flightNo = "DY" + flightNo
	}
	leg := models.FlightLeg{
		DepartureAirport: models.AirportInfo{Code: strings.ToUpper(f.DepartureIata)},
		ArrivalAirport:   models.AirportInfo{Code: strings.ToUpper(f.ArrivalIata)},
		DepartureTime:    norwegianDisplayTime(f.Departure),
		ArrivalTime:      norwegianDisplayTime(f.Arrival),
		Duration:         norwegianDuration(f.Departure, f.Arrival),
		Airline:          "Norwegian",
		AirlineCode:      "DY",
		FlightNumber:     flightNo,
	}
	return models.FlightResult{
		Price:      price,
		Currency:   cur,
		Duration:   leg.Duration,
		Stops:      0,
		Provider:   "norwegian",
		Legs:       []models.FlightLeg{leg},
		BookingURL: norwegianBookingURL(f.DepartureIata, f.ArrivalIata, f.Departure),
	}, true
}

func norwegianPrice(f norwegianFlight, fallbackCurrency string) (float64, string) {
	if f.Fare.Amount > 0 {
		return f.Fare.Amount, firstNonEmpty(f.Fare.Currency, fallbackCurrency)
	}
	return f.LowestFare, firstNonEmpty(f.Currency, fallbackCurrency)
}

func norwegianDisplayTime(s string) string {
	if t, err := time.Parse(norwegianTimeLayout, s); err == nil {
		return t.Format(flightTimeLayout)
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Format(flightTimeLayout)
	}
	return s
}

func norwegianDuration(dep, arr string) int {
	dt, derr := norwegianParse(dep)
	at, aerr := norwegianParse(arr)
	if derr != nil || aerr != nil {
		return 0
	}
	mins := int(at.Sub(dt).Minutes())
	if mins < 0 {
		return 0
	}
	return mins
}

func norwegianParse(s string) (time.Time, error) {
	if t, err := time.Parse(norwegianTimeLayout, s); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, s)
}

func norwegianBookingURL(origin, destination, departure string) string {
	day := departure
	if t, err := norwegianParse(departure); err == nil {
		day = t.Format("2006-01-02")
	}
	q := url.Values{}
	q.Set("origin", strings.ToUpper(origin))
	q.Set("destination", strings.ToUpper(destination))
	q.Set("outboundDate", day)
	q.Set("adults", "1")
	return norwegianDefaultHost + "/uk/booking/flight-tickets/?" + q.Encode()
}
