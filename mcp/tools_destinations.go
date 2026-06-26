package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/destinations"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/trip"
)

// --- Tool definition ---

func destinationInfoTool() ToolDef {
	return ToolDef{
		Name:        "destination_info",
		Title:       "Destination Info",
		Description: "Get travel intelligence for any city: weather forecast, country info, public holidays, safety advisory, and currency exchange rates. All from free APIs, no keys required.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"location":     {Type: "string", Description: "City or location name (e.g., Tokyo, Barcelona, New York)"},
				"travel_dates": {Type: "string", Description: "Optional travel date range as YYYY-MM-DD,YYYY-MM-DD (e.g., 2026-06-15,2026-06-18)"},
			},
			Required: []string{"location"},
		},
		OutputSchema: destinationInfoOutputSchema(),
		Annotations: &ToolAnnotations{
			Title:          "Destination Info",
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  true,
		},
	}
}

func destinationInfoOutputSchema() interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"location": schemaString(),
			"country": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name":       schemaString(),
					"code":       schemaString(),
					"capital":    schemaString(),
					"languages":  schemaStringArray(),
					"currencies": schemaStringArray(),
					"region":     schemaString(),
				},
			},
			"weather": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"current": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"date":             schemaString(),
							"temp_high_c":      schemaNum(),
							"temp_low_c":       schemaNum(),
							"precipitation_mm": schemaNum(),
							"description":      schemaString(),
						},
					},
					"forecast": schemaArray(map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"date":             schemaString(),
							"temp_high_c":      schemaNum(),
							"temp_low_c":       schemaNum(),
							"precipitation_mm": schemaNum(),
							"description":      schemaString(),
						},
					}),
				},
			},
			"holidays": schemaArray(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"date": schemaString(),
					"name": schemaString(),
					"type": schemaString(),
				},
			}),
			"safety": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"level":        schemaNum(),
					"advisory":     schemaString(),
					"source":       schemaString(),
					"last_updated": schemaString(),
				},
			},
			"currency": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"local_currency": schemaString(),
					"exchange_rate":  schemaNum(),
					"base_currency":  schemaString(),
				},
			},
			"timezone": schemaString(),
		},
		"required": []string{"location"},
	}
}

// --- Tool handler ---

func handleDestinationInfo(ctx context.Context, args map[string]any, elicit ElicitFunc, sampling SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	location := argString(args, "location")
	if location == "" {
		return nil, nil, fmt.Errorf("location is required")
	}

	travelDates := argString(args, "travel_dates")
	var dates models.DateRange
	if travelDates != "" {
		parts := strings.SplitN(travelDates, ",", 2)
		if len(parts) == 2 {
			dates.CheckIn = strings.TrimSpace(parts[0])
			dates.CheckOut = strings.TrimSpace(parts[1])
		} else if len(parts) == 1 {
			dates.CheckIn = strings.TrimSpace(parts[0])
		}
	}

	info, err := destinations.GetDestinationInfo(ctx, location, dates)
	if err != nil {
		return nil, nil, err
	}

	summary := destinationSummary(info)
	content, err := buildAnnotatedContentBlocks(summary, info)
	if err != nil {
		return nil, nil, err
	}

	return content, info, nil
}

func destinationSummary(info *models.DestinationInfo) string {
	parts := []string{fmt.Sprintf("Destination: %s", info.Location)}

	if info.Country.Name != "" {
		parts = append(parts, fmt.Sprintf("Country: %s (%s)", info.Country.Name, info.Country.Region))
	}

	if info.Weather.Current.Date != "" {
		parts = append(parts, fmt.Sprintf("Today: %s, %.0f-%.0f C",
			info.Weather.Current.Description, info.Weather.Current.TempLow, info.Weather.Current.TempHigh))
	}

	if info.Safety.Source != "" {
		parts = append(parts, fmt.Sprintf("Safety: %.1f/5 - %s", info.Safety.Level, info.Safety.Advisory))
	}

	if info.Currency.LocalCurrency != "" && info.Currency.ExchangeRate > 0 {
		parts = append(parts, fmt.Sprintf("Currency: 1 EUR = %.2f %s", info.Currency.ExchangeRate, info.Currency.LocalCurrency))
	}

	if len(info.Holidays) > 0 {
		parts = append(parts, fmt.Sprintf("%d public holidays during travel dates", len(info.Holidays)))
	}

	return strings.Join(parts, ". ") + "."
}

