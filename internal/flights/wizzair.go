package flights

// Wizz Air flight provider.
//
// Like Ryanair, Wizz Air sells almost exclusively through its own channels and
// is omitted from Google Flights / GDS aggregation, so a meta-search misses its
// (frequently cheapest) Central/Eastern-European fares entirely. This provider
// calls Wizz Air's public, unauthenticated timetable endpoint directly:
//
//	POST https://be.wizzair.com/{version}/Api/search/timetable
//
// The response carries the operating flight number (e.g. "W6 2401"), local
// departure dates and the lead-in price, so results map cleanly to
// models.FlightResult and feed comparable all-in pricing (Wizz bag fees live in
// internal/baggage).
//
// VERSION ROTATION (verified gotcha): the {version} path segment rotates
// periodically (observed "10.1.0", "27.x", ...). It is NOT hardcoded into the
// URL builder. Resolution order (first hit wins):
//
//  1. WIZZAIR_API_VERSION env var (operator override, no code change / redeploy).
//  2. wizzVersion package var (overridable in tests; seeded from the const).
//
// wizzDefaultVersion is the last-known-good value. A clean live-discovery path
// (Wizz exposes its metadata via https://wizzair.com/.../metadata) is left as a
// documented TODO below because it cannot be verified offline; the env override
// gives operators a zero-deploy escape hatch in the meantime.
//
// Tracking: low-cost-carrier provider breadth (flights domain).

