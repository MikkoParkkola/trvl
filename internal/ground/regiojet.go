package ground

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

const (
	regiojetBaseURL   = "https://brn-ybus-pubapi.sa.cz/restapi"
	regiojetLocations = "/consts/locations"
	regiojetSearch    = "/routes/search/simple"
)

// RegioJetCity represents a city from the RegioJet locations API.
type RegioJetCity struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Country string `json:"country"`
}

// regiojetLocationsResponse wraps the country-grouped location list.
type regiojetCountry struct {
	Country string            `json:"country"`
	Code    string            `json:"code"`
	Cities  []regiojetCityRaw `json:"cities"`
}

type regiojetCityRaw struct {
	ID       int      `json:"id"`
	Name     string   `json:"name"`
	Aliases  []string `json:"aliases"`
	Stations []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"stations"`
}

// regiojetSearchResult is a single route from the RegioJet search API.
type regiojetSearchResult struct {
	DepartureTime  string   `json:"departureTime"`
	ArrivalTime    string   `json:"arrivalTime"`
	PriceFrom      float64  `json:"priceFrom"`
	PriceTo        float64  `json:"priceTo"`
	TransfersCount int      `json:"transfersCount"`
	FreeSeatsCount int      `json:"freeSeatsCount"`
	VehicleTypes   []string `json:"vehicleTypes"`
	BookingURL     string   `json:"bookingUrl"`
}

// regiojetLocationCache caches city lookups.
var regiojetLocationCache []regiojetCountry
var regiojetLocationCacheMu sync.RWMutex
var regiojetLocationCacheCond = sync.NewCond(&regiojetLocationCacheMu)
var regiojetLocationCacheLoading bool

// RegioJetAutoComplete searches for cities by name in the RegioJet network.
func RegioJetAutoComplete(ctx context.Context, query string) ([]RegioJetCity, error) {
	countries, err := loadRegioJetLocations(ctx)
	if err != nil {
		return nil, err
	}

	query = strings.ToLower(query)
	var results []RegioJetCity
	for _, country := range countries {
		for _, city := range country.Cities {
			if matchesQuery(city, query) {
				results = append(results, RegioJetCity{
					ID:      city.ID,
					Name:    city.Name,
					Country: country.Country,
				})
			}
		}
	}
	return results, nil
}

// matchesQuery checks if a city matches a search query.
func matchesQuery(city regiojetCityRaw, query string) bool {
	if strings.Contains(strings.ToLower(city.Name), query) {
		return true
	}
	for _, alias := range city.Aliases {
		if strings.Contains(strings.ToLower(alias), query) {
			return true
		}
	}
	return false
}

// loadRegioJetLocations fetches and caches the full location list.
func loadRegioJetLocations(ctx context.Context) ([]regiojetCountry, error) {
	regiojetLocationCacheMu.RLock()
	if regiojetLocationCache != nil {
		countries := cloneRegioJetCountries(regiojetLocationCache)
		regiojetLocationCacheMu.RUnlock()
		return countries, nil
	}
	regiojetLocationCacheMu.RUnlock()

	regiojetLocationCacheMu.Lock()
	for regiojetLocationCacheLoading {
		regiojetLocationCacheCond.Wait()
		if regiojetLocationCache != nil {
			countries := cloneRegioJetCountries(regiojetLocationCache)
			regiojetLocationCacheMu.Unlock()
			return countries, nil
		}
	}
	if regiojetLocationCache != nil {
		countries := cloneRegioJetCountries(regiojetLocationCache)
		regiojetLocationCacheMu.Unlock()
		return countries, nil
	}
	regiojetLocationCacheLoading = true
	regiojetLocationCacheMu.Unlock()

	countries, err := fetchRegioJetLocations(ctx)

	regiojetLocationCacheMu.Lock()
	if err == nil {
		regiojetLocationCache = cloneRegioJetCountries(countries)
	}
	regiojetLocationCacheLoading = false
	regiojetLocationCacheCond.Broadcast()
	if err != nil {
		regiojetLocationCacheMu.Unlock()
		return nil, err
	}
	cached := cloneRegioJetCountries(regiojetLocationCache)
	regiojetLocationCacheMu.Unlock()
	return cached, nil
}

func fetchRegioJetLocations(ctx context.Context) ([]regiojetCountry, error) {
	u := regiojetBaseURL + regiojetLocations
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := rateLimitedDo(ctx, regiojetLimiter, req)
	if err != nil {
		return nil, fmt.Errorf("regiojet locations: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("regiojet locations: HTTP %d: %s", resp.StatusCode, body)
	}

	var countries []regiojetCountry
	if err := json.NewDecoder(resp.Body).Decode(&countries); err != nil {
		return nil, fmt.Errorf("regiojet locations decode: %w", err)
	}

	return countries, nil
}

func cloneRegioJetCountries(src []regiojetCountry) []regiojetCountry {
	if src == nil {
		return nil
	}

	dst := make([]regiojetCountry, len(src))
	for i, country := range src {
		dst[i] = regiojetCountry{
			Country: country.Country,
			Code:    country.Code,
			Cities:  make([]regiojetCityRaw, len(country.Cities)),
		}

		for j, city := range country.Cities {
			dst[i].Cities[j] = regiojetCityRaw{
				ID:      city.ID,
				Name:    city.Name,
				Aliases: append([]string(nil), city.Aliases...),
			}
			if len(city.Stations) > 0 {
				dst[i].Cities[j].Stations = append(dst[i].Cities[j].Stations, city.Stations...)
			}
		}
	}

	return dst
}

// SearchRegioJet searches RegioJet for routes between two city IDs on a date.
// date is YYYY-MM-DD format.
func SearchRegioJet(ctx context.Context, fromCityID, toCityID int, date string, opts SearchOptions) ([]models.GroundRoute, error) {
	currency := opts.Currency
	if currency == "" {
		currency = "EUR"
	}

	params := url.Values{
		"fromLocationId":   {fmt.Sprintf("%d", fromCityID)},
		"toLocationId":     {fmt.Sprintf("%d", toCityID)},
		"fromLocationType": {"CITY"},
		"toLocationType":   {"CITY"},
		"departureDate":    {date},
		"tariffs":          {"REGULAR"},
		"currency":         {strings.ToUpper(currency)},
		"sort":             {"PRICE"},
	}
	if opts.MaxPrice > 0 {
		params.Set("priceMax", fmt.Sprintf("%.0f", opts.MaxPrice))
	}

	u := regiojetBaseURL + regiojetSearch + "?" + params.Encode()
	slog.Debug("regiojet search", "url", u)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := rateLimitedDo(ctx, regiojetLimiter, req)
	if err != nil {
		return nil, fmt.Errorf("regiojet search: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("regiojet search: HTTP %d: %s", resp.StatusCode, body)
	}

	var wrapper struct {
		Routes []regiojetSearchResult `json:"routes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&wrapper); err != nil {
		return nil, fmt.Errorf("regiojet decode: %w", err)
	}
	results := wrapper.Routes

	for _, r := range results {
		if len(r.VehicleTypes) > 0 {
			slog.Debug("regiojet vehicle types", "departure", r.DepartureTime, "types", r.VehicleTypes)
		}
	}

	// Resolve city names for display
	fromName := resolveCityName(ctx, fromCityID)
	toName := resolveCityName(ctx, toCityID)

	var routes []models.GroundRoute
	for _, r := range results {
		dur := computeRegioJetDuration(r.DepartureTime, r.ArrivalTime)
		routeType := classifyVehicleTypes(r.VehicleTypes)

		var seatsLeft *int
		if r.FreeSeatsCount > 0 {
			s := r.FreeSeatsCount
			seatsLeft = &s
		}

		route := models.GroundRoute{
			Provider:  "regiojet",
			Type:      routeType,
			Price:     r.PriceFrom,
			PriceMax:  r.PriceTo,
			Currency:  strings.ToUpper(currency),
			Duration:  dur,
			Transfers: r.TransfersCount,
			Departure: models.GroundStop{
				City: fromName,
				Time: r.DepartureTime,
			},
			Arrival: models.GroundStop{
				City: toName,
				Time: r.ArrivalTime,
			},
			SeatsLeft:  seatsLeft,
			BookingURL: buildRegioJetBookingURL(fromCityID, toCityID, date),
		}
		routes = append(routes, route)
	}

	// Filter to requested date only (RegioJet sometimes returns multi-day results).
	if date != "" {
		filtered := routes[:0]
		for _, r := range routes {
			if routeDepartsOnDate(r.Departure.Time, date) {
				filtered = append(filtered, r)
			}
		}
		routes = filtered
	}

	sort.Slice(routes, func(i, j int) bool {
		return routes[i].Price < routes[j].Price
	})
	return routes, nil
}

