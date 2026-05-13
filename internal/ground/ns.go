package ground

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

const nsTripsEndpoint = "https://gateway.apiportal.ns.nl/reisinformatie-api/api/v3/trips"

// nsAPIKey is the public API key embedded in the NS website JS for all visitors.
const nsAPIKey = "3833ed4cbc5d43bd9241420caf04365c"

// nsLimiter enforces a conservative rate limit: 5 req/min.
var nsLimiter = newProviderLimiter(12 * time.Second)

// nsClient is a dedicated HTTP client for NS API calls.
// NS has a public API so the standard client (no Chrome TLS) is sufficient.
var nsClient = &http.Client{
	Timeout: 30 * time.Second,
}

// nsStation holds metadata for an NS station.
type nsStation struct {
	Code    string // NS station abbreviation (e.g. "ASD")
	UIC     string // UIC/EVA code
	Name    string // Full station name
	City    string // Display city name
	Country string // ISO 3166-1 alpha-2
}

// nsStations maps lowercase city/alias to station info.
var nsStations = map[string]nsStation{
	// Dutch stations
	"amsterdam":  {Code: "ASD", UIC: "8400058", Name: "Amsterdam Centraal", City: "Amsterdam", Country: "NL"},
	"rotterdam":  {Code: "RTD", UIC: "8400530", Name: "Rotterdam Centraal", City: "Rotterdam", Country: "NL"},
	"den haag":   {Code: "GVC", UIC: "8400390", Name: "Den Haag Centraal", City: "Den Haag", Country: "NL"},
	"the hague":  {Code: "GVC", UIC: "8400390", Name: "Den Haag Centraal", City: "Den Haag", Country: "NL"},
	"utrecht":    {Code: "UT", UIC: "8400621", Name: "Utrecht Centraal", City: "Utrecht", Country: "NL"},
	"eindhoven":  {Code: "EHV", UIC: "8400206", Name: "Eindhoven", City: "Eindhoven", Country: "NL"},
	"groningen":  {Code: "GN", UIC: "8400263", Name: "Groningen", City: "Groningen", Country: "NL"},
	"maastricht": {Code: "MT", UIC: "8400382", Name: "Maastricht", City: "Maastricht", Country: "NL"},
	"arnhem":     {Code: "AH", UIC: "8400071", Name: "Arnhem Centraal", City: "Arnhem", Country: "NL"},
	"breda":      {Code: "BD", UIC: "8400126", Name: "Breda", City: "Breda", Country: "NL"},
	// International destinations NS serves
	"brussels": {Code: "BRUSSEL", UIC: "8814001", Name: "Brussel-Zuid", City: "Brussels", Country: "BE"},
	"antwerp":  {Code: "ANTWERPB", UIC: "8821006", Name: "Antwerpen-Berchem", City: "Antwerp", Country: "BE"},
	"berlin":   {Code: "BERLIN", UIC: "8011160", Name: "Berlin Hbf", City: "Berlin", Country: "DE"},
	"london":   {Code: "LONDON", UIC: "7015400", Name: "London St Pancras", City: "London", Country: "GB"},
}

// LookupNSStation resolves a city name to an NS station (case-insensitive).
func LookupNSStation(city string) (nsStation, bool) {
	s, ok := nsStations[strings.ToLower(strings.TrimSpace(city))]
	return s, ok
}

// HasNSStation returns true if the city has a known NS station.
func HasNSStation(city string) bool {
	_, ok := LookupNSStation(city)
	return ok
}

// nsTripsResponse represents the top-level response from the NS trips API.
type nsTripsResponse struct {
	Trips []nsTrip `json:"trips"`
}

type nsTrip struct {
	Legs                     []nsTripLeg `json:"legs"`
	OptimalPrice             *nsPrice    `json:"optimalPrice,omitempty"`
	ProductType              string      `json:"productType,omitempty"`
	Transfers                int         `json:"transfers"`
	PlannedDurationInMinutes int         `json:"plannedDurationInMinutes"`
}

