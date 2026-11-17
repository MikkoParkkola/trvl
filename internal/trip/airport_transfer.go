// Package trip implements multi-search travel planning helpers.
package trip

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/destinations"
	"github.com/MikkoParkkola/trvl/internal/ground"
	"github.com/MikkoParkkola/trvl/internal/models"
)

const airportTransferTaxiProvider = "taxi"

var (
	airportTransferCityProviders  = []string{"flixbus", "regiojet", "eurostar", "db", "sncf", "trainline"}
	airportTransferExtraProviders = []string{airportTransferTaxiProvider}
)

// MarketedAdditionalProviderNames returns transport providers marketed outside
// the ground-provider catalog, currently the airport-transfer taxi estimate.
func MarketedAdditionalProviderNames() []string {
	return append([]string(nil), airportTransferExtraProviders...)
}

// AirportTransferInput configures an airport-to-destination ground search.
type AirportTransferInput struct {
	AirportCode string
	Destination string
	Date        string
	ArrivalTime string
	Currency    string
	Providers   []string
	MaxPrice    float64
	Type        string
	NoCache     bool
}

// AirportTransferResult combines exact airport routing with broader city-level
// ground options when available.
type AirportTransferResult struct {
	Success         bool                 `json:"success"`
	AirportCode     string               `json:"airport_code"`
	Airport         string               `json:"airport"`
	AirportCity     string               `json:"airport_city"`
	Destination     string               `json:"destination"`
	DestinationCity string               `json:"destination_city,omitempty"`
	Date            string               `json:"date"`
	ArrivalTime     string               `json:"arrival_time,omitempty"`
	Count           int                  `json:"count"`
	ExactMatches    int                  `json:"exact_matches"`
	CityMatches     int                  `json:"city_matches"`
	Routes          []models.GroundRoute `json:"routes"`
	Error           string               `json:"error,omitempty"`
}

type airportTransferDeps struct {
	geocode          func(context.Context, string) (destinations.GeoResult, error)
	searchTransitous func(context.Context, float64, float64, float64, float64, string) ([]models.GroundRoute, error)
	searchGround     func(context.Context, string, string, string, ground.SearchOptions) (*models.GroundSearchResult, error)
	estimateTaxi     func(context.Context, ground.TaxiEstimateInput) (models.GroundRoute, error)
}

type airportTransferSearchOutcome struct {
	label  string
	exact  bool
	routes []models.GroundRoute
	err    error
}

type airportTransferRoute struct {
	route models.GroundRoute
	exact bool
}

var defaultAirportTransferDeps = airportTransferDeps{
	geocode:          destinations.Geocode,
	searchTransitous: ground.SearchTransitous,
	searchGround:     ground.SearchByName,
	estimateTaxi:     ground.EstimateTaxiTransfer,
}

// SearchAirportTransfers finds airport-to-destination ground transport options.
func SearchAirportTransfers(ctx context.Context, input AirportTransferInput) (*AirportTransferResult, error) {
	return searchAirportTransfers(ctx, input, defaultAirportTransferDeps)
}

