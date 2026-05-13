// Package ground searches FlixBus and RegioJet for bus/train connections.
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
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

const (
	flixbusBaseURL      = "https://global.api.flixbus.com"
	flixbusAutocomplete = "/search/autocomplete/cities"
	flixbusSearch       = "/search/service/v4/search"
)

// FlixBusCity represents a city from the FlixBus autocomplete API.
type FlixBusCity struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Country string  `json:"country"`
	Score   float64 `json:"score"`
	Lat     float64 `json:"-"`
	Lon     float64 `json:"-"`
}

// flixbusAutocompleteResponse is the raw autocomplete item.
type flixbusAutocompleteItem struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Country  string  `json:"country"`
	Score    float64 `json:"score"`
	Location struct {
		Lat float64 `json:"lat"`
		Lon float64 `json:"lon"`
	} `json:"location"`
	IsFlixbusCity bool `json:"is_flixbus_city"`
}

// flixbusSearchResponse is the top-level search API response.
type flixbusSearchResponse struct {
	Trips []flixbusTrip `json:"trips"`
}

type flixbusTrip struct {
	DepartureCityID string                   `json:"departure_city_id"`
	ArrivalCityID   string                   `json:"arrival_city_id"`
	Results         map[string]flixbusResult `json:"results"`
}

type flixbusResult struct {
	UID          string           `json:"uid"`
	Status       string           `json:"status"`
	TransferType string           `json:"transfer_type"`
	Provider     string           `json:"provider"`
	Departure    flixbusStop      `json:"departure"`
	Arrival      flixbusStop      `json:"arrival"`
	Duration     flixbusDuration  `json:"duration"`
	Price        flixbusPrice     `json:"price"`
	Remaining    flixbusRemaining `json:"remaining"`
	Available    flixbusAvailable `json:"available"`
	Legs         []flixbusLeg     `json:"legs"`
}

type flixbusStop struct {
	Date      string `json:"date"`
	CityID    string `json:"city_id"`
	StationID string `json:"station_id"`
}

type flixbusDuration struct {
	Hours   int `json:"hours"`
	Minutes int `json:"minutes"`
}

type flixbusPrice struct {
	Total    float64 `json:"total"`
	Original float64 `json:"original"`
}

type flixbusRemaining struct {
	Seats    *int   `json:"seats"`
	Capacity string `json:"capacity"`
}

type flixbusAvailable struct {
	Seats int `json:"seats"`
}

type flixbusLeg struct {
	Departure        flixbusStop `json:"departure"`
	Arrival          flixbusStop `json:"arrival"`
	MeansOfTransport string      `json:"means_of_transport"`
	Amenities        []any       `json:"amenities"`
}

// FlixBusAutoComplete searches for cities by name.
func FlixBusAutoComplete(ctx context.Context, query string) ([]FlixBusCity, error) {
	u := flixbusBaseURL + flixbusAutocomplete + "?" + url.Values{
		"q":      {query},
		"locale": {"en"},
	}.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}

	resp, err := rateLimitedDo(ctx, flixbusLimiter, req)
	if err != nil {
		return nil, fmt.Errorf("flixbus autocomplete: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("flixbus autocomplete: HTTP %d: %s", resp.StatusCode, body)
	}

	var items []flixbusAutocompleteItem
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, fmt.Errorf("flixbus autocomplete decode: %w", err)
	}

	var cities []FlixBusCity
	for _, item := range items {
		if !item.IsFlixbusCity {
			continue
		}
		cities = append(cities, FlixBusCity{
			ID:      item.ID,
			Name:    item.Name,
			Country: item.Country,
			Score:   item.Score,
			Lat:     item.Location.Lat,
			Lon:     item.Location.Lon,
		})
	}
	return cities, nil
}

