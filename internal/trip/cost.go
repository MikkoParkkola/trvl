// Package trip implements trip cost estimation by combining flight and hotel searches.
package trip

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"

	"github.com/MikkoParkkola/trvl/internal/destinations"
	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/preferences"
)

// TripCostInput configures a trip cost calculation.
type TripCostInput struct {
	Origin      string
	Destination string
	DepartDate  string
	ReturnDate  string
	Guests      int
	Currency    string // target currency for total
}

// FlightCost holds flight price details.
type FlightCost struct {
	Outbound      float64 `json:"outbound"`
	Return        float64 `json:"return"`
	Currency      string  `json:"currency"`
	OutboundStops int     `json:"outbound_stops"`
	ReturnStops   int     `json:"return_stops"`
}

// HotelCost holds hotel price details.
type HotelCost struct {
	PerNight float64 `json:"per_night"`
	Total    float64 `json:"total"`
	Currency string  `json:"currency"`
	Name     string  `json:"name,omitempty"`
}

// TripCostResult is the top-level response for a trip cost calculation.
type TripCostResult struct {
	Success   bool       `json:"success"`
	Flights   FlightCost `json:"flights"`
	Hotels    HotelCost  `json:"hotels"`
	Total     float64    `json:"total"`
	Currency  string     `json:"currency"`
	PerPerson float64    `json:"per_person"`
	PerDay    float64    `json:"per_day"`
	Nights    int        `json:"nights"`
	Error     string     `json:"error,omitempty"`
}

// CalculateTripCost estimates the total cost of a trip by searching for the
// cheapest outbound flight, return flight, and hotel at the destination.
//
// Flights are priced per person; hotels are per room (not multiplied by guests).
// Total = (outbound + return) * guests + hotel_per_night * nights.
func CalculateTripCost(ctx context.Context, input TripCostInput) (*TripCostResult, error) {
	if input.Origin == "" || input.Destination == "" {
		return nil, fmt.Errorf("origin and destination are required")
	}
	if input.DepartDate == "" || input.ReturnDate == "" {
		return nil, fmt.Errorf("depart_date and return_date are required")
	}
	if input.Guests <= 0 {
		return nil, fmt.Errorf("guests must be at least 1")
	}

	departDate, err := models.ParseDate(input.DepartDate)
	if err != nil {
		return nil, fmt.Errorf("invalid depart_date %q: %w", input.DepartDate, err)
	}
	returnDate, err := models.ParseDate(input.ReturnDate)
	if err != nil {
		return nil, fmt.Errorf("invalid return_date %q: %w", input.ReturnDate, err)
	}
	if !returnDate.After(departDate) {
		return nil, fmt.Errorf("return_date must be after depart_date")
	}

	nights := int(math.Round(returnDate.Sub(departDate).Hours() / 24))
	if nights <= 0 {
		return nil, fmt.Errorf("trip must be at least 1 night")
	}

	result := &TripCostResult{
		Currency: input.Currency,
		Nights:   nights,
	}

	// Load user preferences for hotel filtering.
	prefs, _ := preferences.Load()

	// Build hotel search options with preference-based filters.
	// MaxPages=1: compound commands only need the cheapest hotel, not 75 results.
	// This avoids fetching 3 paginated HTML pages (~1.5-3MB each) per search.
	hotelOpts := hotels.HotelSearchOptions{
		CheckIn:  input.DepartDate,
		CheckOut: input.ReturnDate,
		Guests:   input.Guests,
		Sort:     "cheapest",
		Currency: input.Currency,
		MaxPages: 1,
	}
	if prefs != nil {
		if prefs.MinHotelStars > 0 {
			hotelOpts.Stars = prefs.MinHotelStars
		}
		if prefs.MinHotelRating > 0 {
			hotelOpts.MinRating = prefs.MinHotelRating
		}
		if prefs.BudgetPerNightMax > 0 {
			hotelOpts.MaxPrice = prefs.BudgetPerNightMax
		}
	}

	client := newCompoundSearchClient()

	// Search all three in parallel so the shared no-cache client reduces memory
	// without regressing command latency.
	var (
		outResult   *models.FlightSearchResult
		retResult   *models.FlightSearchResult
		hotelResult *models.HotelSearchResult
		outErr      error
		retErr      error
		hotelErr    error
		wg          sync.WaitGroup
	)

	wg.Add(3)
	go func() {
		defer wg.Done()
		outResult, outErr = flights.SearchFlightsWithClient(ctx, client, input.Origin, input.Destination, input.DepartDate, flights.SearchOptions{
			SortBy: models.SortCheapest,
			Adults: 1,
		})
	}()
	go func() {
		defer wg.Done()
		retResult, retErr = flights.SearchFlightsWithClient(ctx, client, input.Destination, input.Origin, input.ReturnDate, flights.SearchOptions{
			SortBy: models.SortCheapest,
			Adults: 1,
		})
	}()
	go func() {
		defer wg.Done()
		hotelLocation := models.ResolveHotelCity(input.Destination)
		hotelResult, hotelErr = hotels.SearchHotelsWithClient(ctx, client, hotelLocation, hotelOpts)
	}()
	wg.Wait()

	// Apply preference-based post-filtering (dormitories, ensuite, districts).
	if hotelErr == nil && hotelResult != nil && hotelResult.Success && prefs != nil {
		city := models.ResolveLocationName(input.Destination)
		hotelResult.Hotels = preferences.FilterHotels(hotelResult.Hotels, city, prefs)
		hotelResult.Count = len(hotelResult.Hotels)
	}

	assembleTripCost(ctx, result, input.Currency, outResult, outErr, retResult, retErr, hotelResult, hotelErr, nights, input.Guests)

	return result, nil
}

