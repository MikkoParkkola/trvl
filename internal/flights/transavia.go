package flights

// Transavia flight provider (opt-in, API-key gated).
//
// Transavia (HV / Transavia France TO) is a KLM-owned low-cost carrier that, like
// Ryanair and Wizz Air, is thinly represented in GDS/meta aggregation. Unlike
// those two it exposes an OFFICIAL public API (developer.transavia.com) that
// requires a FREE developer API key. This provider therefore mirrors the AFKLM
// opt-in pattern: it is a no-op skip when no key is configured and only fires
// when the operator supplies one.
//
// Endpoint (Flight Offers — "cheapest flight offers" search):
//
//	GET https://api.transavia.com/v1/flightoffers/
//	    ?origin=AMS&destination=BCN&originDepartureDate=20260707
//	    &adultCount=1&directFlight=true
//	Header: apikey: <TRANSAVIA_API_KEY>
//
// The response carries an array of priced offers; each offer's outbound flight
// list exposes the operating flight number, marketing airline, equipment and
// scheduled local times. We map the cheapest direct offers to FlightResult.
//
// LIVE VERIFICATION PENDING: implemented against the documented/representative
// JSON shape (fixture-tested). It has NOT been verified against the live API
// because that requires a developer key. The wire shape may need a field tweak
// once a key is available — the parser is intentionally tolerant.
//
// Tracking: low-cost-carrier provider breadth (flights domain).

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

// transaviaHost is overridable in tests (httptest server).
var transaviaHost = "https" + "://" + "api.transavia.com"

var (
	transaviaLimiter = rate.NewLimiter(rate.Every(500*time.Millisecond), 1)
	transaviaClient  = &http.Client{Timeout: 25 * time.Second}
)

// transaviaAPIKey returns the configured Transavia developer API key, or "".
func transaviaAPIKey() string {
	return strings.TrimSpace(os.Getenv("TRANSAVIA_API_KEY"))
}

// transaviaConfigured reports whether a Transavia API key is present. Mirrors
// afklm.Configured(): provider silently skips when unconfigured.
func transaviaConfigured() bool {
	return transaviaAPIKey() != ""
}

// transaviaPrice is the priced total of an offer.
type transaviaPrice struct {
	Total    float64 `json:"total"`
	Currency string  `json:"currency"`
}

// transaviaFlight is a single operating segment within an offer.
type transaviaFlight struct {
	FlightNumber    int    `json:"flightNumber"`
	Origin          string `json:"origin"`
	Destination     string `json:"destination"`
	DepartureDate   string `json:"departureDateTime"`
	ArrivalDate     string `json:"arrivalDateTime"`
	MarketingFlight struct {
		CarrierCode string `json:"carrierCode"`
		Number      int    `json:"number"`
	} `json:"marketingFlight"`
	Equipment string `json:"aircraftType"`
}

// transaviaOffer is one priced itinerary in the offers response.
type transaviaOffer struct {
	OutboundFlight  transaviaFlight   `json:"outboundFlight"`
	OutboundFlights []transaviaFlight `json:"outboundFlights"`
	Price           transaviaPrice    `json:"price"`
	PricingInfo     struct {
		TotalPriceAllPassengers float64 `json:"totalPriceAllPassengers"`
		TotalPriceOnePassenger  float64 `json:"totalPriceOnePassenger"`
		Currency                string  `json:"currency"`
	} `json:"pricingInfoSummary"`
}

type transaviaOffersResponse struct {
	FlightOffer []transaviaOffer `json:"flightOffer"`
}

// SearchTransavia queries Transavia's Flight Offers API for one-way fares on the
// given route and date, returning canonical FlightResults tagged provider
// "transavia". It is a no-op (returns nil, nil) when no API key is configured,
// mirroring the AFKLM opt-in pattern.
func SearchTransavia(ctx context.Context, origin, destination, date, currency string, opts SearchOptions) ([]models.FlightResult, error) {
	key := transaviaAPIKey()
	if key == "" {
		return nil, nil
	}
	if currency == "" {
		currency = "EUR"
	}
	if err := transaviaLimiter.Wait(ctx); err != nil {
		return nil, err
	}

	depDate, ok := models.FormatProviderDate(date, models.DateLayoutCompact)
	if !ok {
		return nil, fmt.Errorf("transavia: invalid date %q", date)
	}

	q := url.Values{}
	q.Set("origin", strings.ToUpper(origin))
	q.Set("destination", strings.ToUpper(destination))
	q.Set("originDepartureDate", depDate)
	q.Set("adultCount", fmt.Sprintf("%d", max(opts.Adults, 1)))
	q.Set("directFlight", "true")
	reqURL := transaviaHost + "/v1/flightoffers/?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("transavia: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("apikey", key)
	req.Header.Set("User-Agent", "trvl/1.0")

	resp, err := transaviaClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("transavia: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("transavia: unauthorized (status %d) — check TRANSAVIA_API_KEY", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("transavia: unexpected status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("transavia: read body: %w", err)
	}

	var parsed transaviaOffersResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("transavia: decode: %w", err)
	}

	results := make([]models.FlightResult, 0, len(parsed.FlightOffer))
	for _, offer := range parsed.FlightOffer {
		if mapped, ok := mapTransaviaOffer(offer, currency); ok {
			results = append(results, mapped)
		}
	}
	return results, nil
}