func searchAirportTransfers(ctx context.Context, input AirportTransferInput, deps airportTransferDeps) (*AirportTransferResult, error) {
	input.AirportCode = strings.ToUpper(strings.TrimSpace(input.AirportCode))
	input.Destination = strings.TrimSpace(input.Destination)
	input.Date = strings.TrimSpace(input.Date)
	input.ArrivalTime = strings.TrimSpace(input.ArrivalTime)

	if input.AirportCode == "" || input.Destination == "" || input.Date == "" {
		return nil, fmt.Errorf("airport_code, destination, and date are required")
	}
	if err := models.ValidateIATA(input.AirportCode); err != nil {
		return nil, fmt.Errorf("invalid airport_code: %w", err)
	}
	if _, ok := models.AirportNames[input.AirportCode]; !ok {
		return nil, fmt.Errorf("unknown airport_code %q: airport metadata not available yet", input.AirportCode)
	}
	if _, err := models.ParseDate(input.Date); err != nil {
		return nil, fmt.Errorf("invalid date %q: %w", input.Date, err)
	}
	earliestDeparture, err := parseAirportTransferClock(input.ArrivalTime)
	if err != nil {
		return nil, fmt.Errorf("invalid arrival_time %q: %w", input.ArrivalTime, err)
	}

	airportName := models.LookupAirportName(input.AirportCode)
	airportCity := models.ResolveAirportCity(input.AirportCode)

	destinationGeo, err := geocodeAirportTransferDestination(ctx, deps.geocode, input.Destination, airportCity)
	if err != nil {
		return nil, fmt.Errorf("destination geocoding failed: %w", err)
	}
	destinationCity := strings.TrimSpace(destinationGeo.Locality)
	if destinationCity == "" {
		destinationCity = input.Destination
	}

	result := &AirportTransferResult{
		AirportCode:     input.AirportCode,
		Airport:         airportName,
		AirportCity:     airportCity,
		Destination:     input.Destination,
		DestinationCity: destinationCity,
		Date:            input.Date,
		ArrivalTime:     input.ArrivalTime,
	}

	transitousEnabled, taxiEnabled, cityProviders := splitAirportTransferProviders(input.Providers)
	if !transitousEnabled && !taxiEnabled && len(cityProviders) == 0 {
		result.Error = "no providers selected"
		return result, nil
	}

	outcomes := make(chan airportTransferSearchOutcome, 3)
	var wg sync.WaitGroup

	if transitousEnabled || taxiEnabled {
		originGeo, err := deps.geocode(ctx, buildAirportTransferOriginQuery(airportName))
		if err != nil {
			if transitousEnabled {
				outcomes <- airportTransferSearchOutcome{
					label: "exact airport routing",
					exact: true,
					err:   fmt.Errorf("geocode airport: %w", err),
				}
			}
			if taxiEnabled {
				outcomes <- airportTransferSearchOutcome{
					label: "taxi estimate",
					exact: true,
					err:   fmt.Errorf("geocode airport: %w", err),
				}
			}
		} else {
			if transitousEnabled {
				wg.Add(1)
				go func() {
					defer wg.Done()
					routes, err := deps.searchTransitous(ctx, originGeo.Lat, originGeo.Lon, destinationGeo.Lat, destinationGeo.Lon, input.Date)
					if err == nil {
						bookingURL := ground.BuildTransitousURL(originGeo.Lat, originGeo.Lon, destinationGeo.Lat, destinationGeo.Lon)
						for i := range routes {
							if routes[i].BookingURL == "" {
								routes[i].BookingURL = bookingURL
							}
						}
					}
					outcomes <- airportTransferSearchOutcome{
						label:  "exact airport routing",
						exact:  true,
						routes: routes,
						err:    err,
					}
				}()
			}
			if taxiEnabled {
				if deps.estimateTaxi == nil {
					outcomes <- airportTransferSearchOutcome{
						label: "taxi estimate",
						exact: true,
						err:   fmt.Errorf("taxi estimate provider not configured"),
					}
				} else {
					wg.Add(1)
					go func() {
						defer wg.Done()
						route, err := deps.estimateTaxi(ctx, ground.TaxiEstimateInput{
							FromName:    airportName,
							ToName:      input.Destination,
							FromLat:     originGeo.Lat,
							FromLon:     originGeo.Lon,
							ToLat:       destinationGeo.Lat,
							ToLon:       destinationGeo.Lon,
							CountryCode: destinationGeo.CountryCode,
							Currency:    input.Currency,
						})
						var routes []models.GroundRoute
						if err == nil {
							routes = []models.GroundRoute{route}
						}
						outcomes <- airportTransferSearchOutcome{
							label:  "taxi estimate",
							exact:  true,
							routes: routes,
							err:    err,
						}
					}()
				}
			}
		}
	}

	if len(cityProviders) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			opts := ground.SearchOptions{
				Currency:  input.Currency,
				Providers: cityProviders,
				MaxPrice:  input.MaxPrice,
				Type:      input.Type,
				NoCache:   input.NoCache,
			}
			res, err := deps.searchGround(ctx, airportCity, destinationCity, input.Date, opts)
			var routes []models.GroundRoute
			if err == nil && res != nil {
				routes = res.Routes
				if !res.Success && res.Error != "" {
					err = fmt.Errorf("%s", res.Error)
					routes = nil
				}
			}
			outcomes <- airportTransferSearchOutcome{
				label:  "city transfer search",
				exact:  false,
				routes: routes,
				err:    err,
			}
		}()
	}
	go func() {
		wg.Wait()
		close(outcomes)
	}()

	var (
		exactRoutes []models.GroundRoute
		cityRoutes  []models.GroundRoute
		errors      []string
	)
	for outcome := range outcomes {
		if outcome.err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", outcome.label, outcome.err))
			continue
		}
		if outcome.exact {
			exactRoutes = append(exactRoutes, outcome.routes...)
		} else {
			cityRoutes = append(cityRoutes, outcome.routes...)
		}
	}

	routes := mergeAirportTransferRoutes(exactRoutes, cityRoutes)
	routes = filterAirportTransferRoutesByConstraints(routes, input.MaxPrice, input.Type)
	if earliestDeparture >= 0 {
		routes = filterAirportTransferRoutes(routes, earliestDeparture)
	}
	sortAirportTransferRoutes(routes)

	result.ExactMatches, result.CityMatches = countAirportTransferMatches(routes)
	result.Routes = unwrapAirportTransferRoutes(routes)
	result.Count = len(result.Routes)
	result.Success = result.Count > 0
	if !result.Success {
		if len(errors) > 0 {
			result.Error = strings.Join(errors, "; ")
		} else {
			result.Error = "no airport transfer routes found"
		}
	}

	return result, nil
}

