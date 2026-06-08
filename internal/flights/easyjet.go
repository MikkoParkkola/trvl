package flights

// easyJet flight provider (opt-in, endpoint-gated).
//
// easyJet (U2) is, like Ryanair and Wizz Air, an ultra-LCC that sells almost
// exclusively through its own channel and is omitted from Google Flights / GDS
// aggregation — so a meta-search misses its frequently-cheapest fares entirely.
//
// UNLIKE Ryanair (public Fare Finder JSON) and Wizz Air (public unauthenticated
// timetable JSON), easyJet's availability endpoint
//
//	GET https://www.easyjet.com/ejavailability/api/v75/availability/query?...
//
// is fronted by Akamai bot defence and returns HTTP 403 (text/html challenge)
// for any plain programmatic client — verified 2026-06-08 against AMS→BCN. There
// is NO clean public unauthenticated JSON endpoint reachable from a static Go
// binary without a headless/bot-evasion layer.
//
// Per the locked repo decisions (no browser/headless deps; honest typed status
// over fabricated data; "API-first with optional opt-ins") we do NOT ship a
// fragile scraper that races Akamai. Instead this provider mirrors the Transavia
// / AFKLM opt-in pattern: it is a no-op skip unless the operator supplies a
// reachable availability base URL via EASYJET_API_BASE (e.g. an authorised
// partner endpoint or a self-hosted reverse proxy that handles the bot
// challenge). When configured it maps an ASSUMED availability JSON shape to
// canonical FlightResults tagged provider "easyjet" with carrier code "U2".
//
// SCHEMA NOT VERIFIED: the public endpoint is Akamai-blocked, so this parser is
// written against a representative/assumed shape, NOT confirmed against a live
// response. The field tags below may need adjustment once an operator supplies a
// reachable endpoint; the parser is intentionally tolerant (two price encodings).
//
// When UNconfigured, searchFlightsCore surfaces an honest "skipped /
// not_configured" ProviderStatus carrying the AKAMAI_BLOCK root cause and a fix
// hint — never a crash, never a fabricated empty "no easyJet flights" result.
//
// Tracking: MIK-4963.

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

// easyjetDefaultHost is the canonical easyJet host used to build human booking
// deep links. Its availability API is Akamai bot-defended (403 for plain
// clients), so it is never queried for fares by default — the operator must
// point EASYJET_API_BASE at a reachable endpoint for the fare-search path.
const easyjetDefaultHost = "https" + "://" + "www.easyjet.com"

var (
	easyjetLimiter = rate.NewLimiter(rate.Every(500*time.Millisecond), 1)
	easyjetClient  = &http.Client{Timeout: 25 * time.Second}
)

// easyjetAPIBase returns the operator-supplied easyJet availability base URL, or
// "". When empty the provider is a no-op (honest skip), because the public
// endpoint is Akamai-blocked for static clients.
func easyjetAPIBase() string {
	return strings.TrimSpace(os.Getenv("EASYJET_API_BASE"))
}

// easyjetConfigured reports whether an operator has opted in by supplying a
// reachable availability base URL. Mirrors transaviaConfigured().
func easyjetConfigured() bool {
	return easyjetAPIBase() != ""
}

// easyjetFare is the lead-in price of an availability slice.
type easyjetFare struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currencyCode"`
}

// easyjetFlight is a single bookable flight in the availability response. The
// shape is an ASSUMED/representative easyJet ejavailability payload — NOT
// verified against the live API (the endpoint is Akamai-blocked). The parser is
// intentionally tolerant of the two common price encodings.
type easyjetFlight struct {
	FlightNumber  string      `json:"flightNumber"`
	DepartureIata string      `json:"departureAirport"`
	ArrivalIata   string      `json:"arrivalAirport"`
	Departure     string      `json:"departureDateTime"`
	Arrival       string      `json:"arrivalDateTime"`
	Fare          easyjetFare `json:"fare"`
	LowestFare    float64     `json:"lowestFare"`
	Currency      string      `json:"currencyCode"`
}

type easyjetAvailabilityResponse struct {
	Flights []easyjetFlight `json:"flights"`
}