// --- Weekend Getaway tool ---

// weekendGetawayOutputSchema returns the JSON Schema for WeekendResult.
func weekendGetawayOutputSchema() interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"success": schemaBool(),
			"origin":  schemaString(),
			"month":   schemaString(),
			"nights":  schemaInt(),
			"count":   schemaInt(),
			"destinations": schemaArray(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"destination":    schemaString(),
					"airport_code":   schemaString(),
					"flight_price":   schemaNum(),
					"hotel_estimate": schemaNum(),
					"total_estimate": schemaNum(),
					"currency":       schemaString(),
					"stops":          schemaInt(),
					"airline_name":   schemaString(),
				},
				"required": []string{"destination", "airport_code", "total_estimate", "currency"},
			}),
			"error": schemaString(),
		},
		"required": []string{"success", "count"},
	}
}

func weekendGetawayTool() ToolDef {
	return ToolDef{
		Name:        "weekend_getaway",
		Title:       "Weekend Getaway Finder",
		Description: "Find cheap weekend getaway destinations from an airport. Returns top 10 cheapest destinations ranked by total estimated cost (round-trip flight + estimated hotel).",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"origin":     {Type: "string", Description: "Departure airport IATA code (e.g., HEL, JFK)"},
				"month":      {Type: "string", Description: "Month to search (e.g., july-2026, 2026-07)"},
				"max_budget": {Type: "number", Description: "Maximum total budget in EUR (0 = no limit)"},
				"nights":     {Type: "integer", Description: "Number of nights (default: 2)"},
			},
			Required: []string{"origin", "month"},
		},
		OutputSchema: weekendGetawayOutputSchema(),
		Annotations: &ToolAnnotations{
			Title:          "Weekend Getaway Finder",
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  true,
		},
	}
}

func handleWeekendGetaway(ctx context.Context, args map[string]any, elicit ElicitFunc, sampling SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	origin := strings.ToUpper(argString(args, "origin"))
	month := argString(args, "month")

	if origin == "" {
		return nil, nil, fmt.Errorf("origin is required")
	}
	if month == "" {
		return nil, nil, fmt.Errorf("month is required")
	}

	if err := models.ValidateIATA(origin); err != nil {
		return nil, nil, fmt.Errorf("invalid origin: %w", err)
	}

	opts := trip.WeekendOptions{
		Month:     month,
		MaxBudget: argFloat(args, "max_budget", 0),
		Nights:    argInt(args, "nights", 2),
	}

	result, err := trip.FindWeekendGetaways(ctx, origin, opts)
	if err != nil {
		return nil, nil, err
	}

	summary := weekendSummary(result)
	content, err := buildAnnotatedContentBlocks(summary, result)
	if err != nil {
		return nil, nil, err
	}

	return content, result, nil
}

func weekendSummary(result *trip.WeekendResult) string {
	if !result.Success || result.Count == 0 {
		if result.Error != "" {
			return fmt.Sprintf("Weekend getaway search failed: %s", result.Error)
		}
		return "No weekend getaway destinations found."
	}

	parts := []string{
		fmt.Sprintf("Found %d weekend getaway destinations from %s in %s (%d nights)",
			result.Count, result.Origin, result.Month, result.Nights),
	}

	if len(result.Destinations) > 0 {
		d := result.Destinations[0]
		parts = append(parts, fmt.Sprintf("Cheapest: %s (%s) - EUR %.0f total (flight %.0f + hotel est. %.0f)",
			d.Destination, d.AirportCode, d.Total, d.FlightPrice, d.HotelPrice))
	}

	return strings.Join(parts, ". ") + "."
}

// --- Trip Cost tool ---

