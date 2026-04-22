// Package flights -- hidden_city.go
//
// Hidden-city ticketing detector per Mikko's travel mental model:
// book X→HUB→Y where the full-itinerary price (with Y beyond) is cheaper
// than the direct X→HUB price. Passenger exits at HUB and skips the Y leg.
//
// The function is advisory only: it flags candidates and returns the
// itinerary details for the caller to display. It never books or suggests
// actions.
//
// Reference: travel_search_mental_model.md section "HIDDEN-CITY SKIP-LAST-LEG".

package flights

import (
	"context"
	"strings"
	"sync"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// HiddenCitySearchFn prices an origin→dest search via caller-injected provider.
// Matches the shape of SearchFlights for dependency injection in tests.
type HiddenCitySearchFn func(ctx context.Context, origin, destination, date string) (*models.FlightSearchResult, error)

// HiddenCityCandidate describes one detected savings opportunity.
type HiddenCityCandidate struct {
	BeyondDestination string  // IATA code of the "throwaway" leg destination
	DirectPrice       float64 // price of direct origin→hub OW
	HiddenPrice       float64 // price of origin→hub→beyond full itinerary
	Savings           float64 // DirectPrice - HiddenPrice
	Currency          string
	Itinerary         models.FlightResult // the cheap flight that routes through hub
}

// hiddenCitySeedBeyondDestinations returns the seed set of "beyond" airports
// to probe when hub is AMS or HEL. Chosen to cover common destinations from
// those hubs where fare construction produces hidden-city savings.
//
// Derived from Mikko's documented hidden-city success cases (AMS→HEL→RIX via
// Finnair) and typical KL/AF beyond-city fare structures.
func hiddenCitySeedBeyondDestinations(hub string) []string {
	switch strings.ToUpper(hub) {
	case "AMS":
		return []string{"HEL", "RIX", "TLL", "WAW", "BUD", "VIE", "OSL", "ARN", "CPH", "ZRH", "MUC", "FCO", "MAD", "BCN", "LIS", "DUB", "ATH", "IST", "KRK", "PRG", "OTP"}
	case "HEL":
		return []string{"RIX", "TLL", "ARN", "OSL", "CPH", "MUC", "VIE", "WAW", "KRK", "BUD", "PRG", "FRA", "ZRH", "AMS", "LHR", "CDG"}
	default:
		// Generic set for other hubs; callers should customise if needed.
		return []string{"FRA", "CDG", "LHR", "MAD", "AMS", "VIE"}
	}
}

// routesThroughHub reports whether a flight's itinerary transits the given hub.
// Returns true when any leg arrives at hub AND hub is not the final destination.
func routesThroughHub(flight models.FlightResult, hub string) bool {
	hub = strings.ToUpper(hub)
	if len(flight.Legs) == 0 {
		return false
	}
	finalArrival := strings.ToUpper(flight.Legs[len(flight.Legs)-1].ArrivalAirport.Code)
	if finalArrival == hub {
		return false // hub is final dest, not a transit
	}
	for _, leg := range flight.Legs {
		if strings.ToUpper(leg.ArrivalAirport.Code) == hub {
			return true
		}
	}
	return false
}

// DetectHiddenCity probes beyond-destinations and returns candidates where
// the full itinerary is cheaper than the direct origin→hub flight.
//
// Concurrency is bounded to 5 parallel searches. The direct price comes from
// direct origin→hub; each beyond is searched independently. Returned
// candidates are sorted by Savings descending (caller may re-sort).
//
// minSavings is the minimum €/$ saving required to flag a candidate.
// Default 30 when zero.
//
// Returns nil (no error) if direct price cannot be obtained — hidden-city
// comparison requires a baseline.
func DetectHiddenCity(ctx context.Context, origin, hub, date string, search HiddenCitySearchFn, minSavings float64) ([]HiddenCityCandidate, error) {
	if minSavings <= 0 {
		minSavings = 30
	}

	// Get direct price baseline.
	direct, err := search(ctx, origin, hub, date)
	if err != nil {
		return nil, err
	}
	if direct == nil || !direct.Success || len(direct.Flights) == 0 {
		return nil, nil
	}
	directPrice := direct.Flights[0].Price
	currency := direct.Flights[0].Currency

	beyonds := hiddenCitySeedBeyondDestinations(hub)
	sem := make(chan struct{}, 5)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var candidates []HiddenCityCandidate

	for _, beyond := range beyonds {
		if strings.EqualFold(beyond, origin) || strings.EqualFold(beyond, hub) {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(b string) {
			defer wg.Done()
			defer func() { <-sem }()
			res, err := search(ctx, origin, b, date)
			if err != nil || res == nil || !res.Success || len(res.Flights) == 0 {
				return
			}
			// Find cheapest flight that actually transits hub.
			var cheapest *models.FlightResult
			for i := range res.Flights {
				if routesThroughHub(res.Flights[i], hub) {
					if cheapest == nil || res.Flights[i].Price < cheapest.Price {
						cheapest = &res.Flights[i]
					}
				}
			}
			if cheapest == nil {
				return
			}
			saving := directPrice - cheapest.Price
			if saving < minSavings {
				return
			}
			mu.Lock()
			candidates = append(candidates, HiddenCityCandidate{
				BeyondDestination: b,
				DirectPrice:       directPrice,
				HiddenPrice:       cheapest.Price,
				Savings:           saving,
				Currency:          currency,
				Itinerary:         *cheapest,
			})
			mu.Unlock()
		}(beyond)
	}
	wg.Wait()

	// Sort by savings descending.
	for i := 0; i < len(candidates); i++ {
		for j := i + 1; j < len(candidates); j++ {
			if candidates[j].Savings > candidates[i].Savings {
				candidates[i], candidates[j] = candidates[j], candidates[i]
			}
		}
	}

	return candidates, nil
}