// SearchEasyjet queries an operator-configured easyJet availability endpoint for
// one-way fares on the given route and date, returning canonical FlightResults
// tagged provider "easyjet". It is a no-op (returns nil, nil) when no base URL is
// configured, mirroring the Transavia opt-in pattern — the public easyJet
// endpoint is Akamai bot-defended and unreachable from a static client.
func SearchEasyjet(ctx context.Context, origin, destination, date, currency string, opts SearchOptions) ([]models.FlightResult, error) {
	base := easyjetAPIBase()
	if base == "" {
		return nil, nil
	}
	if currency == "" {
		currency = "EUR"
	}
	if err := easyjetLimiter.Wait(ctx); err != nil {
		return nil, err
	}

	q := url.Values{}
	q.Set("DepartureIata", strings.ToUpper(origin))
	q.Set("ArrivalIata", strings.ToUpper(destination))
	q.Set("DepartureDateFrom", date)
	q.Set("DepartureDateTo", date)
	q.Set("Currency", currency)
	q.Set("NumberOfAdults", fmt.Sprintf("%d", max(opts.Adults, 1)))
	reqURL := strings.TrimRight(base, "/") + "/ejavailability/api/v75/availability/query?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("easyjet: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "trvl/1.0")

	resp, err := easyjetClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("easyjet: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		// The default public host is Akamai-defended; a 403 here means the
		// configured base is still serving a bot challenge, not real JSON.
		return nil, fmt.Errorf("easyjet: blocked (status %d) — EASYJET_API_BASE must reach the JSON availability API, not the Akamai-defended public site", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("easyjet: unexpected status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("easyjet: read body: %w", err)
	}

	var parsed easyjetAvailabilityResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("easyjet: decode: %w", err)
	}

	results := make([]models.FlightResult, 0, len(parsed.Flights))
	for _, f := range parsed.Flights {
		if mapped, ok := mapEasyjetFlight(f, date, currency); ok {
			results = append(results, mapped)
		}
	}
	return results, nil
}

// easyjetTimeLayout is the local datetime easyJet returns (no zone).
const easyjetTimeLayout = "2006-01-02T15:04:05"

// mapEasyjetFlight converts a single availability flight to a FlightResult,
// keeping only flights whose local departure day matches wantDate. Returns
// ok=false for off-date flights or zero-priced placeholders.
func mapEasyjetFlight(f easyjetFlight, wantDate, fallbackCurrency string) (models.FlightResult, bool) {
	if wantDate != "" && !strings.HasPrefix(f.Departure, wantDate) {
		return models.FlightResult{}, false
	}
	price, cur := easyjetPrice(f, fallbackCurrency)
	if price <= 0 {
		return models.FlightResult{}, false
	}
	flightNo := strings.TrimSpace(f.FlightNumber)
	if flightNo != "" && !strings.HasPrefix(strings.ToUpper(flightNo), "U2") {
		flightNo = "U2" + flightNo
	}
	leg := models.FlightLeg{
		DepartureAirport: models.AirportInfo{Code: strings.ToUpper(f.DepartureIata)},
		ArrivalAirport:   models.AirportInfo{Code: strings.ToUpper(f.ArrivalIata)},
		DepartureTime:    easyjetDisplayTime(f.Departure),
		ArrivalTime:      easyjetDisplayTime(f.Arrival),
		Duration:         easyjetDuration(f.Departure, f.Arrival),
		Airline:          "easyJet",
		AirlineCode:      "U2",
		FlightNumber:     flightNo,
	}
	return models.FlightResult{
		Price:      price,
		Currency:   cur,
		Duration:   leg.Duration,
		Stops:      0,
		Provider:   "easyjet",
		Legs:       []models.FlightLeg{leg},
		BookingURL: easyjetBookingURL(f.DepartureIata, f.ArrivalIata, f.Departure),
	}, true
}

func easyjetPrice(f easyjetFlight, fallbackCurrency string) (float64, string) {
	if f.Fare.Amount > 0 {
		return f.Fare.Amount, firstNonEmpty(f.Fare.Currency, fallbackCurrency)
	}
	return f.LowestFare, firstNonEmpty(f.Currency, fallbackCurrency)
}

func easyjetDisplayTime(s string) string {
	if t, err := time.Parse(easyjetTimeLayout, s); err == nil {
		return t.Format(flightTimeLayout)
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Format(flightTimeLayout)
	}
	return s
}

func easyjetDuration(dep, arr string) int {
	dt, derr := easyjetParse(dep)
	at, aerr := easyjetParse(arr)
	if derr != nil || aerr != nil {
		return 0
	}
	mins := int(at.Sub(dt).Minutes())
	if mins < 0 {
		return 0
	}
	return mins
}

func easyjetParse(s string) (time.Time, error) {
	if t, err := time.Parse(easyjetTimeLayout, s); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, s)
}

func easyjetBookingURL(origin, destination, departure string) string {
	day := departure
	if t, err := easyjetParse(departure); err == nil {
		day = t.Format("2006-01-02")
	}
	q := url.Values{}
	q.Set("origin", strings.ToUpper(origin))
	q.Set("destination", strings.ToUpper(destination))
	q.Set("dateofdeparture", day)
	q.Set("adults", "1")
	return easyjetDefaultHost + "/en/buy/flights?" + q.Encode()
}
