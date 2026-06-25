package flights

// Vueling flight provider (opt-in, endpoint-gated).
//
// Vueling (VY) is, like Ryanair, Wizz Air and easyJet, an ultra-LCC that sells
// almost exclusively through its own channel and is omitted from Google Flights
// / GDS aggregation — so a meta-search misses its frequently-cheapest
// short-haul fares (a large Barcelona-hub network) entirely.
//
// UNLIKE Ryanair (public Fare Finder JSON) and Wizz Air (public unauthenticated
// timetable JSON), Vueling has NO clean public unauthenticated JSON availability
// endpoint reachable from a static Go binary. Its booking funnel is an Akamai
// Bot Manager-defended SPA:
//
//	GET https://tickets.vueling.com/  (the New Skies booking/availability engine)
//
// is served by AkamaiGHost and sets the full Akamai Bot Manager cookie suite —
// _abck (with a ~-1~ unsolved-sensor token), bm_ss, bm_s, bm_so, bm_sz and
// akacd_dc_tickets — verified 2026-06-25 against an AMS→BCN one-way ~30 days
// out. The public www.vueling.com host likewise carries x-akamai-transformed
// and bm_ss. Any availability request lacking a valid Akamai sensor payload
// (which is generated only by executing Akamai's obfuscated JS in a real
// browser) is challenged or blocked. There is no clean static-client path.
//
// Per the locked repo decisions (no browser/headless deps; honest typed status
// over fabricated data; "API-first with optional opt-ins") we do NOT ship a
// fragile scraper that races Akamai Bot Manager. Instead this provider mirrors
// the easyJet / Transavia opt-in pattern: it is a no-op skip unless the operator
// supplies a reachable availability base URL via VUELING_API_BASE (e.g. an
// authorised partner endpoint or a self-hosted reverse proxy that handles the
// bot challenge). When configured it maps an ASSUMED availability JSON shape to
// canonical FlightResults tagged provider "vueling" with carrier code "VY".
//
// SCHEMA NOT VERIFIED: the public endpoint is Akamai-blocked, so this parser is
// written against a representative/assumed shape, NOT confirmed against a live
// response. The field tags below may need adjustment once an operator supplies a
// reachable endpoint; the parser is intentionally tolerant (two price encodings),
// matching the easyJet adapter.
//
// When UNconfigured, searchFlightsCore surfaces an honest "skipped /
// not_configured" ProviderStatus carrying the AKAMAI_BLOCK root cause and a fix
// hint — never a crash, never a fabricated empty "no Vueling flights" result.

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

// vuelingDefaultHost is the canonical Vueling host used to build human booking
// deep links. Its booking/availability engine is Akamai Bot Manager-defended
// (challenge for plain clients), so it is never queried for fares by default —
// the operator must point VUELING_API_BASE at a reachable endpoint for the
// fare-search path.
const vuelingDefaultHost = "https" + "://" + "www.vueling.com"

var (
	vuelingLimiter = rate.NewLimiter(rate.Every(500*time.Millisecond), 1)
	vuelingClient  = &http.Client{Timeout: 25 * time.Second}
)

// vuelingAPIBase returns the operator-supplied Vueling availability base URL, or
// "". When empty the provider is a no-op (honest skip), because the public
// endpoint is Akamai Bot Manager-defended for static clients.
func vuelingAPIBase() string {
	return strings.TrimSpace(os.Getenv("VUELING_API_BASE"))
}

// vuelingConfigured reports whether an operator has opted in by supplying a
// reachable availability base URL. Mirrors easyjetConfigured().
func vuelingConfigured() bool {
	return vuelingAPIBase() != ""
}

// vuelingFare is the lead-in price of an availability slice.
type vuelingFare struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currencyCode"`
}

// vuelingFlight is a single bookable flight in the availability response. The
// shape is an ASSUMED/representative Vueling availability payload — NOT verified
// against the live API (the endpoint is Akamai-blocked). The parser is
// intentionally tolerant of the two common price encodings.
type vuelingFlight struct {
	FlightNumber  string      `json:"flightNumber"`
	DepartureIata string      `json:"departureAirport"`
	ArrivalIata   string      `json:"arrivalAirport"`
	Departure     string      `json:"departureDateTime"`
	Arrival       string      `json:"arrivalDateTime"`
	Fare          vuelingFare `json:"fare"`
	LowestFare    float64     `json:"lowestFare"`
	Currency      string      `json:"currencyCode"`
}

type vuelingAvailabilityResponse struct {
	Flights []vuelingFlight `json:"flights"`
}