// resolveCityName looks up a city name by ID from the cached location list.
func resolveCityName(ctx context.Context, cityID int) string {
	countries, err := loadRegioJetLocations(ctx)
	if err != nil {
		return fmt.Sprintf("%d", cityID)
	}
	for _, country := range countries {
		for _, city := range country.Cities {
			if city.ID == cityID {
				return city.Name
			}
		}
	}
	return fmt.Sprintf("%d", cityID)
}

func computeRegioJetDuration(depTime, arrTime string) int {
	// RegioJet times are ISO 8601 with timezone: 2026-04-10T06:01:00.000+02:00
	dep, err1 := time.Parse("2006-01-02T15:04:05.000-07:00", depTime)
	arr, err2 := time.Parse("2006-01-02T15:04:05.000-07:00", arrTime)
	if err1 != nil || err2 != nil {
		// Try without millis
		dep, err1 = time.Parse(time.RFC3339, depTime)
		arr, err2 = time.Parse(time.RFC3339, arrTime)
		if err1 != nil || err2 != nil {
			return 0
		}
	}
	return int(arr.Sub(dep).Minutes())
}

func classifyVehicleTypes(types []string) string {
	hasBus, hasTrain := false, false
	for _, t := range types {
		upper := strings.ToUpper(t)
		switch {
		case upper == "BUS":
			hasBus = true
		case upper == "TRAIN" || upper == "RAIL" || upper == "RAILJET" ||
			upper == "REGIOJET" || strings.Contains(upper, "TRAIN") ||
			strings.Contains(upper, "RAIL"):
			hasTrain = true
		}
	}
	if hasBus && hasTrain {
		return "mixed"
	}
	if hasTrain {
		return "train"
	}
	if hasBus {
		return "bus"
	}
	if len(types) > 0 {
		return strings.ToLower(types[0]) // unknown type, use as-is
	}
	return "bus"
}

func buildRegioJetBookingURL(fromID, toCityID int, date string) string {
	return fmt.Sprintf("https://www.regiojet.com/en/results/%d/%d/%s",
		fromID, toCityID, date)
}

// routeDepartsOnDate checks if a departure time (ISO 8601) falls on the given date (YYYY-MM-DD).
func routeDepartsOnDate(departureTime, date string) bool {
	// Extract just the date portion from ISO time like "2026-07-01T15:00:00.000+02:00"
	if len(departureTime) >= 10 {
		return departureTime[:10] == date
	}
	return true // if we can't parse, keep it
}
