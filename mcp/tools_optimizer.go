package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/hacks"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/optimizer"
	"github.com/MikkoParkkola/trvl/internal/preferences"
	"github.com/MikkoParkkola/trvl/internal/trip"
)

func optimizeBookingTool() ToolDef {
	return ToolDef{
		Name:  "optimize_booking",
		Title: "Optimize Booking",
		Description: "Find the cheapest way to book a trip by searching alternative origins, " +
			"destinations, rail+fly stations, and applying all-in cost (baggage + FF status). " +
			"Returns ranked booking strategies with savings vs naive direct booking.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"origin":           {Type: "string", Description: "Origin IATA airport code (e.g. HEL)"},
				"destination":      {Type: "string", Description: "Destination IATA airport code or city (e.g. BCN)"},
				"departure_date":   {Type: "string", Description: "Departure date (YYYY-MM-DD)"},
				"return_date":      {Type: "string", Description: "Return date (YYYY-MM-DD); omit for one-way"},
				"flex_days":        {Type: "integer", Description: "Date flexibility +/-N days (default 3)"},
				"guests":           {Type: "integer", Description: "Number of passengers (default 1)"},
				"currency":         {Type: "string", Description: "Display currency (default: EUR)"},
				"max_results":      {Type: "integer", Description: "Top N results to return (default 5)"},
				"max_api_calls":    {Type: "integer", Description: "API call budget (default 15)"},
				"need_checked_bag": {Type: "boolean", Description: "Whether a checked bag is needed"},
				"carry_on_only":    {Type: "boolean", Description: "Carry-on only trip"},
			},
			Required: []string{"origin", "destination", "departure_date"},
		},
		OutputSchema: optimizeBookingOutputSchema(),
		Annotations: &ToolAnnotations{
			Title:          "Optimize Booking",
			ReadOnlyHint:   true,
			OpenWorldHint:  true,
			IdempotentHint: true,
		},
	}
}

func optimizeBookingOutputSchema() interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"success": schemaBool(),
			"error":   schemaString(),
			"options": schemaArray(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"rank":                schemaInt(),
					"strategy":            schemaString(),
					"base_cost":           schemaNum(),
					"bag_cost":            schemaNum(),
					"ff_savings":          schemaNum(),
					"transfer_cost":       schemaNum(),
					"all_in_cost":         schemaNum(),
					"currency":            schemaString(),
					"savings_vs_baseline": schemaNum(),
					"hacks_applied":       schemaStringArray(),
					"legs": schemaArray(map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"type":         schemaString(),
							"from":         schemaString(),
							"to":           schemaString(),
							"date":         schemaString(),
							"price":        schemaNum(),
							"currency":     schemaString(),
							"airline":      schemaString(),
							"duration_min": schemaInt(),
							"notes":        schemaString(),
						},
					}),
				},
			}),
			"baseline": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"all_in_cost": schemaNum(),
					"currency":    schemaString(),
				},
			},
		},
		"required": []string{"success"},
	}
}

func handleOptimizeBooking(ctx context.Context, args map[string]any, _ ElicitFunc, _ SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	// Multi-visit routing: when the caller flags two visits between the same
	// cities, delegate to the nested round-trip optimizer (MIK-3076). Window 1
	// is departure_date/return_date; window 2 is window2_depart/window2_return.
	if argBool(args, "multi_visit", false) {
		nestedArgs := map[string]any{
			"origin":         args["origin"],
			"destination":    args["destination"],
			"window1_depart": args["departure_date"],
			"window1_return": args["return_date"],
			"window2_depart": args["window2_depart"],
			"window2_return": args["window2_return"],
		}
		return optimizeNestedRT(ctx, nestedArgs, hacks.DefaultLegPricer)
	}

	input := optimizer.OptimizeInput{
		Origin:         strings.ToUpper(argString(args, "origin")),
		Destination:    strings.ToUpper(argString(args, "destination")),
		DepartDate:     argString(args, "departure_date"),
		ReturnDate:     argString(args, "return_date"),
		FlexDays:       argInt(args, "flex_days", 3),
		Guests:         argInt(args, "guests", 1),
		Currency:       argString(args, "currency"),
		MaxResults:     argInt(args, "max_results", 5),
		MaxAPICalls:    argInt(args, "max_api_calls", 15),
		NeedCheckedBag: argBool(args, "need_checked_bag", false),
		CarryOnOnly:    argBool(args, "carry_on_only", false),
	}

	// Load user preferences for FF statuses and home airports.
	if prefs, err := preferences.Load(); err == nil && prefs != nil {
		for _, ff := range prefs.FrequentFlyerPrograms {
			input.FFStatuses = append(input.FFStatuses, optimizer.FFStatus{
				Alliance: ff.Alliance,
				Tier:     ff.Tier,
			})
		}
		input.HomeAirports = prefs.HomeAirports
		if !input.CarryOnOnly && prefs.CarryOnOnly {
			input.CarryOnOnly = true
		}
	}

	if progress != nil {
		progress(0, 1, "Optimizing booking strategies...")
	}

	result, err := optimizer.Optimize(ctx, input)
	if err != nil {
		return nil, nil, fmt.Errorf("optimize_booking: %w", err)
	}

	// Build summary.
	var summary string
	if result.Success && len(result.Options) > 0 {
		best := result.Options[0]
		summary = fmt.Sprintf("Best: %s — %.0f %s all-in", best.Strategy, best.AllInCost, best.Currency)
		if best.SavingsVsBaseline > 0 {
			summary += fmt.Sprintf(" (saves %.0f %s vs direct)", best.SavingsVsBaseline, best.Currency)
		}
		summary += fmt.Sprintf(" | %d options found", len(result.Options))
	} else {
		summary = "No booking optimizations found"
		if result.Error != "" {
			summary += ": " + result.Error
		}
	}

	content, err := buildAnnotatedContentBlocks(summary, result)
	if err != nil {
		return nil, nil, err
	}
	return content, result, nil
}