// transaviaTimeLayout is the local datetime Transavia returns (no zone).
const transaviaTimeLayout = "2006-01-02T15:04:05"

func mapTransaviaOffer(offer transaviaOffer, fallbackCurrency string) (models.FlightResult, bool) {
	segments := offer.OutboundFlights
	if len(segments) == 0 && offer.OutboundFlight.Origin != "" {
		segments = []transaviaFlight{offer.OutboundFlight}
	}
	if len(segments) == 0 {
		return models.FlightResult{}, false
	}

	price, cur := transaviaOfferPrice(offer, fallbackCurrency)

	legs := make([]models.FlightLeg, 0, len(segments))
	for _, s := range segments {
		legs = append(legs, transaviaLeg(s))
	}
	computeLayovers(legs)

	total := 0
	for _, l := range legs {
		total += l.Duration
	}

	return models.FlightResult{
		Price:      price,
		Currency:   cur,
		Duration:   total,
		Stops:      max(len(legs)-1, 0),
		Provider:   "transavia",
		Legs:       legs,
		BookingURL: transaviaBookingURL(segments[0]),
	}, true
}

func transaviaOfferPrice(offer transaviaOffer, fallbackCurrency string) (float64, string) {
	if offer.Price.Total > 0 {
		return offer.Price.Total, firstNonEmpty(offer.Price.Currency, fallbackCurrency)
	}
	if offer.PricingInfo.TotalPriceOnePassenger > 0 {
		return offer.PricingInfo.TotalPriceOnePassenger, firstNonEmpty(offer.PricingInfo.Currency, fallbackCurrency)
	}
	return offer.PricingInfo.TotalPriceAllPassengers, firstNonEmpty(offer.PricingInfo.Currency, fallbackCurrency)
}

func transaviaLeg(s transaviaFlight) models.FlightLeg {
	carrier := s.MarketingFlight.CarrierCode
	if carrier == "" {
		carrier = "HV"
	}
	number := s.MarketingFlight.Number
	if number == 0 {
		number = s.FlightNumber
	}
	flightNo := ""
	if number > 0 {
		flightNo = fmt.Sprintf("%s%d", carrier, number)
	}
	return models.FlightLeg{
		DepartureAirport: models.AirportInfo{Code: strings.ToUpper(s.Origin)},
		ArrivalAirport:   models.AirportInfo{Code: strings.ToUpper(s.Destination)},
		DepartureTime:    transaviaDisplayTime(s.DepartureDate),
		ArrivalTime:      transaviaDisplayTime(s.ArrivalDate),
		Duration:         transaviaDuration(s.DepartureDate, s.ArrivalDate),
		Airline:          transaviaAirlineName(carrier),
		AirlineCode:      carrier,
		FlightNumber:     flightNo,
		Aircraft:         s.Equipment,
	}
}

func transaviaAirlineName(code string) string {
	if strings.EqualFold(code, "TO") {
		return "Transavia France"
	}
	return "Transavia"
}

func transaviaDisplayTime(s string) string {
	if t, err := time.Parse(transaviaTimeLayout, s); err == nil {
		return t.Format(flightTimeLayout)
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Format(flightTimeLayout)
	}
	return s
}

func transaviaDuration(dep, arr string) int {
	dt, derr := transaviaParse(dep)
	at, aerr := transaviaParse(arr)
	if derr != nil || aerr != nil {
		return 0
	}
	mins := int(at.Sub(dt).Minutes())
	if mins < 0 {
		return 0
	}
	return mins
}

func transaviaParse(s string) (time.Time, error) {
	if t, err := time.Parse(transaviaTimeLayout, s); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, s)
}

func transaviaBookingURL(s transaviaFlight) string {
	day := s.DepartureDate
	if t, err := transaviaParse(s.DepartureDate); err == nil {
		day = t.Format("2006-01-02")
	}
	q := url.Values{}
	q.Set("origin", strings.ToUpper(s.Origin))
	q.Set("destination", strings.ToUpper(s.Destination))
	q.Set("outboundDate", day)
	q.Set("adults", "1")
	return "https" + "://" + "www.transavia.com/en-EU/book/?" + q.Encode()
}