func tripCostTool() ToolDef {
	return ToolDef{
		Name:        "calculate_trip_cost",
		Title:       "Calculate Trip Cost",
		Description: "Estimate total trip cost including outbound flight, return flight, and hotel accommodation. Flights are priced per person; hotels are per room.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"origin":      {Type: "string", Description: "Departure airport IATA code (e.g., HEL, JFK)"},
				"destination": {Type: "string", Description: "Destination airport IATA code (e.g., BCN, LHR)"},
				"depart_date": {Type: "string", Description: "Departure date in YYYY-MM-DD format"},
				"return_date": {Type: "string", Description: "Return date in YYYY-MM-DD format"},
				"guests":      {Type: "integer", Description: "Number of guests (default: 1, must be >= 1). Flights multiply by guests; hotel is per room."},
				"currency":    {Type: "string", Description: "Currency code for totals (default: EUR)"},
			},
			Required: []string{"origin", "destination", "depart_date", "return_date"},
		},
		OutputSchema: tripCostOutputSchema(),
		Annotations: &ToolAnnotations{
			Title:          "Calculate Trip Cost",
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  true,
		},
	}
}

func tripCostOutputSchema() interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"success":    schemaBool(),
			"flights":    schemaObject(),
			"hotels":     schemaObject(),
			"total":      schemaNum(),
			"currency":   schemaString(),
			"per_person": schemaNum(),
			"per_day":    schemaNum(),
			"nights":     schemaInt(),
			"error":      schemaString(),
		},
		"required": []string{"success", "total", "currency"},
	}
}

func handleTripCost(ctx context.Context, args map[string]any, elicit ElicitFunc, sampling SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	origin, dest, err := validateOriginDest(args)
	if err != nil {
		return nil, nil, err
	}

	departDate := argString(args, "depart_date")
	returnDate := argString(args, "return_date")
	guests := argInt(args, "guests", 1)
	currency := argString(args, "currency")

	if departDate == "" || returnDate == "" {
		return nil, nil, fmt.Errorf("depart_date and return_date are required")
	}

	result, err := trip.CalculateTripCost(ctx, trip.TripCostInput{
		Origin:      origin,
		Destination: dest,
		DepartDate:  departDate,
		ReturnDate:  returnDate,
		Guests:      guests,
		Currency:    currency,
	})
	if err != nil {
		return nil, nil, err
	}

	summary := tripCostSummary(result, origin, dest, guests)
	content, err := buildAnnotatedContentBlocks(summary, result)
	if err != nil {
		return nil, nil, err
	}

	return content, result, nil
}

func tripCostSummary(result *trip.TripCostResult, origin, dest string, guests int) string {
	if !result.Success {
		if result.Error != "" {
			return fmt.Sprintf("Trip cost estimation %s to %s failed: %s", origin, dest, result.Error)
		}
		return fmt.Sprintf("Could not estimate trip cost from %s to %s.", origin, dest)
	}

	parts := []string{
		fmt.Sprintf("Trip %s -> %s: %d nights, %d guest(s)", origin, dest, result.Nights, guests),
	}

	if result.Flights.Outbound > 0 || result.Flights.Return > 0 {
		parts = append(parts, fmt.Sprintf("Flights: outbound %s, return %s",
			tripCostSummaryAmount(result.Flights.Outbound, result.Flights.Currency),
			tripCostSummaryAmount(result.Flights.Return, result.Flights.Currency)))
	}
	if result.Hotels.PerNight > 0 {
		parts = append(parts, fmt.Sprintf("Hotel: %s %.0f/night (%s)",
			result.Hotels.Currency, result.Hotels.PerNight, result.Hotels.Name))
	} else {
		parts = append(parts, "Hotel: unavailable")
	}

	parts = append(parts, fmt.Sprintf("Total: %s %.0f (%.0f/person, %.0f/day)",
		result.Currency, result.Total, result.PerPerson, result.PerDay))
	if result.Error != "" {
		parts = append(parts, "Warning: "+result.Error)
	}

	return strings.Join(parts, ". ") + "."
}

func tripCostSummaryAmount(amount float64, currency string) string {
	if amount <= 0 {
		return "unavailable"
	}
	return fmt.Sprintf("%s %.0f", currency, amount)
}

// --- Plan Trip tool ---