// --- Suggest Dates tool ---

// suggestDatesOutputSchema returns the JSON Schema for SmartDateResult.
func suggestDatesOutputSchema() interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"success":     schemaBool(),
			"origin":      schemaString(),
			"destination": schemaString(),
			"cheapest_dates": schemaArray(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"date":        schemaString(),
					"day_of_week": schemaString(),
					"price":       schemaNum(),
					"currency":    schemaString(),
					"return_date": schemaString(),
				},
				"required": []string{"date", "price", "currency"},
			}),
			"average_price": schemaNum(),
			"currency":      schemaString(),
			"insights": schemaArray(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"type":        schemaString(),
					"description": schemaString(),
					"date":        schemaString(),
					"price":       schemaNum(),
					"savings":     schemaNum(),
				},
			}),
			"error": schemaString(),
		},
		"required": []string{"success"},
	}
}

func suggestDatesTool() ToolDef {
	return ToolDef{
		Name:        "suggest_dates",
		Title:       "Smart Date Suggestions",
		Description: "Analyze flight prices around a target date and suggest the cheapest travel dates. Returns the 3 cheapest dates, weekday vs weekend analysis, and actionable savings insights.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"origin":      {Type: "string", Description: "Departure airport IATA code (e.g., HEL, JFK)"},
				"destination": {Type: "string", Description: "Arrival airport IATA code (e.g., BCN, LHR)"},
				"target_date": {Type: "string", Description: "Center date to search around (YYYY-MM-DD)"},
				"flex_days":   {Type: "integer", Description: "Days of flexibility around target date (default: 7)"},
				"round_trip":  {Type: "boolean", Description: "Whether to search round-trip prices (default: false)"},
				"duration":    {Type: "integer", Description: "Trip duration in days for round-trip (default: 7)"},
			},
			Required: []string{"origin", "destination", "target_date"},
		},
		OutputSchema: suggestDatesOutputSchema(),
		Annotations: &ToolAnnotations{
			Title:          "Smart Date Suggestions",
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  true,
		},
	}
}

func handleSuggestDates(ctx context.Context, args map[string]any, elicit ElicitFunc, sampling SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	origin, dest, err := validateOriginDest(args)
	if err != nil {
		return nil, nil, err
	}

	targetDate := argString(args, "target_date")
	if targetDate == "" {
		return nil, nil, fmt.Errorf("target_date is required")
	}

	opts := trip.SmartDateOptions{
		TargetDate: targetDate,
		FlexDays:   argInt(args, "flex_days", 7),
		RoundTrip:  argBool(args, "round_trip", false),
		Duration:   argInt(args, "duration", 7),
	}

	result, err := trip.SuggestDates(ctx, origin, dest, opts)
	if err != nil {
		return nil, nil, err
	}

	summary := suggestDatesSummary(result, origin, dest)
	content, err := buildAnnotatedContentBlocks(summary, result)
	if err != nil {
		return nil, nil, err
	}

	return content, result, nil
}