import (
	"bytes"
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

// wizzDefaultVersion is the last-known-good API version path segment. See the
// package comment: the segment rotates, so this is a fallback, not a contract.
//
// TODO(flights): add DiscoverWizzVersion(ctx) that fetches Wizz's published
// metadata endpoint and refreshes wizzVersion at runtime. Cannot be implemented
// + verified offline; WIZZAIR_API_VERSION env override covers operators today.
const wizzDefaultVersion = "10.1.0"

// wizzVersion is the active API version. Overridable in tests; the env var
// WIZZAIR_API_VERSION takes precedence at request time via wizzResolvedVersion.
var wizzVersion = wizzDefaultVersion

// wizzHost is overridable in tests (httptest server).
var wizzHost = "https" + "://" + "be.wizzair.com"

var (
	wizzLimiter = rate.NewLimiter(rate.Every(500*time.Millisecond), 1)
	wizzClient  = &http.Client{Timeout: 25 * time.Second}
)

// wizzResolvedVersion returns the API version to use, preferring the env
// override so operators can react to a rotation without a redeploy.
func wizzResolvedVersion() string {
	if v := strings.TrimSpace(os.Getenv("WIZZAIR_API_VERSION")); v != "" {
		return v
	}
	return wizzVersion
}

func wizzTimetableURL() string {
	return wizzHost + "/" + wizzResolvedVersion() + "/Api/search/timetable"
}

type wizzFlightLeg struct {
	DepartureStation string `json:"departureStation"`
	ArrivalStation   string `json:"arrivalStation"`
	From             string `json:"from"`
	To               string `json:"to"`
}

type wizzTimetableRequest struct {
	FlightList  []wizzFlightLeg `json:"flightList"`
	PriceType   string          `json:"priceType"`
	AdultCount  int             `json:"adultCount"`
	ChildCount  int             `json:"childCount"`
	InfantCount int             `json:"infantCount"`
}

type wizzPrice struct {
	Amount       float64 `json:"amount"`
	CurrencyCode string  `json:"currencyCode"`
}

type wizzFlight struct {
	DepartureStation string    `json:"departureStation"`
	ArrivalStation   string    `json:"arrivalStation"`
	DepartureDates   []string  `json:"departureDates"`
	Price            wizzPrice `json:"price"`
	PriceType        string    `json:"priceType"`
	FlightNumber     string    `json:"flightNumber"`
}

type wizzTimetableResponse struct {
	OutboundFlights []wizzFlight `json:"outboundFlights"`
	ReturnFlights   []wizzFlight `json:"returnFlights"`
}

// SearchWizzair queries Wizz Air's public timetable endpoint for one-way fares
// on the given route and date, returning canonical FlightResults tagged provider
// "wizzair" with carrier code "W6".
//
// The timetable endpoint returns the cheapest fare per day within a window. We
// request a single day (from == to == date) and keep only flights whose local
// departure date matches the requested date.
func SearchWizzair(ctx context.Context, origin, destination, date, currency string, opts SearchOptions) ([]models.FlightResult, error) {
	if currency == "" {
		currency = "EUR"
	}
	if err := wizzLimiter.Wait(ctx); err != nil {
		return nil, err
	}

	reqBody := wizzTimetableRequest{
		FlightList: []wizzFlightLeg{{
			DepartureStation: strings.ToUpper(origin),
			ArrivalStation:   strings.ToUpper(destination),
			From:             date,
			To:               date,
		}},
		PriceType:   "regular",
		AdultCount:  max(opts.Adults, 1),
		ChildCount:  0,
		InfantCount: 0,
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("wizzair: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, wizzTimetableURL(), bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("wizzair: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Origin", "https"+"://"+"wizzair.com")
	req.Header.Set("Referer", "https"+"://"+"wizzair.com/")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36")

	resp, err := wizzClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("wizzair: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		// A 404 here most likely means the version path segment rotated.
		if resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("wizzair: unexpected status 404 (API version %q may have rotated; set WIZZAIR_API_VERSION)", wizzResolvedVersion())
		}
		return nil, fmt.Errorf("wizzair: unexpected status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("wizzair: read body: %w", err)
	}

	var parsed wizzTimetableResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("wizzair: decode: %w", err)
	}

	results := make([]models.FlightResult, 0, len(parsed.OutboundFlights))
	for _, f := range parsed.OutboundFlights {
		mapped, ok := mapWizzFlight(f, date, currency)
		if ok {
			results = append(results, mapped)
		}
	}
	return results, nil
}

// mapWizzFlight converts a single Wizz timetable flight to a FlightResult,
// keeping only the departure date that matches the requested day. Returns
// ok=false when the flight has no departure on the requested date.
func mapWizzFlight(f wizzFlight, wantDate, fallbackCurrency string) (models.FlightResult, bool) {
	dep := wizzPickDeparture(f.DepartureDates, wantDate)
	if dep == "" {
		return models.FlightResult{}, false
	}
	cur := f.Price.CurrencyCode
	if cur == "" {
		cur = fallbackCurrency
	}
	leg := models.FlightLeg{
		DepartureAirport: models.AirportInfo{Code: strings.ToUpper(f.DepartureStation)},
		ArrivalAirport:   models.AirportInfo{Code: strings.ToUpper(f.ArrivalStation)},
		DepartureTime:    wizzDisplayTime(dep),
		Airline:          "Wizz Air",
		AirlineCode:      "W6",
		FlightNumber:     f.FlightNumber,
	}
	return models.FlightResult{
		Price:      f.Price.Amount,
		Currency:   cur,
		Stops:      0,
		Provider:   "wizzair",
		Legs:       []models.FlightLeg{leg},
		BookingURL: wizzBookingURL(f.DepartureStation, f.ArrivalStation, dep),
	}, true
}

// wizzTimeLayout is the local datetime Wizz returns (no zone).
const wizzTimeLayout = "2006-01-02T15:04:05"

// wizzPickDeparture returns the departureDates entry whose calendar day matches
// wantDate (YYYY-MM-DD). If wantDate is empty it returns the first entry. The
// timetable endpoint can return adjacent days, so this scoping is required.
func wizzPickDeparture(dates []string, wantDate string) string {
	for _, d := range dates {
		if wantDate == "" {
			return d
		}
		if strings.HasPrefix(d, wantDate) {
			return d
		}
	}
	if wantDate == "" && len(dates) > 0 {
		return dates[0]
	}
	return ""
}

func wizzDisplayTime(s string) string {
	if t, err := time.Parse(wizzTimeLayout, s); err == nil {
		return t.Format(flightTimeLayout)
	}
	// Already trimmed to the flight layout, or an unexpected format: pass through.
	if t, err := time.Parse(flightTimeLayout, s); err == nil {
		return t.Format(flightTimeLayout)
	}
	return s
}

func wizzBookingURL(origin, destination, departure string) string {
	day := departure
	if t, err := time.Parse(wizzTimeLayout, departure); err == nil {
		day = t.Format("2006-01-02")
	} else if t, err := time.Parse(flightTimeLayout, departure); err == nil {
		day = t.Format("2006-01-02")
	}
	q := url.Values{}
	q.Set("departureDate", day)
	q.Set("origin", strings.ToUpper(origin))
	q.Set("destination", strings.ToUpper(destination))
	q.Set("adultCount", "1")
	return "https" + "://" + "www.wizzair.com/en-gb/flights/select?" + q.Encode()
}
