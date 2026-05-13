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

// europeanSleeperBase is the base URL for the European Sleeper booking API
// (B-Europe / Sqills S3 Passenger white-label).
const europeanSleeperBase = "https://booking.europeansleeper.eu"

// europeanSleeperLimiter: conservative 1 req/6s.
var europeanSleeperLimiter = newProviderLimiter(6 * time.Second)

// europeanSleeperClient is a shared HTTP client for European Sleeper API calls.
var europeanSleeperClient = &http.Client{
	Timeout: 30 * time.Second,
}

// EuropeanSleeperStation holds metadata for a European Sleeper station.
type EuropeanSleeperStation struct {
	UIC     string // UIC station code
	Name    string
	City    string
	Country string
}

// europeanSleeperStations maps lowercase city name to station info.
// UIC codes verified against European Sleeper route documentation.
var europeanSleeperStations = map[string]EuropeanSleeperStation{
	// Belgium
	"brussels": {UIC: "8800004", Name: "Bruxelles-Midi", City: "Brussels", Country: "BE"},
	"antwerp":  {UIC: "8800046", Name: "Antwerpen-Centraal", City: "Antwerp", Country: "BE"},

	// Netherlands
	"rotterdam": {UIC: "8400530", Name: "Rotterdam Centraal", City: "Rotterdam", Country: "NL"},
	"amsterdam": {UIC: "8400058", Name: "Amsterdam Centraal", City: "Amsterdam", Country: "NL"},

	// Germany
	"berlin":  {UIC: "8011160", Name: "Berlin Hbf", City: "Berlin", Country: "DE"},
	"dresden": {UIC: "8010085", Name: "Dresden Hbf", City: "Dresden", Country: "DE"},

	// Czech Republic
	"prague": {UIC: "5400014", Name: "Praha hlavní nádraží", City: "Prague", Country: "CZ"},
	"praha":  {UIC: "5400014", Name: "Praha hlavní nádraží", City: "Prague", Country: "CZ"},
}

// LookupEuropeanSleeperStation resolves a city name to a European Sleeper station (case-insensitive).
func LookupEuropeanSleeperStation(city string) (EuropeanSleeperStation, bool) {
	s, ok := europeanSleeperStations[strings.ToLower(strings.TrimSpace(city))]
	return s, ok
}

// HasEuropeanSleeperStation returns true if the city has a known European Sleeper station.
func HasEuropeanSleeperStation(city string) bool {
	_, ok := LookupEuropeanSleeperStation(city)
	return ok
}

// HasEuropeanSleeperRoute returns true if at least one of the two cities is on
// the European Sleeper network. This is a lightweight check — the provider is
// queried when either endpoint matches, and will return no results if there is
// no actual connection between the pair.
func HasEuropeanSleeperRoute(from, to string) bool {
	return HasEuropeanSleeperStation(from) || HasEuropeanSleeperStation(to)
}

// europeanSleeperTripsResponse represents the API response from the Sqills S3
// offers endpoint.
type europeanSleeperTripsResponse struct {
	Trips []europeanSleeperTrip `json:"trips"`
	Data  []europeanSleeperTrip `json:"data"` // alternative key
}

type europeanSleeperTrip struct {
	DepartureTime string                   `json:"departureTime"`
	ArrivalTime   string                   `json:"arrivalTime"`
	Duration      int                      `json:"duration"` // minutes
	Origin        europeanSleeperTripStop  `json:"origin"`
	Destination   europeanSleeperTripStop  `json:"destination"`
	Prices        []europeanSleeperPrice   `json:"prices"`
	Fares         []europeanSleeperPrice   `json:"fares"` // alternative key
	Segments      []europeanSleeperSegment `json:"segments"`
}

type europeanSleeperTripStop struct {
	Name    string `json:"name"`
	Station string `json:"station"`
}

type europeanSleeperPrice struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
	Class    string  `json:"class"`
}

type europeanSleeperSegment struct {
	DepartureTime string                  `json:"departureTime"`
	ArrivalTime   string                  `json:"arrivalTime"`
	Origin        europeanSleeperTripStop `json:"origin"`
	Destination   europeanSleeperTripStop `json:"destination"`
	TrainNumber   string                  `json:"trainNumber"`
	Operator      string                  `json:"operator"`
}

