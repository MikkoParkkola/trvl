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

// snalltagetBase is the base URL for the Snälltåget booking API
// (Sqills S3 Passenger).
const snalltagetBase = "https://bokning.snalltaget.se"

// snalltagetLimiter: conservative 1 req/6s.
var snalltagetLimiter = newProviderLimiter(6 * time.Second)

// snalltagetClient is a shared HTTP client for Snälltåget API calls.
var snalltagetClient = &http.Client{
	Timeout: 30 * time.Second,
}

// SnalltagetStation holds metadata for a Snälltåget station.
type SnalltagetStation struct {
	UIC     string // UIC station code
	Name    string
	City    string
	Country string
}

// snalltagetStations maps lowercase city name to station info.
// UIC codes verified against Snälltåget route documentation.
var snalltagetStations = map[string]SnalltagetStation{
	// Sweden — year-round Stockholm–Malmö route
	"stockholm":  {UIC: "7400001", Name: "Stockholm Central", City: "Stockholm", Country: "SE"},
	"norrköping": {UIC: "7400120", Name: "Norrköping C", City: "Norrköping", Country: "SE"},
	"norrkoping": {UIC: "7400120", Name: "Norrköping C", City: "Norrköping", Country: "SE"},
	"linköping":  {UIC: "7400180", Name: "Linköping C", City: "Linköping", Country: "SE"},
	"linkoping":  {UIC: "7400180", Name: "Linköping C", City: "Linköping", Country: "SE"},
	"alvesta":    {UIC: "7400440", Name: "Alvesta", City: "Alvesta", Country: "SE"},
	"hässleholm": {UIC: "7400490", Name: "Hässleholm C", City: "Hässleholm", Country: "SE"},
	"hassleholm": {UIC: "7400490", Name: "Hässleholm C", City: "Hässleholm", Country: "SE"},
	"lund":       {UIC: "7400500", Name: "Lund C", City: "Lund", Country: "SE"},
	"malmö":      {UIC: "7400003", Name: "Malmö C", City: "Malmö", Country: "SE"},
	"malmo":      {UIC: "7400003", Name: "Malmö C", City: "Malmö", Country: "SE"},
	"malmö c":    {UIC: "7400003", Name: "Malmö C", City: "Malmö", Country: "SE"},

	// Sweden — winter ski train (Stockholm–Åre/Duved)
	"åre":   {UIC: "7402200", Name: "Åre", City: "Åre", Country: "SE"},
	"are":   {UIC: "7402200", Name: "Åre", City: "Åre", Country: "SE"},
	"duved": {UIC: "7402210", Name: "Duved", City: "Duved", Country: "SE"},

	// Germany — summer seasonal (Stockholm–Berlin via ferry)
	"berlin": {UIC: "8011160", Name: "Berlin Hbf", City: "Berlin", Country: "DE"},
}

// LookupSnalltagetStation resolves a city name to a Snälltåget station (case-insensitive).
func LookupSnalltagetStation(city string) (SnalltagetStation, bool) {
	s, ok := snalltagetStations[strings.ToLower(strings.TrimSpace(city))]
	return s, ok
}

// HasSnalltagetStation returns true if the city has a known Snälltåget station.
func HasSnalltagetStation(city string) bool {
	_, ok := LookupSnalltagetStation(city)
	return ok
}

// HasSnalltagetRoute returns true if at least one of the two cities is on the
// Snälltåget network. Snälltåget is a niche night-train operator so we query
// when either endpoint matches.
func HasSnalltagetRoute(from, to string) bool {
	return HasSnalltagetStation(from) || HasSnalltagetStation(to)
}

// snalltagetTripsResponse represents the API response from the Sqills S3
// offers endpoint.
type snalltagetTripsResponse struct {
	Trips []snalltagetTrip `json:"trips"`
	Data  []snalltagetTrip `json:"data"` // alternative key
}

type snalltagetTrip struct {
	DepartureTime string              `json:"departureTime"`
	ArrivalTime   string              `json:"arrivalTime"`
	Duration      int                 `json:"duration"` // minutes
	Origin        snalltagetTripStop  `json:"origin"`
	Destination   snalltagetTripStop  `json:"destination"`
	Prices        []snalltagetPrice   `json:"prices"`
	Fares         []snalltagetPrice   `json:"fares"` // alternative key
	Segments      []snalltagetSegment `json:"segments"`
}

type snalltagetTripStop struct {
	Name    string `json:"name"`
	Station string `json:"station"`
}

type snalltagetPrice struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
	Class    string  `json:"class"`
}

type snalltagetSegment struct {
	DepartureTime string             `json:"departureTime"`
	ArrivalTime   string             `json:"arrivalTime"`
	Origin        snalltagetTripStop `json:"origin"`
	Destination   snalltagetTripStop `json:"destination"`
	TrainNumber   string             `json:"trainNumber"`
	Operator      string             `json:"operator"`
}

// SearchSnalltaget searches Snälltåget for night train journeys between two
// cities. It uses the Sqills S3 Passenger API exposed by bokning.snalltaget.se.
func SearchSnalltaget(ctx context.Context, from, to, date, currency string) ([]models.GroundRoute, error) {
	fromStation, ok := LookupSnalltagetStation(from)
	if !ok {
		return nil, fmt.Errorf("no Snälltåget station for %q", from)
	}
	toStation, ok := LookupSnalltagetStation(to)
	if !ok {
		return nil, fmt.Errorf("no Snälltåget station for %q", to)
	}

	if currency == "" {
		currency = "SEK"
	}

	if err := snalltagetLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("snalltaget rate limiter: %w", err)
	}

	slog.Debug("snalltaget search", "from", fromStation.City, "to", toStation.City, "date", date)

	apiURL := fmt.Sprintf("%s/api/v3/offers?origin=%s&destination=%s&date=%s&passengers=1&currency=%s",
		snalltagetBase,
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

	resp, err := snalltagetClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("snalltaget search: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		// The Sqills API may not be publicly accessible; fail gracefully.
		// Snälltåget routes are also covered by Deutsche Bahn and SJ providers.
		slog.Debug("snalltaget API unavailable", "status", resp.StatusCode)
		return nil, nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("snalltaget read: %w", err)
	}

	var tripsResp snalltagetTripsResponse
	if err := json.Unmarshal(body, &tripsResp); err != nil {
		return nil, fmt.Errorf("snalltaget decode: %w", err)
	}

	trips := tripsResp.Trips
	if len(trips) == 0 {
		trips = tripsResp.Data
	}

	bookingURL := buildSnalltagetBookingURL(fromStation, toStation, date)
	routes := parseSnalltagetTrips(trips, fromStation, toStation, date, currency, bookingURL)

	slog.Debug("snalltaget results", "routes", len(routes))
	return routes, nil
}

// parseSnalltagetTrips converts API trips into GroundRoute models.
func parseSnalltagetTrips(trips []snalltagetTrip, fromStation, toStation SnalltagetStation, date, currency, bookingURL string) []models.GroundRoute {
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
			Provider: "snalltaget",
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

// buildSnalltagetBookingURL constructs a booking URL for snalltaget.se.
func buildSnalltagetBookingURL(from, to SnalltagetStation, date string) string {
	return fmt.Sprintf("https://www.snalltaget.se/en/booking?origin=%s&destination=%s&date=%s",
		url.QueryEscape(from.UIC),
		url.QueryEscape(to.UIC),
		url.QueryEscape(date),
	)
}