type nsTripLeg struct {
	Origin      nsStop `json:"origin"`
	Destination nsStop `json:"destination"`
	// trainCategory is the train type (e.g. "Intercity", "Sprinter")
	TrainCategory string `json:"trainCategory,omitempty"`
	Direction     string `json:"direction,omitempty"`
	// plannedDepartureDateTime and plannedArrivalDateTime are ISO 8601 strings.
	PlannedDepartureDateTime string `json:"plannedDepartureDateTime,omitempty"`
	PlannedArrivalDateTime   string `json:"plannedArrivalDateTime,omitempty"`
	// actualDepartureDateTime and actualArrivalDateTime are ISO 8601 strings.
	ActualDepartureDateTime string `json:"actualDepartureDateTime,omitempty"`
	ActualArrivalDateTime   string `json:"actualArrivalDateTime,omitempty"`
}

type nsStop struct {
	Name            string `json:"name"`
	UICCode         string `json:"uicCode,omitempty"`
	PlannedDateTime string `json:"plannedDateTime,omitempty"`
	ActualDateTime  string `json:"actualDateTime,omitempty"`
	City            string `json:"city,omitempty"`
}

type nsPrice struct {
	TotalPriceInCents int `json:"totalPriceInCents,omitempty"`
	PriceInCents      int `json:"priceInCents,omitempty"`
}

// nsPrices maps a "from-to" pair (lowercase, hyphen-separated city names) to the
// second-class single fare in EUR. Prices are fixed NS tariffs that change rarely.
// The map is keyed by the canonical direction; lookupNSPrice also checks the reverse.
var nsPrices = map[string]float64{
	"amsterdam-rotterdam":  17.40,
	"amsterdam-den haag":   13.50,
	"amsterdam-utrecht":    9.40,
	"amsterdam-eindhoven":  24.70,
	"amsterdam-groningen":  33.90,
	"amsterdam-maastricht": 30.50,
	"amsterdam-arnhem":     21.60,
	"amsterdam-breda":      20.60,
	"rotterdam-utrecht":    12.80,
	"rotterdam-den haag":   5.40,
	"utrecht-arnhem":       11.50,
	"utrecht-eindhoven":    16.60,
}

// lookupNSPrice returns the fixed NS fare in EUR for a city pair, or 0 if unknown.
// Comparison is case-insensitive and direction-independent.
func lookupNSPrice(from, to string) float64 {
	key := strings.ToLower(from) + "-" + strings.ToLower(to)
	if p, ok := nsPrices[key]; ok {
		return p
	}
	// Try reverse direction.
	key = strings.ToLower(to) + "-" + strings.ToLower(from)
	if p, ok := nsPrices[key]; ok {
		return p
	}
	return 0
}

// SearchNS searches NS (Dutch Railways) for train trips between two cities.
// date must be in YYYY-MM-DD format. currency is used for the output GroundRoute.
func SearchNS(ctx context.Context, from, to, date, currency string) ([]models.GroundRoute, error) {
	fromStation, ok := LookupNSStation(from)
	if !ok {
		return nil, fmt.Errorf("no NS station for %q", from)
	}
	toStation, ok := LookupNSStation(to)
	if !ok {
		return nil, fmt.Errorf("no NS station for %q", to)
	}

	if currency == "" {
		currency = "EUR"
	}

	// NS API requires dateTime in the format YYYY-MM-DDTHH:MM.
	dateTime := date + "T08:00"

	params := url.Values{
		"fromStation": {fromStation.Name},
		"toStation":   {toStation.Name},
		"dateTime":    {dateTime},
	}
	apiURL := nsTripsEndpoint + "?" + params.Encode()

	// Wait for rate limiter.
	if err := nsLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("ns rate limiter: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "en")
	req.Header.Set("Ocp-Apim-Subscription-Key", nsAPIKey)
	req.Header.Set("X-Caller-Id", "NS Web")

	slog.Debug("ns search", "from", fromStation.City, "to", toStation.City, "date", date)

	resp, err := nsClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ns search: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("ns search: HTTP %d: %s", resp.StatusCode, body)
	}

	rawBody, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("ns read body: %w", err)
	}
	slog.Debug("ns response", "status", resp.StatusCode, "body_len", len(rawBody))

	var nsResp nsTripsResponse
	if err := json.Unmarshal(rawBody, &nsResp); err != nil {
		return nil, fmt.Errorf("ns decode: %w", err)
	}

	slog.Debug("ns parsed", "trips", len(nsResp.Trips))
	return parseNSTrips(nsResp.Trips, fromStation, toStation, currency), nil
}