// SearchEuropeanSleeper searches European Sleeper for night train journeys
// between two cities. It uses the Sqills S3 Passenger API exposed by
// booking.europeansleeper.eu.
func SearchEuropeanSleeper(ctx context.Context, from, to, date, currency string) ([]models.GroundRoute, error) {
	fromStation, ok := LookupEuropeanSleeperStation(from)
	if !ok {
		return nil, fmt.Errorf("no European Sleeper station for %q", from)
	}
	toStation, ok := LookupEuropeanSleeperStation(to)
	if !ok {
		return nil, fmt.Errorf("no European Sleeper station for %q", to)
	}

	if currency == "" {
		currency = "EUR"
	}

	if err := europeanSleeperLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("european sleeper rate limiter: %w", err)
	}

	slog.Debug("european sleeper search", "from", fromStation.City, "to", toStation.City, "date", date)

	apiURL := fmt.Sprintf("%s/api/v3/offers?origin=%s&destination=%s&date=%s&passengers=1&currency=%s",
		europeanSleeperBase,
		url.QueryEscape(fromStation.UIC),
		url.QueryEscape(toStation.UIC),
		url.QueryEscape(date),
		url.QueryEscape(strings.ToUpper(currency)),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "trvl/1.0 (travel agent; github.com/MikkoParkkola/trvl)")

	resp, err := europeanSleeperClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("european sleeper search: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		// The Sqills API may not be publicly accessible; fail gracefully.
		// European Sleeper routes are also covered by Deutsche Bahn and ÖBB providers.
		slog.Debug("european sleeper API unavailable", "status", resp.StatusCode)
		return nil, nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("european sleeper read: %w", err)
	}

	var tripsResp europeanSleeperTripsResponse
	if err := json.Unmarshal(body, &tripsResp); err != nil {
		return nil, fmt.Errorf("european sleeper decode: %w", err)
	}

	trips := tripsResp.Trips
	if len(trips) == 0 {
		trips = tripsResp.Data
	}

	bookingURL := buildEuropeanSleeperBookingURL(fromStation, toStation, date)
	routes := parseEuropeanSleeperTrips(trips, fromStation, toStation, date, currency, bookingURL)

	slog.Debug("european sleeper results", "routes", len(routes))
	return routes, nil
}

// parseEuropeanSleeperTrips converts API trips into GroundRoute models.
func parseEuropeanSleeperTrips(trips []europeanSleeperTrip, fromStation, toStation EuropeanSleeperStation, date, currency, bookingURL string) []models.GroundRoute {
	var routes []models.GroundRoute

	for _, trip := range trips {
		depTime := normaliseTimeString(trip.DepartureTime)
		arrTime := normaliseTimeString(trip.ArrivalTime)

		duration := trip.Duration
		if duration <= 0 {
			duration = computeDurationMinutes(depTime, arrTime)
		}

		// Find cheapest price.
		price := 0.0
		priceCurrency := strings.ToUpper(currency)
		allPrices := trip.Prices
		if len(allPrices) == 0 {
			allPrices = trip.Fares
		}
		for _, p := range allPrices {
			if p.Amount > 0 && (price == 0 || p.Amount < price) {
				price = p.Amount
				if p.Currency != "" {
					priceCurrency = strings.ToUpper(p.Currency)
				}
			}
		}

		// Build legs from segments.
		var legs []models.GroundLeg
		for _, seg := range trip.Segments {
			legs = append(legs, models.GroundLeg{
				Type:     "train",
				Provider: firstNonEmpty(seg.Operator, seg.TrainNumber),
				Departure: models.GroundStop{
					City:    firstNonEmpty(seg.Origin.Name, fromStation.City),
					Station: seg.Origin.Station,
					Time:    normaliseTimeString(seg.DepartureTime),
				},
				Arrival: models.GroundStop{
					City:    firstNonEmpty(seg.Destination.Name, toStation.City),
					Station: seg.Destination.Station,
					Time:    normaliseTimeString(seg.ArrivalTime),
				},
				Duration: computeDurationMinutes(normaliseTimeString(seg.DepartureTime), normaliseTimeString(seg.ArrivalTime)),
			})
		}

		transfers := 0
		if len(legs) > 1 {
			transfers = len(legs) - 1
		}

		routes = append(routes, models.GroundRoute{
			Provider: "european_sleeper",
			Type:     "train",
			Price:    price,
			Currency: priceCurrency,
			Duration: duration,
			Departure: models.GroundStop{
				City:    fromStation.City,
				Station: fromStation.Name,
				Time:    depTime,
			},
			Arrival: models.GroundStop{
				City:    toStation.City,
				Station: toStation.Name,
				Time:    arrTime,
			},
			Transfers:  transfers,
			Legs:       legs,
			BookingURL: bookingURL,
		})
	}

	return routes
}

// normaliseTimeString trims an ISO 8601 datetime string to the canonical
// "2006-01-02T15:04:05" format, stripping timezone suffixes.
func normaliseTimeString(s string) string {
	if s == "" {
		return ""
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Format("2006-01-02T15:04:05")
	}
	for _, layout := range []string{
		"2006-01-02T15:04:05.000",
		"2006-01-02T15:04:05",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format("2006-01-02T15:04:05")
		}
	}
	return s
}

// buildEuropeanSleeperBookingURL constructs a booking URL for europeanSleeper.eu.
func buildEuropeanSleeperBookingURL(from, to EuropeanSleeperStation, date string) string {
	return fmt.Sprintf("https://www.europeansleeper.eu/en/booking?origin=%s&destination=%s&date=%s",
		url.QueryEscape(from.UIC),
		url.QueryEscape(to.UIC),
		url.QueryEscape(date),
	)
}