func splitAirportTransferProviders(providers []string) (bool, bool, []string) {
	if len(providers) == 0 {
		return true, true, append([]string(nil), airportTransferCityProviders...)
	}

	var (
		transitousEnabled bool
		taxiEnabled       bool
		cityProviders     []string
		seen              = make(map[string]bool)
	)
	for _, provider := range providers {
		provider = strings.ToLower(strings.TrimSpace(provider))
		if provider == "" || seen[provider] {
			continue
		}
		seen[provider] = true
		if provider == "transitous" {
			transitousEnabled = true
			continue
		}
		if provider == airportTransferTaxiProvider {
			taxiEnabled = true
			continue
		}
		cityProviders = append(cityProviders, provider)
	}
	return transitousEnabled, taxiEnabled, cityProviders
}

func filterAirportTransferRoutesByConstraints(routes []airportTransferRoute, maxPrice float64, typeFilter string) []airportTransferRoute {
	if maxPrice <= 0 && typeFilter == "" {
		return routes
	}

	filtered := routes[:0]
	for _, route := range routes {
		if typeFilter != "" && !strings.EqualFold(route.route.Type, typeFilter) {
			continue
		}
		if maxPrice > 0 && route.route.Price > 0 && route.route.Price > maxPrice {
			continue
		}
		filtered = append(filtered, route)
	}
	return filtered
}

func parseAirportTransferClock(value string) (int, error) {
	if value == "" {
		return -1, nil
	}
	t, err := time.Parse("15:04", value)
	if err != nil {
		return 0, fmt.Errorf("expected HH:MM")
	}
	return t.Hour()*60 + t.Minute(), nil
}

func buildAirportTransferOriginQuery(airportName string) string {
	if strings.Contains(strings.ToLower(airportName), "airport") {
		return airportName
	}
	return airportName + " airport"
}

