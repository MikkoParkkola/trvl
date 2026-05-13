package ground

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// Transitous provides the MOTIS 2 API for pan-European public transit routing.
// API docs: https://transitous.org/api/
// OpenAPI: https://github.com/motis-project/motis/blob/master/openapi.yaml
const transitousEndpoint = "https://api.transitous.org/api/v1/plan"

// transitousLimiter enforces a conservative rate limit: 10 req/min.
// Transitous is a community-run service with limited resources.
var transitousLimiter = newProviderLimiter(6 * time.Second)

// transitousClient is a dedicated HTTP client for Transitous API calls.
var transitousClient = &http.Client{
	Timeout: 30 * time.Second,
}

// transitousResponse is the top-level response from the MOTIS plan endpoint.
type transitousResponse struct {
	Itineraries []transitousItinerary `json:"itineraries"`
	From        transitousPlace       `json:"from"`
	To          transitousPlace       `json:"to"`
}

type transitousItinerary struct {
	Duration  int             `json:"duration"`  // seconds
	StartTime string          `json:"startTime"` // ISO 8601
	EndTime   string          `json:"endTime"`   // ISO 8601
	Transfers int             `json:"transfers"`
	Legs      []transitousLeg `json:"legs"`
}

type transitousLeg struct {
	Mode      string           `json:"mode"` // WALK, BUS, TRAM, SUBWAY, RAIL, REGIONAL_RAIL, etc.
	From      transitousPlace  `json:"from"`
	To        transitousPlace  `json:"to"`
	Duration  int              `json:"duration"` // seconds
	StartTime string           `json:"startTime"`
	EndTime   string           `json:"endTime"`
	Route     *transitousRoute `json:"route,omitempty"`
	TripId    string           `json:"tripId,omitempty"`
	HeadSign  string           `json:"headsign,omitempty"`
}

type transitousPlace struct {
	Name      string  `json:"name"`
	Lat       float64 `json:"lat"`
	Lon       float64 `json:"lon"`
	StopID    string  `json:"stopId,omitempty"`
	Departure string  `json:"departure,omitempty"`
	Arrival   string  `json:"arrival,omitempty"`
}

type transitousRoute struct {
	ShortName string `json:"shortName,omitempty"`
	LongName  string `json:"longName,omitempty"`
	Agency    string `json:"agency,omitempty"`
	Mode      string `json:"mode,omitempty"`
}

// SearchTransitous searches Transitous (MOTIS 2) for transit connections
// between two coordinate pairs. date is YYYY-MM-DD (departure date).
func SearchTransitous(ctx context.Context, fromLat, fromLon, toLat, toLon float64, date string) ([]models.GroundRoute, error) {
	// Build departure time as ISO 8601 (8:00 AM local, UTC for API).
	departureTime := date + "T08:00:00Z"

	params := url.Values{
		"fromPlace":      {fmt.Sprintf("%.6f,%.6f", fromLat, fromLon)},
		"toPlace":        {fmt.Sprintf("%.6f,%.6f", toLat, toLon)},
		"time":           {departureTime},
		"numItineraries": {"5"},
	}

	apiURL := transitousEndpoint + "?" + params.Encode()

	if err := transitousLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("transitous rate limiter: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "trvl/1.0 (travel agent; github.com/MikkoParkkola/trvl)")

	slog.Debug("transitous search",
		"from", fmt.Sprintf("%.4f,%.4f", fromLat, fromLon),
		"to", fmt.Sprintf("%.4f,%.4f", toLat, toLon),
		"date", date)

	resp, err := transitousClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("transitous search: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("transitous search: HTTP %d: %s", resp.StatusCode, respBody)
	}

	var result transitousResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("transitous decode: %w", err)
	}

	var routes []models.GroundRoute
	for _, itin := range result.Itineraries {
		// Skip walk-only itineraries.
		if isWalkOnly(itin) {
			continue
		}

		route := models.GroundRoute{
			Provider:  "transitous",
			Type:      classifyTransitousType(itin),
			Price:     0, // Transitous does not provide pricing
			Currency:  "",
			Duration:  itin.Duration / 60, // seconds to minutes
			Transfers: itin.Transfers,
			Departure: models.GroundStop{
				City: result.From.Name,
				Time: itin.StartTime,
			},
			Arrival: models.GroundStop{
				City: result.To.Name,
				Time: itin.EndTime,
			},
		}

		// Build legs from non-walk segments.
		for _, leg := range itin.Legs {
			if leg.Mode == "WALK" {
				continue
			}
			groundLeg := models.GroundLeg{
				Type:     motisModeTo(leg.Mode),
				Provider: transitousLegProvider(leg),
				Departure: models.GroundStop{
					Station: leg.From.Name,
					Time:    leg.StartTime,
				},
				Arrival: models.GroundStop{
					Station: leg.To.Name,
					Time:    leg.EndTime,
				},
				Duration: leg.Duration / 60,
			}
			route.Legs = append(route.Legs, groundLeg)
		}

		// Set station names from first/last transit leg.
		if len(route.Legs) > 0 {
			route.Departure.Station = route.Legs[0].Departure.Station
			route.Arrival.Station = route.Legs[len(route.Legs)-1].Arrival.Station
		}

		routes = append(routes, route)
	}

	return routes, nil
}

// isWalkOnly returns true if all legs in the itinerary are walking.
func isWalkOnly(itin transitousItinerary) bool {
	for _, leg := range itin.Legs {
		if leg.Mode != "WALK" {
			return false
		}
	}
	return true
}

// classifyTransitousType determines the transport type from itinerary legs.
func classifyTransitousType(itin transitousItinerary) string {
	hasTrain := false
	hasBus := false
	hasTram := false

	for _, leg := range itin.Legs {
		switch leg.Mode {
		case "WALK":
			continue
		case "BUS", "COACH":
			hasBus = true
		case "TRAM", "CABLE_CAR", "GONDOLA", "FUNICULAR":
			hasTram = true
		default:
			// RAIL, REGIONAL_RAIL, SUBURBAN, SUBWAY, HIGHSPEED_RAIL, METRO, etc.
			hasTrain = true
		}
	}

	switch {
	case hasTrain && hasBus:
		return "mixed"
	case hasTrain:
		return "train"
	case hasTram:
		return "tram"
	case hasBus:
		return "bus"
	default:
		return "transit"
	}
}

// motisModeTo maps MOTIS mode strings to our simpler type vocabulary.
func motisModeTo(mode string) string {
	switch mode {
	case "BUS", "COACH":
		return "bus"
	case "TRAM", "CABLE_CAR", "GONDOLA", "FUNICULAR":
		return "tram"
	case "SUBWAY", "METRO":
		return "metro"
	default:
		return "train"
	}
}

// transitousLegProvider extracts the operator/agency from a leg.
func transitousLegProvider(leg transitousLeg) string {
	if leg.Route != nil && leg.Route.Agency != "" {
		return leg.Route.Agency
	}
	return "transitous"
}

// BuildTransitousURL constructs a query URL for the Transitous web interface.
func BuildTransitousURL(fromLat, fromLon, toLat, toLon float64) string {
	return fmt.Sprintf("https://transitous.org/?from=%.6f,%.6f&to=%.6f,%.6f",
		fromLat, fromLon, toLat, toLon)
}