func planTripOutputSchema() interface{} {
	flightItemSchema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"price":         schemaNum(),
			"currency":      schemaString(),
			"airline":       schemaString(),
			"flight_number": schemaString(),
			"stops":         schemaInt(),
			"duration_min":  schemaInt(),
			"departure":     schemaString(),
			"arrival":       schemaString(),
			"route":         schemaString(),
		},
	}
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"success":          schemaBool(),
			"origin":           schemaString(),
			"destination":      schemaString(),
			"depart_date":      schemaString(),
			"return_date":      schemaString(),
			"nights":           schemaInt(),
			"guests":           schemaInt(),
			"outbound_flights": schemaArray(flightItemSchema),
			"return_flights":   schemaArray(flightItemSchema),
			"hotels": schemaArray(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name":      schemaString(),
					"rating":    schemaNum(),
					"reviews":   schemaInt(),
					"per_night": schemaNum(),
					"total":     schemaNum(),
					"currency":  schemaString(),
					"amenities": schemaString(),
				},
			}),
			"summary": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"flights_total": schemaNum(),
					"hotel_total":   schemaNum(),
					"grand_total":   schemaNum(),
					"per_person":    schemaNum(),
					"per_day":       schemaNum(),
					"currency":      schemaString(),
				},
			},
			"error": schemaString(),
		},
		"required": []string{"success", "origin", "destination"},
	}
}

func planTripTool() ToolDef {
	return ToolDef{
		Name:        "plan_trip",
		Title:       "Plan Complete Trip",
		Description: "Plan a complete trip with outbound flights, return flights, and hotel options in one search. Returns top 5 options for each plus a total cost summary. Omit return_date for a one-way trip.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"origin":      {Type: "string", Description: "Origin IATA airport code (e.g. AMS, HEL)"},
				"destination": {Type: "string", Description: "Destination IATA airport code (e.g. PRG, BCN)"},
				"depart_date": {Type: "string", Description: "Departure date (YYYY-MM-DD)"},
				"return_date": {Type: "string", Description: "Return date (YYYY-MM-DD). Omit for a one-way trip."},
				"guests":      {Type: "integer", Description: "Number of guests (default: 1, must be >= 1)"},
				"currency":    {Type: "string", Description: "Target currency (e.g. EUR, USD)"},
			},
			Required: []string{"origin", "destination", "depart_date"},
		},
		OutputSchema: planTripOutputSchema(),
		Annotations: &ToolAnnotations{
			Title:          "Plan Complete Trip",
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  true,
		},
	}
}

func handlePlanTrip(ctx context.Context, args map[string]any, elicit ElicitFunc, sampling SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	origin, dest, err := validateOriginDest(args)
	if err != nil {
		return nil, nil, err
	}

	departDate := argString(args, "depart_date")
	returnDate := argString(args, "return_date")
	if departDate == "" {
		return nil, nil, fmt.Errorf("depart_date is required")
	}
	// An empty return_date plans a one-way trip — mirrors the CLI plan-all path,
	// which leaves the return date blank for one-way searches. PlanTrip then skips
	// the return leg without fabricating one.

	input := trip.PlanInput{
		Origin:      origin,
		Destination: dest,
		DepartDate:  departDate,
		ReturnDate:  returnDate,
		Guests:      argInt(args, "guests", 1),
		Currency:    argString(args, "currency"),
	}

	result, err := trip.PlanTrip(ctx, input)
	if err != nil {
		return nil, nil, toolExecutionError("Trip planning", err)
	}

	data, _ := json.MarshalIndent(result, "", "  ")
	if !result.Success && (len(result.OutboundFlights) > 0 || len(result.ReturnFlights) > 0 || len(result.Hotels) > 0) {
		return []ContentBlock{
			{Type: "text", Text: fmt.Sprintf("Partial trip plan: %s", result.Error)},
			{Type: "text", Text: string(data)},
		}, result, nil
	}
	if !result.Success && result.Error != "" {
		return nil, nil, toolResultError("Trip planning", result.Error)
	}
	return []ContentBlock{{Type: "text", Text: string(data)}}, result, nil
}

// --- Optimize Trip Dates tool ---