// SearchFlixBus searches FlixBus for routes between two cities on a date.
// fromCity and toCity are FlixBus UUID city IDs.
// date is YYYY-MM-DD format.
func SearchFlixBus(ctx context.Context, fromCity, toCity, date string, opts SearchOptions) ([]models.GroundRoute, error) {
	currency := opts.Currency
	if currency == "" {
		currency = "EUR"
	}

	// Convert YYYY-MM-DD to d.m.Y for FlixBus API
	t, err := models.ParseDate(date)
	if err != nil {
		return nil, fmt.Errorf("invalid date %q: %w", date, err)
	}
	fbDate := fmt.Sprintf("%02d.%02d.%d", t.Day(), int(t.Month()), t.Year())

	params := url.Values{
		"from_city_id":   {fromCity},
		"to_city_id":     {toCity},
		"departure_date": {fbDate},
		"products":       {`{"adult":1}`},
		"currency":       {strings.ToUpper(currency)},
		"search_by":      {"cities"},
		"sort":           {"price"},
	}
	if opts.MaxPrice > 0 {
		params.Set("price_max", fmt.Sprintf("%.0f", opts.MaxPrice))
	}

	u := flixbusBaseURL + flixbusSearch + "?" + params.Encode()
	slog.Debug("flixbus search", "url", u)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}

	resp, err := rateLimitedDo(ctx, flixbusLimiter, req)
	if err != nil {
		return nil, fmt.Errorf("flixbus search: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("flixbus search: HTTP %d: %s", resp.StatusCode, body)
	}

	var searchResp flixbusSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&searchResp); err != nil {
		return nil, fmt.Errorf("flixbus decode: %w", err)
	}

	// City name cache for enriching results.
	// Collect all unique city IDs from trips/legs, then resolve via autocomplete.
	cityNames := make(map[string]string)
	{
		cityIDs := make(map[string]struct{})
		for _, trip := range searchResp.Trips {
			cityIDs[trip.DepartureCityID] = struct{}{}
			cityIDs[trip.ArrivalCityID] = struct{}{}
			for _, r := range trip.Results {
				cityIDs[r.Departure.CityID] = struct{}{}
				cityIDs[r.Arrival.CityID] = struct{}{}
				for _, leg := range r.Legs {
					cityIDs[leg.Departure.CityID] = struct{}{}
					cityIDs[leg.Arrival.CityID] = struct{}{}
				}
			}
		}
		// Resolve each unique city ID via autocomplete (FlixBus autocomplete
		// returns matching cities when queried by ID).
		for id := range cityIDs {
			if id == "" {
				continue
			}
			cities, err := FlixBusAutoComplete(ctx, id)
			if err == nil {
				for _, c := range cities {
					if c.ID == id {
						cityNames[id] = c.Name
						break
					}
				}
			}
		}
	}

	var routes []models.GroundRoute
	for _, trip := range searchResp.Trips {
		for _, r := range trip.Results {
			if r.Status != "available" {
				continue
			}

			dur := r.Duration.Hours*60 + r.Duration.Minutes
			transfers := len(r.Legs) - 1
			if transfers < 0 {
				transfers = 0
			}

			// Collect amenities across all legs
			amenities := collectFlixBusAmenities(r.Legs)

			// Parse legs
			legs := make([]models.GroundLeg, 0, len(r.Legs))
			for _, leg := range r.Legs {
				legDur := computeLegDuration(leg.Departure.Date, leg.Arrival.Date)
				legAmenities := parseFlixBusAmenities(leg.Amenities)
				legs = append(legs, models.GroundLeg{
					Type:     leg.MeansOfTransport,
					Provider: "flixbus",
					Departure: models.GroundStop{
						City:    cityNames[leg.Departure.CityID],
						Station: leg.Departure.StationID,
						Time:    leg.Departure.Date,
					},
					Arrival: models.GroundStop{
						City:    cityNames[leg.Arrival.CityID],
						Station: leg.Arrival.StationID,
						Time:    leg.Arrival.Date,
					},
					Duration:  legDur,
					Amenities: legAmenities,
				})
			}

			var seatsLeft *int
			if r.Available.Seats > 0 {
				s := r.Available.Seats
				seatsLeft = &s
			}

			routeType := "bus"
			for _, leg := range r.Legs {
				if leg.MeansOfTransport == "train" {
					routeType = "train"
					break
				}
			}
			if transfers > 0 {
				hasBus, hasTrain := false, false
				for _, leg := range r.Legs {
					if leg.MeansOfTransport == "bus" {
						hasBus = true
					}
					if leg.MeansOfTransport == "train" {
						hasTrain = true
					}
				}
				if hasBus && hasTrain {
					routeType = "mixed"
				}
			}

			route := models.GroundRoute{
				Provider:  "flixbus",
				Type:      routeType,
				Price:     r.Price.Total,
				Currency:  strings.ToUpper(currency),
				Duration:  dur,
				Transfers: transfers,
				Departure: models.GroundStop{
					City: cityNames[r.Departure.CityID],
					Time: r.Departure.Date,
				},
				Arrival: models.GroundStop{
					City: cityNames[r.Arrival.CityID],
					Time: r.Arrival.Date,
				},
				Legs:       legs,
				Amenities:  amenities,
				SeatsLeft:  seatsLeft,
				BookingURL: buildFlixBusBookingURL(fromCity, toCity, date),
			}
			routes = append(routes, route)
		}
	}

	sort.Slice(routes, func(i, j int) bool {
		return routes[i].Price < routes[j].Price
	})
	return routes, nil
}

// collectFlixBusAmenities deduplicates amenities across legs.
func collectFlixBusAmenities(legs []flixbusLeg) []string {
	seen := make(map[string]bool)
	var result []string
	for _, leg := range legs {
		for _, a := range parseFlixBusAmenities(leg.Amenities) {
			if !seen[a] {
				seen[a] = true
				result = append(result, a)
			}
		}
	}
	return result
}

// parseFlixBusAmenities handles both string and object amenity formats.
func parseFlixBusAmenities(raw []any) []string {
	var result []string
	for _, a := range raw {
		switch v := a.(type) {
		case string:
			result = append(result, strings.ToLower(v))
		case map[string]any:
			if t, ok := v["type"].(string); ok {
				result = append(result, strings.ToLower(t))
			}
		}
	}
	return result
}

func computeLegDuration(depTime, arrTime string) int {
	dep, err1 := time.Parse(time.RFC3339, depTime)
	arr, err2 := time.Parse(time.RFC3339, arrTime)
	if err1 != nil || err2 != nil {
		return 0
	}
	return int(arr.Sub(dep).Minutes())
}

func buildFlixBusBookingURL(fromID, toID, date string) string {
	return fmt.Sprintf("https://shop.flixbus.com/search?departureCity=%s&arrivalCity=%s&rideDate=%s",
		url.QueryEscape(fromID), url.QueryEscape(toID), url.QueryEscape(date))
}