func suggestDatesSummary(result *trip.SmartDateResult, origin, dest string) string {
	if !result.Success {
		if result.Error != "" {
			return fmt.Sprintf("Date suggestion %s to %s failed: %s", origin, dest, result.Error)
		}
		return fmt.Sprintf("Could not find date suggestions from %s to %s.", origin, dest)
	}

	parts := []string{
		fmt.Sprintf("Date analysis %s -> %s (avg %s %.0f)", origin, dest, result.Currency, result.AveragePrice),
	}

	for _, ins := range result.Insights {
		parts = append(parts, ins.Description)
	}

	return strings.Join(parts, ". ") + "."
}

// --- Optimize Multi-City tool ---

// multiCityOutputSchema returns the JSON Schema for MultiCityResult.
func multiCityOutputSchema() interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"success":       schemaBool(),
			"home_airport":  schemaString(),
			"optimal_order": schemaStringArray(),
			"segments": schemaArray(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"from":     schemaString(),
					"to":       schemaString(),
					"price":    schemaNum(),
					"currency": schemaString(),
				},
				"required": []string{"from", "to", "price", "currency"},
			}),
			"total_cost":           schemaNum(),
			"currency":             schemaString(),
			"worst_cost":           schemaNum(),
			"savings":              schemaNum(),
			"permutations_checked": schemaInt(),
			"error":                schemaString(),
		},
		"required": []string{"success"},
	}
}

func optimizeMultiCityTool() ToolDef {
	return ToolDef{
		Name:        "optimize_multi_city",
		Title:       "Multi-City Trip Optimizer",
		Description: "Find the cheapest routing order for visiting multiple cities. Tries all permutations (up to 6 cities) and returns the optimal visit order with per-segment prices.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"home_airport": {Type: "string", Description: "Home airport IATA code (e.g., HEL, JFK)"},
				"cities":       {Type: "string", Description: "Comma-separated list of city IATA codes to visit (e.g., BCN,ROM,PAR)"},
				"depart_date":  {Type: "string", Description: "Departure date (YYYY-MM-DD)"},
				"return_date":  {Type: "string", Description: "Return date (YYYY-MM-DD, optional)"},
			},
			Required: []string{"home_airport", "cities", "depart_date"},
		},
		OutputSchema: multiCityOutputSchema(),
		Annotations: &ToolAnnotations{
			Title:          "Multi-City Trip Optimizer",
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  true,
		},
	}
}

func handleOptimizeMultiCity(ctx context.Context, args map[string]any, elicit ElicitFunc, sampling SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	home := strings.ToUpper(argString(args, "home_airport"))
	citiesStr := argString(args, "cities")
	departDate := argString(args, "depart_date")
	returnDate := argString(args, "return_date")

	if home == "" {
		return nil, nil, fmt.Errorf("home_airport is required")
	}
	if citiesStr == "" {
		return nil, nil, fmt.Errorf("cities is required")
	}
	if departDate == "" {
		return nil, nil, fmt.Errorf("depart_date is required")
	}

	if err := models.ValidateIATA(home); err != nil {
		return nil, nil, fmt.Errorf("invalid home_airport: %w", err)
	}

	cities := argStringSlice(args, "cities")
	if len(cities) == 0 {
		return nil, nil, fmt.Errorf("at least one city is required")
	}
	for i, c := range cities {
		cities[i] = strings.ToUpper(strings.TrimSpace(c))
		if err := models.ValidateIATA(cities[i]); err != nil {
			return nil, nil, fmt.Errorf("invalid city %q: %w", c, err)
		}
	}

	opts := trip.MultiCityOptions{
		DepartDate: departDate,
		ReturnDate: returnDate,
	}

	result, err := trip.OptimizeMultiCity(ctx, home, cities, opts)
	if err != nil {
		return nil, nil, err
	}

	summary := multiCitySummary(result)
	content, err := buildAnnotatedContentBlocks(summary, result)
	if err != nil {
		return nil, nil, err
	}

	return content, result, nil
}

func multiCitySummary(result *trip.MultiCityResult) string {
	if !result.Success {
		if result.Error != "" {
			return fmt.Sprintf("Multi-city optimization failed: %s", result.Error)
		}
		return "Could not optimize multi-city routing."
	}

	route := append([]string{result.HomeAirport}, result.OptimalOrder...)
	route = append(route, result.HomeAirport)
	routeStr := strings.Join(route, " -> ")

	summary := fmt.Sprintf("Optimal route: %s. Total: %s %.0f.", routeStr, result.Currency, result.TotalCost)
	if result.Savings > 0 {
		summary += fmt.Sprintf(" Saves %s %.0f vs worst order.", result.Currency, result.Savings)
	}

	return summary
}