func geocodeAirportTransferDestination(ctx context.Context, geocode func(context.Context, string) (destinations.GeoResult, error), destination, airportCity string) (destinations.GeoResult, error) {
	destination = strings.TrimSpace(destination)
	if airportCity == "" || strings.Contains(strings.ToLower(destination), strings.ToLower(airportCity)) {
		return geocode(ctx, destination)
	}
	if biased, err := geocode(ctx, destination+", "+airportCity); err == nil {
		return biased, nil
	}
	return geocode(ctx, destination)
}

func mergeAirportTransferRoutes(exactRoutes, cityRoutes []models.GroundRoute) []airportTransferRoute {
	var merged []airportTransferRoute
	seen := make(map[string]bool)

	appendRoutes := func(routes []models.GroundRoute, exact bool) {
		for _, route := range routes {
			key := fmt.Sprintf("%s|%s|%s|%.2f", route.Provider, route.Departure.Time, route.Arrival.Time, route.Price)
			if seen[key] {
				continue
			}
			seen[key] = true
			merged = append(merged, airportTransferRoute{route: route, exact: exact})
		}
	}

	appendRoutes(exactRoutes, true)
	appendRoutes(cityRoutes, false)
	return merged
}

func filterAirportTransferRoutes(routes []airportTransferRoute, earliestDeparture int) []airportTransferRoute {
	filtered := routes[:0]
	for _, route := range routes {
		minutes, ok := airportTransferDepartureMinutes(route.route.Departure.Time)
		if !ok || minutes >= earliestDeparture {
			filtered = append(filtered, route)
		}
	}
	return filtered
}

func airportTransferDepartureMinutes(value string) (int, bool) {
	if len(value) >= 16 && value[13] == ':' {
		hour, errHour := strconv.Atoi(value[11:13])
		minute, errMinute := strconv.Atoi(value[14:16])
		if errHour == nil && errMinute == nil {
			return hour*60 + minute, true
		}
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return 0, false
	}
	return parsed.Hour()*60 + parsed.Minute(), true
}

func sortAirportTransferRoutes(routes []airportTransferRoute) {
	sort.SliceStable(routes, func(i, j int) bool {
		if routes[i].exact != routes[j].exact {
			return routes[i].exact
		}
		taxiI := strings.EqualFold(routes[i].route.Type, "taxi")
		taxiJ := strings.EqualFold(routes[j].route.Type, "taxi")
		if taxiI != taxiJ {
			return !taxiI
		}
		pricedI := routes[i].route.Price > 0
		pricedJ := routes[j].route.Price > 0
		if pricedI != pricedJ {
			return pricedI
		}
		if pricedI && routes[i].route.Price != routes[j].route.Price {
			return routes[i].route.Price < routes[j].route.Price
		}
		// A count that is not known is no evidence of a direct ride.
		if routes[i].route.TransfersUnknown != routes[j].route.TransfersUnknown {
			return !routes[i].route.TransfersUnknown
		}
		if routes[i].route.Transfers != routes[j].route.Transfers {
			return routes[i].route.Transfers < routes[j].route.Transfers
		}
		if routes[i].route.Departure.Time != routes[j].route.Departure.Time {
			return routes[i].route.Departure.Time < routes[j].route.Departure.Time
		}
		if routes[i].route.Duration != routes[j].route.Duration {
			return routes[i].route.Duration < routes[j].route.Duration
		}
		return routes[i].route.Provider < routes[j].route.Provider
	})
}

func countAirportTransferMatches(routes []airportTransferRoute) (int, int) {
	var exactCount, cityCount int
	for _, route := range routes {
		if route.exact {
			exactCount++
		} else {
			cityCount++
		}
	}
	return exactCount, cityCount
}

func unwrapAirportTransferRoutes(routes []airportTransferRoute) []models.GroundRoute {
	unwrapped := make([]models.GroundRoute, 0, len(routes))
	for _, route := range routes {
		unwrapped = append(unwrapped, route.route)
	}
	return unwrapped
}