// assembleTripCost populates the TripCostResult from search results.
// It extracts cheapest flights/hotels, aggregates costs, and computes per-person/per-day.
func assembleTripCost(ctx context.Context, result *TripCostResult, requestedCurrency string, outResult *models.FlightSearchResult, outErr error, retResult *models.FlightSearchResult, retErr error, hotelResult *models.HotelSearchResult, hotelErr error, nights, guests int) {
	// Extract cheapest outbound flight.
	if outErr == nil && outResult != nil && outResult.Success && len(outResult.Flights) > 0 {
		cheapest := cheapestFlight(outResult.Flights)
		result.Flights.Outbound = cheapest.Price
		result.Flights.Currency = cheapest.Currency
		result.Flights.OutboundStops = cheapest.Stops
	}

	// Extract cheapest return flight.
	if retErr == nil && retResult != nil && retResult.Success && len(retResult.Flights) > 0 {
		cheapest := cheapestFlight(retResult.Flights)
		result.Flights.Return = cheapest.Price
		if result.Flights.Currency == "" {
			result.Flights.Currency = cheapest.Currency
		}
		result.Flights.ReturnStops = cheapest.Stops
	}

	// Extract cheapest hotel.
	if hotelErr == nil && hotelResult != nil && hotelResult.Success && len(hotelResult.Hotels) > 0 {
		cheapest := cheapestHotel(hotelResult.Hotels)
		result.Hotels.PerNight = cheapest.Price
		result.Hotels.Total = cheapest.Price * float64(nights)
		result.Hotels.Currency = cheapest.Currency
		result.Hotels.Name = cheapest.Name
	}

	// Collect errors.
	var errors []string
	if outErr != nil {
		errors = append(errors, fmt.Sprintf("outbound flights: %v", outErr))
	}
	if retErr != nil {
		errors = append(errors, fmt.Sprintf("return flights: %v", retErr))
	}
	if hotelErr != nil {
		errors = append(errors, fmt.Sprintf("hotels: %v", hotelErr))
	}

	applyTripCostCurrencyAndTotals(ctx, result, requestedCurrency, nights, guests, destinations.ConvertCurrency)
	result.Error = formatTripCostError(errors, result.Success)
}

type tripCostCurrencyConverter func(context.Context, float64, string, string) (float64, string)

func applyTripCostCurrencyAndTotals(
	ctx context.Context,
	result *TripCostResult,
	requestedCurrency string,
	nights, guests int,
	convert tripCostCurrencyConverter,
) {
	result.Success = false
	result.Total = 0
	result.PerPerson = 0
	result.PerDay = 0

	targetCurrency := chooseTripCostSummaryCurrency(
		requestedCurrency,
		result.Flights.Currency,
		result.Hotels.Currency,
	)
	flightCurrency := result.Flights.Currency
	hotelCurrency := result.Hotels.Currency

	if targetCurrency != "" {
		if result.Flights.Outbound > 0 {
			result.Flights.Outbound, result.Flights.Currency = convertedTripCostAmount(
				ctx,
				result.Flights.Outbound,
				flightCurrency,
				targetCurrency,
				convert,
			)
		}
		if result.Flights.Return > 0 {
			result.Flights.Return, result.Flights.Currency = convertedTripCostAmount(
				ctx,
				result.Flights.Return,
				flightCurrency,
				targetCurrency,
				convert,
			)
		}
		if result.Hotels.PerNight > 0 {
			result.Hotels.PerNight, result.Hotels.Currency = convertedTripCostAmount(
				ctx,
				result.Hotels.PerNight,
				hotelCurrency,
				targetCurrency,
				convert,
			)
		}
		if result.Hotels.Total > 0 {
			result.Hotels.Total, result.Hotels.Currency = convertedTripCostAmount(
				ctx,
				result.Hotels.Total,
				hotelCurrency,
				targetCurrency,
				convert,
			)
		}
		result.Currency = targetCurrency
	}

	// Flights are per person, hotels are per room.
	flightPerPerson := result.Flights.Outbound + result.Flights.Return
	flightTotal := flightPerPerson * float64(guests)
	result.Total = flightTotal + result.Hotels.Total

	if result.Total > 0 {
		result.Success = true
		result.PerPerson = result.Total / float64(guests)
		if nights > 0 {
			result.PerDay = result.Total / float64(nights)
		}
	}
}

func formatTripCostError(errors []string, partial bool) string {
	if len(errors) == 0 {
		return ""
	}

	joined := strings.Join(errors, "; ")
	if partial {
		return "partial failure: " + joined
	}

	return joined
}

// cheapestFlight returns the flight with the lowest positive price.
func cheapestFlight(flts []models.FlightResult) models.FlightResult {
	best := flts[0]
	for _, f := range flts[1:] {
		if f.Price > 0 && (best.Price <= 0 || f.Price < best.Price) {
			best = f
		}
	}
	return best
}

// cheapestHotel returns the hotel with the lowest positive price.
func cheapestHotel(htls []models.HotelResult) models.HotelResult {
	var best models.HotelResult
	for _, h := range htls {
		if !models.HotelPriceEligibleForFinalTripCost(h) {
			continue
		}
		if best.Price <= 0 || h.Price < best.Price {
			best = h
		}
	}
	return best
}

func chooseTripCostSummaryCurrency(requested string, currencies ...string) string {
	if requested != "" {
		return requested
	}
	for _, currency := range currencies {
		if currency != "" {
			return currency
		}
	}
	return ""
}

func convertedTripCostAmount(
	ctx context.Context,
	amount float64,
	from, to string,
	convert tripCostCurrencyConverter,
) (float64, string) {
	converted, currency := convert(ctx, amount, from, to)
	return math.Round(converted*100) / 100, currency
}