// SearchVueling queries an operator-configured Vueling availability endpoint for
// one-way fares on the given route and date, returning canonical FlightResults
// tagged provider "vueling". It is a no-op (returns nil, nil) when no base URL is
// configured, mirroring the easyJet opt-in pattern — the public Vueling endpoint
// is Akamai Bot Manager-defended and unreachable from a static client.
func SearchVueling(ctx context.Context, origin, destination, date, currency string, opts SearchOptions) ([]models.FlightResult, error) {
	base := vuelingAPIBase()
	if base == "" {
		return nil, nil
	}
	if currency == "" {
		currency = "EUR"
	}
	if err := vuelingLimiter.Wait(ctx); err != nil {
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
		return nil, fmt.Errorf("vueling: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "trvl/1.0")

	resp, err := vuelingClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("vueling: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		// The default public host is Akamai Bot Manager-defended; a 403 here
		// means the configured base is still serving a bot challenge, not real
		// JSON.
		return nil, fmt.Errorf("vueling: blocked (status %d) — VUELING_API_BASE must reach the JSON availability API, not the Akamai-defended public site", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("vueling: unexpected status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("vueling: read body: %w", err)
	}

	var parsed vuelingAvailabilityResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("vueling: decode: %w", err)
	}

	results := make([]models.FlightResult, 0, len(parsed.Flights))
	for _, f := range parsed.Flights {
		if mapped, ok := mapVuelingFlight(f, date, currency); ok {
			results = append(results, mapped)
		}
	}
	return results, nil
}

// vuelingTimeLayout is the local datetime Vueling returns (no zone).
const vuelingTimeLayout = "2006-01-02T15:04:05"

// mapVuelingFlight converts a single availability flight to a FlightResult,
// keeping only flights whose local departure day matches wantDate. Returns
// ok=false for off-date flights or zero-priced placeholders.
func mapVuelingFlight(f vuelingFlight, wantDate, fallbackCurrency string) (models.FlightResult, bool) {
	if wantDate != "" && !strings.HasPrefix(f.Departure, wantDate) {
		return models.FlightResult{}, false
	}
	price, cur := vuelingPrice(f, fallbackCurrency)
	if price <= 0 {
		return models.FlightResult{}, false
	}
	flightNo := strings.TrimSpace(f.FlightNumber)
	if flightNo != "" && !strings.HasPrefix(strings.ToUpper(flightNo), "VY") {
		flightNo = "VY" + flightNo
	}
	leg := models.FlightLeg{
		DepartureAirport: models.AirportInfo{Code: strings.ToUpper(f.DepartureIata)},
		ArrivalAirport:   models.AirportInfo{Code: strings.ToUpper(f.ArrivalIata)},
		DepartureTime:    vuelingDisplayTime(f.Departure),
		ArrivalTime:      vuelingDisplayTime(f.Arrival),
		Duration:         vuelingDuration(f.Departure, f.Arrival),
		Airline:          "Vueling",
		AirlineCode:      "VY",
		FlightNumber:     flightNo,
	}
	return models.FlightResult{
		Price:      price,
		Currency:   cur,
		Duration:   leg.Duration,
		Stops:      0,
		Provider:   "vueling",
		Legs:       []models.FlightLeg{leg},
		BookingURL: vuelingBookingURL(f.DepartureIata, f.ArrivalIata, f.Departure),
	}, true
}

func vuelingPrice(f vuelingFlight, fallbackCurrency string) (float64, string) {
	if f.Fare.Amount > 0 {
		return f.Fare.Amount, firstNonEmpty(f.Fare.Currency, fallbackCurrency)
	}
	return f.LowestFare, firstNonEmpty(f.Currency, fallbackCurrency)
}

func vuelingDisplayTime(s string) string {
	if t, err := time.Parse(vuelingTimeLayout, s); err == nil {
		return t.Format(flightTimeLayout)
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Format(flightTimeLayout)
	}
	return s
}

func vuelingDuration(dep, arr string) int {
	dt, derr := vuelingParse(dep)
	at, aerr := vuelingParse(arr)
	if derr != nil || aerr != nil {
		return 0
	}
	mins := int(at.Sub(dt).Minutes())
	if mins < 0 {
		return 0
	}
	return mins
}

func vuelingParse(s string) (time.Time, error) {
	if t, err := time.Parse(vuelingTimeLayout, s); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, s)
}

func vuelingBookingURL(origin, destination, departure string) string {
	day := departure
	if t, err := vuelingParse(departure); err == nil {
		day = t.Format("2006-01-02")
	}
	q := url.Values{}
	q.Set("origin", strings.ToUpper(origin))
	q.Set("destination", strings.ToUpper(destination))
	q.Set("departureDate", day)
	q.Set("adults", "1")
	return vuelingDefaultHost + "/en/book-flights?" + q.Encode()
}