// parseNSTrips converts NS API trips into GroundRoute models.
func parseNSTrips(trips []nsTrip, fromStation, toStation nsStation, currency string) []models.GroundRoute {
	var routes []models.GroundRoute
	for _, trip := range trips {
		if len(trip.Legs) == 0 {
			continue
		}

		first := trip.Legs[0]
		last := trip.Legs[len(trip.Legs)-1]

		depTime := firstNonEmpty(first.Origin.PlannedDateTime, first.Origin.ActualDateTime, first.PlannedDepartureDateTime)
		arrTime := firstNonEmpty(last.Destination.PlannedDateTime, last.Destination.ActualDateTime, last.PlannedArrivalDateTime)

		// Price from optimalPrice; NS always prices in cents (EUR).
		price := 0.0
		if trip.OptimalPrice != nil {
			cents := trip.OptimalPrice.TotalPriceInCents
			if cents == 0 {
				cents = trip.OptimalPrice.PriceInCents
			}
			if cents > 0 {
				price = float64(cents) / 100.0
			}
		}
		// Fall back to fixed fare lookup when the API returns no price.
		if price == 0 {
			price = lookupNSPrice(fromStation.City, toStation.City)
		}

		duration := trip.PlannedDurationInMinutes
		if duration == 0 {
			duration = computeDurationMinutes(depTime, arrTime) // fallback
		}

		// Build per-leg detail.
		var legs []models.GroundLeg
		for _, leg := range trip.Legs {
			legDep := firstNonEmpty(leg.Origin.PlannedDateTime, leg.Origin.ActualDateTime, leg.PlannedDepartureDateTime)
			legArr := firstNonEmpty(leg.Destination.PlannedDateTime, leg.Destination.ActualDateTime, leg.PlannedArrivalDateTime)
			legs = append(legs, models.GroundLeg{
				Type:     "train",
				Provider: leg.TrainCategory,
				Departure: models.GroundStop{
					City:    leg.Origin.Name,
					Station: leg.Origin.Name,
					Time:    legDep,
				},
				Arrival: models.GroundStop{
					City:    leg.Destination.Name,
					Station: leg.Destination.Name,
					Time:    legArr,
				},
				Duration: computeDurationMinutes(legDep, legArr),
			})
		}

		routes = append(routes, models.GroundRoute{
			Provider: "ns",
			Type:     "train",
			Price:    price,
			Currency: strings.ToUpper(currency),
			Duration: duration,
			Departure: models.GroundStop{
				City:    fromStation.City,
				Station: first.Origin.Name,
				Time:    depTime,
			},
			Arrival: models.GroundStop{
				City:    toStation.City,
				Station: last.Destination.Name,
				Time:    arrTime,
			},
			Transfers:  trip.Transfers,
			Legs:       legs,
			BookingURL: buildNSBookingURL(fromStation.Name, toStation.Name, date(depTime)),
		})
	}
	return routes
}

// date extracts the YYYY-MM-DD portion from an ISO 8601 datetime string.
func date(datetime string) string {
	if len(datetime) >= 10 {
		return datetime[:10]
	}
	return datetime
}

// buildNSBookingURL constructs a booking URL for ns.nl.
func buildNSBookingURL(fromName, toName, travelDate string) string {
	return fmt.Sprintf("https://www.ns.nl/en/journeyplanner/#/?vertrekFromName=%s&aankomstToName=%s&type=departure&datetime=%s",
		url.QueryEscape(fromName),
		url.QueryEscape(toName),
		url.QueryEscape(travelDate+"T08:00"),
	)
}
