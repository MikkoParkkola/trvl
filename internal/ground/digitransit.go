package ground

import (
	"bytes"
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

const digitransitEndpoint = "https://api.digitransit.fi/routing/v2/finland/gtfs/v1"

// digitransitAPIKey is the public subscription key embedded in matka.fi for all visitors.
const digitransitAPIKey = "195ac14f2a2b40e6b06ede06b2b33bb9"

// digitransitLimiter enforces 5 req/min (conservative; actual limit is higher).
var digitransitLimiter = newProviderLimiter(12 * time.Second)

// digitransitClient is a dedicated HTTP client for Digitransit API calls.
var digitransitClient = &http.Client{
	Timeout: 30 * time.Second,
}

// digitransitStation holds lat/lon for Finnish train stations.
type digitransitStation struct {
	Lat  float64
	Lon  float64
	Name string // Official Finnish station name
}

// digitransitStations maps lowercase city name/alias to station coordinates.
var digitransitStations = map[string]digitransitStation{
	"helsinki":    {60.1719, 24.9414, "Helsinki"},
	"tampere":     {61.4978, 23.7610, "Tampere"},
	"turku":       {60.4518, 22.2666, "Turku"},
	"oulu":        {65.0121, 25.4651, "Oulu"},
	"jyväskylä":   {62.2426, 25.7473, "Jyväskylä"},
	"jyvaskyla":   {62.2426, 25.7473, "Jyväskylä"},
	"kuopio":      {62.8924, 27.6783, "Kuopio"},
	"lahti":       {60.9827, 25.6612, "Lahti"},
	"rovaniemi":   {66.5039, 25.7294, "Rovaniemi"},
	"vaasa":       {63.0952, 21.6165, "Vaasa"},
	"kouvola":     {60.8681, 26.7043, "Kouvola"},
	"seinäjoki":   {62.7903, 22.8403, "Seinäjoki"},
	"seinajoki":   {62.7903, 22.8403, "Seinäjoki"},
	"joensuu":     {62.6010, 29.7636, "Joensuu"},
	"hämeenlinna": {60.9966, 24.4641, "Hämeenlinna"},
	"hameenlinna": {60.9966, 24.4641, "Hämeenlinna"},
}

// vrPrices maps a "from-to" pair (lowercase city names, canonical direction) to the
// VR fixed second-class single fare in EUR. lookupVRPrice also checks the reverse.
var vrPrices = map[string]float64{
	"helsinki-tampere":   22.50,
	"helsinki-turku":     19.90,
	"helsinki-oulu":      59.90,
	"helsinki-jyväskylä": 34.90,
	"helsinki-kuopio":    39.90,
	"helsinki-lahti":     14.90,
	"helsinki-rovaniemi": 69.90,
	"helsinki-vaasa":     49.90,
	"helsinki-kouvola":   16.90,
	"tampere-turku":      19.90,
	"tampere-oulu":       39.90,
	"tampere-jyväskylä":  14.90,
}

// LookupDigitransitStation resolves a city name to a Digitransit station (case-insensitive).
func LookupDigitransitStation(city string) (digitransitStation, bool) {
	s, ok := digitransitStations[strings.ToLower(strings.TrimSpace(city))]
	return s, ok
}

// HasDigitransitStation returns true if the city has a known Finnish train station.
func HasDigitransitStation(city string) bool {
	_, ok := LookupDigitransitStation(city)
	return ok
}

// lookupVRPrice returns the fixed VR fare in EUR for a city pair, or 0 if unknown.
// Comparison is case-insensitive and direction-independent.
// Normalises "jyvaskyla" → "jyväskylä" so the map key always matches.
func lookupVRPrice(from, to string) float64 {
	from = normaliseFinCity(strings.ToLower(strings.TrimSpace(from)))
	to = normaliseFinCity(strings.ToLower(strings.TrimSpace(to)))

	if p, ok := vrPrices[from+"-"+to]; ok {
		return p
	}
	if p, ok := vrPrices[to+"-"+from]; ok {
		return p
	}
	return 0
}

// normaliseFinCity maps ASCII aliases to their canonical UTF-8 key in vrPrices.
func normaliseFinCity(city string) string {
	switch city {
	case "jyvaskyla":
		return "jyväskylä"
	case "seinajoki":
		return "seinäjoki"
	case "hameenlinna":
		return "hämeenlinna"
	}
	return city
}

// --- GraphQL response types ---

type digitransitResponse struct {
	Data struct {
		Plan struct {
			Itineraries []digitransitItinerary `json:"itineraries"`
		} `json:"plan"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type digitransitItinerary struct {
	StartTime int64            `json:"startTime"` // Unix ms
	EndTime   int64            `json:"endTime"`   // Unix ms
	Duration  int              `json:"duration"`  // seconds
	Legs      []digitransitLeg `json:"legs"`
}

type digitransitLeg struct {
	Mode      string          `json:"mode"`
	StartTime int64           `json:"startTime"` // Unix ms
	EndTime   int64           `json:"endTime"`   // Unix ms
	From      digitransitStop `json:"from"`
	To        digitransitStop `json:"to"`
	Route     *struct {
		ShortName string `json:"shortName"`
		LongName  string `json:"longName"`
	} `json:"route"`
	Trip *struct {
		TripHeadsign string `json:"tripHeadsign"`
	} `json:"trip"`
}

type digitransitStop struct {
	Name string  `json:"name"`
	Lat  float64 `json:"lat"`
	Lon  float64 `json:"lon"`
}

// SearchDigitransit queries the Digitransit (Matka.fi) GraphQL API for rail
// itineraries between two Finnish cities. date must be YYYY-MM-DD.
func SearchDigitransit(ctx context.Context, from, to, date, currency string) ([]models.GroundRoute, error) {
	fromStation, ok := LookupDigitransitStation(from)
	if !ok {
		return nil, fmt.Errorf("no Digitransit station for %q", from)
	}
	toStation, ok := LookupDigitransitStation(to)
	if !ok {
		return nil, fmt.Errorf("no Digitransit station for %q", to)
	}

	if currency == "" {
		currency = "EUR"
	}

	query := buildDigitransitQuery(fromStation, toStation, date)

	body, _ := json.Marshal(map[string]string{"query": query})

	if err := digitransitLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("digitransit rate limiter: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, digitransitEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("digitransit-subscription-key", digitransitAPIKey)

	slog.Debug("digitransit search", "from", fromStation.Name, "to", toStation.Name, "date", date)

	resp, err := digitransitClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("digitransit search: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("digitransit: HTTP %d: %s", resp.StatusCode, raw)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("digitransit read body: %w", err)
	}

	var dt digitransitResponse
	if err := json.Unmarshal(raw, &dt); err != nil {
		return nil, fmt.Errorf("digitransit decode: %w", err)
	}
	if len(dt.Errors) > 0 {
		return nil, fmt.Errorf("digitransit API error: %s", dt.Errors[0].Message)
	}

	slog.Debug("digitransit parsed", "itineraries", len(dt.Data.Plan.Itineraries))
	return parseDigitransitItineraries(dt.Data.Plan.Itineraries, fromStation, toStation, date, currency), nil
}

// buildDigitransitQuery constructs the GraphQL query string.
func buildDigitransitQuery(from, to digitransitStation, date string) string {
	return fmt.Sprintf(`{
  plan(
    from: {lat: %f, lon: %f}
    to: {lat: %f, lon: %f}
    date: "%s"
    time: "08:00:00"
    numItineraries: 5
    transportModes: [{mode: RAIL}]
  ) {
    itineraries {
      startTime endTime duration
      legs {
        mode
        startTime endTime
        from { name lat lon }
        to { name lat lon }
        route { shortName longName }
        trip { tripHeadsign }
      }
    }
  }
}`, from.Lat, from.Lon, to.Lat, to.Lon, date)
}

// parseDigitransitItineraries converts Digitransit itineraries to GroundRoutes.
func parseDigitransitItineraries(
	itins []digitransitItinerary,
	fromStation, toStation digitransitStation,
	searchDate, currency string,
) []models.GroundRoute {
	var routes []models.GroundRoute

	for _, itin := range itins {
		// Only include itineraries that contain at least one RAIL leg.
		hasRail := false
		for _, leg := range itin.Legs {
			if strings.EqualFold(leg.Mode, "RAIL") {
				hasRail = true
				break
			}
		}
		if !hasRail || len(itin.Legs) == 0 {
			continue
		}

		depTime := msToISO(itin.StartTime)
		arrTime := msToISO(itin.EndTime)
		durationMin := itin.Duration / 60

		price := lookupVRPrice(fromStation.Name, toStation.Name)

		// Build legs.
		var legs []models.GroundLeg
		transfers := 0
		for _, leg := range itin.Legs {
			if !strings.EqualFold(leg.Mode, "RAIL") {
				continue
			}
			routeName := ""
			if leg.Route != nil {
				routeName = leg.Route.ShortName
				if routeName == "" {
					routeName = leg.Route.LongName
				}
			}
			legDep := msToISO(leg.StartTime)
			legArr := msToISO(leg.EndTime)
			legs = append(legs, models.GroundLeg{
				Type:     "train",
				Provider: routeName,
				Departure: models.GroundStop{
					City:    leg.From.Name,
					Station: leg.From.Name,
					Time:    legDep,
				},
				Arrival: models.GroundStop{
					City:    leg.To.Name,
					Station: leg.To.Name,
					Time:    legArr,
				},
				Duration: computeDurationMinutes(legDep, legArr),
			})
		}
		if len(legs) > 1 {
			transfers = len(legs) - 1
		}

		routes = append(routes, models.GroundRoute{
			Provider: "vr",
			Type:     "train",
			Price:    price,
			Currency: strings.ToUpper(currency),
			Duration: durationMin,
			Departure: models.GroundStop{
				City:    fromStation.Name,
				Station: fromStation.Name,
				Time:    depTime,
			},
			Arrival: models.GroundStop{
				City:    toStation.Name,
				Station: toStation.Name,
				Time:    arrTime,
			},
			Transfers:  transfers,
			Legs:       legs,
			BookingURL: buildVRBookingURL(fromStation.Name, toStation.Name, searchDate),
		})
	}
	return routes
}

// msToISO converts a Unix millisecond timestamp to an ISO 8601 string (UTC).
func msToISO(ms int64) string {
	if ms == 0 {
		return ""
	}
	return time.Unix(ms/1000, 0).UTC().Format(time.RFC3339)
}

// buildVRBookingURL returns a VR.fi booking URL for the given city pair and date.
func buildVRBookingURL(from, to, date string) string {
	return fmt.Sprintf("https://www.vr.fi/en/buy-ticket?from=%s&to=%s&date=%s",
		url.QueryEscape(from),
		url.QueryEscape(to),
		url.QueryEscape(date),
	)
}