func optimizeTripDatesOutputSchema() interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"success": schemaBool(),
			"best_date": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"depart_date": schemaString(),
					"return_date": schemaString(),
					"flight_cost": schemaNum(),
					"total_cost":  schemaNum(),
					"currency":    schemaString(),
					"savings":     schemaNum(),
				},
			},
			"options": schemaArray(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"depart_date": schemaString(),
					"return_date": schemaString(),
					"flight_cost": schemaNum(),
					"total_cost":  schemaNum(),
					"currency":    schemaString(),
					"savings":     schemaNum(),
				},
			}),
			"max_savings": schemaNum(),
			"currency":    schemaString(),
			"error":       schemaString(),
		},
		"required": []string{"success"},
	}
}

func optimizeTripDatesTool() ToolDef {
	return ToolDef{
		Name:        "optimize_trip_dates",
		Title:       "Optimize Trip Dates",
		Description: "Find the cheapest dates for a trip within a date range. Searches flight prices across the range and returns the optimal departure dates sorted by total cost.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"origin":      {Type: "string", Description: "Origin airport IATA code (e.g. HEL)"},
				"destination": {Type: "string", Description: "Destination airport IATA code or city name"},
				"from_date":   {Type: "string", Description: "Start of search range (YYYY-MM-DD)"},
				"to_date":     {Type: "string", Description: "End of search range (YYYY-MM-DD)"},
				"trip_length": {Type: "integer", Description: "Trip length in nights"},
				"guests":      {Type: "integer", Description: "Number of guests (default: 1)"},
				"currency":    {Type: "string", Description: "Target currency (e.g. EUR)"},
			},
			Required: []string{"origin", "destination", "from_date", "to_date", "trip_length"},
		},
		OutputSchema: optimizeTripDatesOutputSchema(),
		Annotations: &ToolAnnotations{
			Title:          "Optimize Trip Dates",
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  true,
		},
	}
}

func handleOptimizeTripDates(ctx context.Context, args map[string]any, elicit ElicitFunc, sampling SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	origin := strings.ToUpper(argString(args, "origin"))
	destination := argString(args, "destination")
	fromDate := argString(args, "from_date")
	toDate := argString(args, "to_date")
	tripLength := argInt(args, "trip_length", 0)
	guests := argInt(args, "guests", 1)
	currency := argString(args, "currency")

	if origin == "" || destination == "" {
		return nil, nil, fmt.Errorf("origin and destination are required")
	}
	if fromDate == "" || toDate == "" {
		return nil, nil, fmt.Errorf("from_date and to_date are required")
	}

	result, err := trip.OptimizeTripDates(ctx, trip.OptimizeTripDatesInput{
		Origin:      origin,
		Destination: destination,
		FromDate:    fromDate,
		ToDate:      toDate,
		TripLength:  tripLength,
		Guests:      guests,
		Currency:    currency,
	})
	if err != nil {
		return nil, nil, err
	}

	summary := optimizeTripDatesSummary(result, origin, destination)
	content, err := buildAnnotatedContentBlocks(summary, result)
	if err != nil {
		return nil, nil, err
	}

	return content, result, nil
}

func optimizeTripDatesSummary(result *trip.OptimizeTripDatesResult, origin, dest string) string {
	if !result.Success {
		if result.Error != "" {
			return fmt.Sprintf("Trip date optimization %s to %s failed: %s", origin, dest, result.Error)
		}
		return fmt.Sprintf("Could not find optimal dates from %s to %s.", origin, dest)
	}

	parts := []string{
		fmt.Sprintf("Found %d date options for %s -> %s", len(result.Options), origin, dest),
	}

	if result.BestDate != nil {
		parts = append(parts, fmt.Sprintf("Cheapest: depart %s, return %s at %s %.0f",
			result.BestDate.DepartDate, result.BestDate.ReturnDate,
			result.BestDate.Currency, result.BestDate.TotalCost))
	}
	if result.MaxSavings > 0 {
		parts = append(parts, fmt.Sprintf("Max savings: %s %.0f", result.Currency, result.MaxSavings))
	}

	return strings.Join(parts, ". ") + "."
}
