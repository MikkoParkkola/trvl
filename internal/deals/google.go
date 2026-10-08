package deals

import (
	"context"
	"fmt"
	"time"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/explore"
	"github.com/MikkoParkkola/trvl/internal/preferences"
)

// fetchGoogleExploreFunc is the live Google source, swappable so tests stay
// offline (MIK-8095).
var fetchGoogleExploreFunc = fetchGoogleExplore

// fetchGoogleExplore queries Google Flights Explore for the cheapest
// destinations from the user's home airport and converts them to Deal
// structs so they appear alongside RSS deals in trvl deals output.
func fetchGoogleExplore(ctx context.Context, origins []string) ([]Deal, error) {
	if len(origins) == 0 {
		// Try to load home airports from preferences.
		if prefs, err := preferences.Load(); err == nil && prefs != nil && len(prefs.HomeAirports) > 0 {
			origins = prefs.HomeAirports
		}
	}
	if len(origins) == 0 {
		origins = []string{"HEL"} // sensible default
	}

	client := batchexec.NewClient()

	var allDeals []Deal
	for _, origin := range origins {
		opts := explore.ExploreOptions{}
		result, err := explore.SearchExplore(ctx, client, origin, opts)
		if err != nil {
			continue // best-effort: skip failing origins
		}

		now := time.Now()
		for _, dest := range result.Destinations {
			if dest.Price <= 0 {
				continue
			}
			title := fmt.Sprintf("Cheapest to %s", dest.CityName)
			if dest.AirlineName != "" {
				title = fmt.Sprintf("%s %s to %s", dest.AirlineName, flightType(dest.Stops), dest.CityName)
			}
			if dest.Country != "" {
				title += ", " + dest.Country
			}
			title += fmt.Sprintf(" from €%.0f", dest.Price)

			allDeals = append(allDeals, Deal{
				Title:       title,
				Price:       dest.Price,
				Currency:    "EUR",
				Origin:      origin,
				Destination: dest.AirportCode,
				Airline:     dest.AirlineName,
				Stops:       stopLabel(dest.Stops),
				Type:        "deal",
				Source:      "Google Flights",
				URL:         fmt.Sprintf("https://www.google.com/travel/flights?q=flights+from+%s+to+%s", origin, dest.AirportCode),
				Published:   now,
			})
		}
	}

	return allDeals, nil
}

func flightType(stops int) string {
	if stops == 0 {
		return "nonstop"
	}
	return fmt.Sprintf("%d-stop", stops)
}

func stopLabel(stops int) string {
	switch stops {
	case 0:
		return "nonstop"
	case 1:
		return "1 stop"
	default:
		return fmt.Sprintf("%d stops", stops)
	}
}
