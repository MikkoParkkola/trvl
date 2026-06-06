package flights

// Ryanair flight provider.
//
// Ryanair excludes itself from Google Flights / GDS, so trvl's aggregator
// sources never surface it. This provider calls Ryanair's public Fare Finder
// endpoint (services-api.ryanair.com/farfnd/v4/oneWayFares) directly — no API
// key required — to recover the cheapest carrier a meta-search otherwise misses.
// The response carries the operating flight number (e.g. "FR9014"), exact times
// and price, so results map cleanly to models.FlightResult and feed the
// comparable-all-in pricing (Ryanair's bag fees live in internal/baggage).
//
// Tracking: MIK-4963.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"golang.org/x/time/rate"
)

const ryanairFarfndDefault = "https" + "://" + "services-api.ryanair.com/farfnd/v4/oneWayFares"

// ryanairBaseURL is overridable in tests.
var ryanairBaseURL = ryanairFarfndDefault

var (
	ryanairLimiter = rate.NewLimiter(rate.Every(500*time.Millisecond), 1)
	ryanairClient  = &http.Client{Timeout: 20 * time.Second}
)

type ryanairAirport struct {
	IataCode string `json:"iataCode"`
	Name     string `json:"name"`
}

type ryanairPrice struct {
	Value        float64 `json:"value"`
	CurrencyCode string  `json:"currencyCode"`
}

type ryanairSegment struct {
	DepartureAirport ryanairAirport `json:"departureAirport"`
	ArrivalAirport   ryanairAirport `json:"arrivalAirport"`
	DepartureDate    string         `json:"departureDate"`
	ArrivalDate      string         `json:"arrivalDate"`
	Price            ryanairPrice   `json:"price"`
	FlightNumber     string         `json:"flightNumber"`
}

type ryanairFare struct {
	Outbound ryanairSegment `json:"outbound"`
}

type ryanairFarfndResponse struct {
	Fares []ryanairFare `json:"fares"`
}

// SearchRyanair queries Ryanair's Fare Finder for nonstop fares on the given
// route and date, returning them as canonical FlightResults tagged provider
// "ryanair" with carrier code "FR".
func SearchRyanair(ctx context.Context, origin, destination, date, currency string, opts SearchOptions) ([]models.FlightResult, error) {
	if currency == "" {
		currency = "EUR"
	}
	if err := ryanairLimiter.Wait(ctx); err != nil {
		return nil, err
	}

	q := url.Values{}
	q.Set("departureAirportIataCode", strings.ToUpper(origin))
	q.Set("arrivalAirportIataCode", strings.ToUpper(destination))
	q.Set("outboundDepartureDateFrom", date)
	q.Set("outboundDepartureDateTo", date)
	q.Set("currency", currency)
	reqURL := ryanairBaseURL + "?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("ryanair: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; trvl/1.0)")

	resp, err := ryanairClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ryanair: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ryanair: unexpected status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("ryanair: read body: %w", err)
	}

	var parsed ryanairFarfndResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("ryanair: decode: %w", err)
	}

	results := make([]models.FlightResult, 0, len(parsed.Fares))
	for _, f := range parsed.Fares {
		results = append(results, mapRyanairFare(f.Outbound, currency))
	}
	return results, nil
}

func mapRyanairFare(s ryanairSegment, fallbackCurrency string) models.FlightResult {
	cur := s.Price.CurrencyCode
	if cur == "" {
		cur = fallbackCurrency
	}
	leg := models.FlightLeg{
		DepartureAirport: models.AirportInfo{Code: s.DepartureAirport.IataCode, Name: s.DepartureAirport.Name},
		ArrivalAirport:   models.AirportInfo{Code: s.ArrivalAirport.IataCode, Name: s.ArrivalAirport.Name},
		DepartureTime:    ryanairDisplayTime(s.DepartureDate),
		ArrivalTime:      ryanairDisplayTime(s.ArrivalDate),
		Duration:         ryanairDuration(s.DepartureDate, s.ArrivalDate),
		Airline:          "Ryanair",
		AirlineCode:      "FR",
		FlightNumber:     s.FlightNumber,
	}
	return models.FlightResult{
		Price:      s.Price.Value,
		Currency:   cur,
		Duration:   leg.Duration,
		Stops:      0,
		Provider:   "ryanair",
		Legs:       []models.FlightLeg{leg},
		BookingURL: ryanairBookingURL(s.DepartureAirport.IataCode, s.ArrivalAirport.IataCode, s.DepartureDate),
	}
}

// ryanairTimeLayout is the local datetime Ryanair returns (no zone).
const ryanairTimeLayout = "2006-01-02T15:04:05"

func ryanairDisplayTime(s string) string {
	if t, err := time.Parse(ryanairTimeLayout, s); err == nil {
		return t.Format("2006-01-02T15:04")
	}
	return s
}

func ryanairDuration(dep, arr string) int {
	dt, derr := time.Parse(ryanairTimeLayout, dep)
	at, aerr := time.Parse(ryanairTimeLayout, arr)
	if derr != nil || aerr != nil {
		return 0
	}
	mins := int(at.Sub(dt).Minutes())
	if mins < 0 {
		return 0
	}
	return mins
}

func ryanairBookingURL(origin, destination, departureDate string) string {
	day := departureDate
	if t, err := time.Parse(ryanairTimeLayout, departureDate); err == nil {
		day = t.Format("2006-01-02")
	}
	q := url.Values{}
	q.Set("adults", "1")
	q.Set("dateOut", day)
	q.Set("originIata", strings.ToUpper(origin))
	q.Set("destinationIata", strings.ToUpper(destination))
	q.Set("isReturn", "false")
	return "https" + "://" + "www.ryanair.com/gb/en/trip/flights/select?" + q.Encode()
}
