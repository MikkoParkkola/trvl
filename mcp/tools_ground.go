package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/ground"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/profile"
	"github.com/MikkoParkkola/trvl/internal/transfer"
	"github.com/MikkoParkkola/trvl/internal/trip"
)

func searchGroundTool() ToolDef {
	return ToolDef{
		Name:        "search_ground",
		Title:       "Ground Transport Search",
		Description: "Search bus, train, and ferry connections between cities. Uses API-first providers across Europe, with browser/curl-assisted fallbacks disabled unless explicitly enabled.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"from":                    {Type: "string", Description: "Departure city name (e.g. Prague, Helsinki, Vienna)"},
				"to":                      {Type: "string", Description: "Arrival city name"},
				"date":                    {Type: "string", Description: "Departure date (YYYY-MM-DD)"},
				"currency":                {Type: "string", Description: "Price currency (default: EUR)"},
				"type":                    {Type: "string", Description: "Filter: bus, train, ferry, or empty for all"},
				"max_price":               {Type: "number", Description: "Maximum price filter (0 = no limit)"},
				"provider":                {Type: "string", Description: "Restrict to provider: flixbus, regiojet, trainline, sncf, transitous, db, oebb, ns, vr, tallink, dfds, vikingline, eckeroline, ferryhopper"},
				"allow_browser_fallbacks": {Type: "boolean", Description: "Allow browser/curl/cookie-assisted provider fallbacks (default: false)"},
			},
			Required: []string{"from", "to", "date"},
		},
		OutputSchema: groundSearchOutputSchema(),
		Annotations: &ToolAnnotations{
			Title:          "Ground Transport Search",
			ReadOnlyHint:   true,
			OpenWorldHint:  true,
			IdempotentHint: true,
		},
	}
}

func groundSearchOutputSchema() interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"success": schemaBool(),
			"count":   schemaInt(),
			"routes":  groundRoutesOutputSchema(),
			"error":   schemaString(),
		},
		"required": []string{"success", "count"},
	}
}

func groundRoutesOutputSchema() interface{} {
	return schemaArray(map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"provider":         schemaString(),
			"type":             schemaString(),
			"price":            schemaNum(),
			"price_max":        schemaNum(),
			"currency":         schemaString(),
			"duration_minutes": schemaInt(),
			"transfers":        schemaInt(),
			"amenities":        schemaStringArray(),
			"seats_left":       schemaInt(),
			"booking_url":      schemaString(),
		},
	})
}

func searchAirportTransfersTool() ToolDef {
	return ToolDef{
		Name:        "search_airport_transfers",
		Title:       "Airport Transfer Search",
		Description: "Search airport-to-hotel or airport-to-city ground transport. Lists exact airport routing first, adds taxi fare estimates, then broader city-level providers.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"airport_code": {Type: "string", Description: "Arrival airport IATA code (e.g. CDG, LHR, FCO)"},
				"destination":  {Type: "string", Description: "Hotel, address, district, or city destination"},
				"date":         {Type: "string", Description: "Travel date (YYYY-MM-DD)"},
				"arrival_time": {Type: "string", Description: "Only include routes departing at or after this local time (HH:MM)"},
				"currency":     {Type: "string", Description: "Price currency (default: EUR)"},
				"type":         {Type: "string", Description: "Filter: bus, train, taxi, tram, metro, mixed, or empty for all"},
				"max_price":    {Type: "number", Description: "Maximum price filter (0 = no limit)"},
				"provider":     {Type: "string", Description: "Restrict to provider: transitous, taxi, flixbus, regiojet, eurostar, db, sncf, trainline"},
			},
			Required: []string{"airport_code", "destination", "date"},
		},
		OutputSchema: airportTransferOutputSchema(),
		Annotations: &ToolAnnotations{
			Title:          "Airport Transfer Search",
			ReadOnlyHint:   true,
			OpenWorldHint:  true,
			IdempotentHint: true,
		},
	}
}

func airportTransferOutputSchema() interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"success":          schemaBool(),
			"airport_code":     schemaString(),
			"airport":          schemaString(),
			"airport_city":     schemaString(),
			"destination":      schemaString(),
			"destination_city": schemaString(),
			"date":             schemaString(),
			"arrival_time":     schemaString(),
			"count":            schemaInt(),
			"exact_matches":    schemaInt(),
			"city_matches":     schemaInt(),
			"routes":           groundRoutesOutputSchema(),
			"error":            schemaString(),
		},
		"required": []string{"success", "airport_code", "airport", "airport_city", "destination", "date", "count", "exact_matches", "city_matches"},
	}
}

func handleSearchGround(ctx context.Context, args map[string]any, elicit ElicitFunc, sampling SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	from := models.ResolveLocationName(argString(args, "from"))
	to := models.ResolveLocationName(argString(args, "to"))
	date := argString(args, "date")

	if from == "" || to == "" || date == "" {
		return nil, nil, fmt.Errorf("from, to, and date are required")
	}

	opts := ground.SearchOptions{
		Currency:              argString(args, "currency"),
		MaxPrice:              argFloat(args, "max_price", 0),
		Type:                  argString(args, "type"),
		AllowBrowserFallbacks: argBool(args, "allow_browser_fallbacks", false),
	}
	if p := argString(args, "provider"); p != "" {
		opts.Providers = strings.Split(p, ",")
	}

	// Apply profile hint for preferred transport mode when the caller has not
	// specified one explicitly.
	if _, explicit := args["type"]; !explicit && opts.Type == "" {
		prof, _ := profile.Load()
		hints := profile.GroundHints(prof, from, to)
		opts.Type = hints.PreferredType
	}

	result, err := ground.SearchByName(ctx, from, to, date, opts)
	if err != nil {
		return nil, nil, toolExecutionError("Ground transport search", err)
	}

	if !result.Success {
		if result.Error != "" {
			return nil, nil, toolResultError("Ground transport search", result.Error)
		}
		msg := fmt.Sprintf("No ground routes found from %s to %s on %s", from, to, date)
		return []ContentBlock{{Type: "text", Text: msg}}, result, nil
	}

	summary := buildGroundRouteSummary(
		fmt.Sprintf("Found %d ground routes from %s to %s on %s", result.Count, from, to, date),
		result.Routes,
	)

	content := []ContentBlock{
		{Type: "text", Text: summary, Annotations: &ContentAnnotation{Audience: []string{"user"}, Priority: 1.0}},
		{Type: "text", Text: "Structured data attached.", Annotations: &ContentAnnotation{Audience: []string{"assistant"}, Priority: 0.5}},
	}

	return content, result, nil
}

func handleSearchAirportTransfers(ctx context.Context, args map[string]any, elicit ElicitFunc, sampling SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	input := trip.AirportTransferInput{
		AirportCode: argString(args, "airport_code"),
		Destination: argString(args, "destination"),
		Date:        argString(args, "date"),
		ArrivalTime: argString(args, "arrival_time"),
		Currency:    argString(args, "currency"),
		MaxPrice:    argFloat(args, "max_price", 0),
		Type:        argString(args, "type"),
	}
	if p := argString(args, "provider"); p != "" {
		input.Providers = strings.Split(p, ",")
	}
	if input.AirportCode == "" || input.Destination == "" || input.Date == "" {
		return nil, nil, fmt.Errorf("airport_code, destination, and date are required")
	}

	result, err := trip.SearchAirportTransfers(ctx, input)
	if err != nil {
		return nil, nil, err
	}
	if !result.Success {
		msg := fmt.Sprintf("No airport transfer routes found from %s to %s on %s", result.Airport, result.Destination, result.Date)
		if result.Error != "" {
			msg += ": " + result.Error
		}
		return []ContentBlock{{Type: "text", Text: msg}}, result, nil
	}

	summary := buildGroundRouteSummary(
		fmt.Sprintf("Found %d airport transfer routes from %s to %s on %s", result.Count, result.Airport, result.Destination, result.Date),
		result.Routes,
	)
	if result.ArrivalTime != "" {
		summary += fmt.Sprintf("\n\nFiltered to departures at or after %s.", result.ArrivalTime)
	}
	if result.CityMatches > 0 {
		summary += fmt.Sprintf("\n\nExact airport matches are listed first (%d exact, %d broader city).", result.ExactMatches, result.CityMatches)
	}
	if groundRoutesHaveProvider(result.Routes, "taxi") {
		summary += "\n\nTaxi fares are estimates based on route distance and typical local tariffs."
	}

	// Door-to-door comparison card (Option C): enrich the raw routes into a
	// choosable set of modes with time/price/pros/cons/grounded steps and
	// cheapest/fastest/best-value/luggage labels. Additive + backward-compatible.
	comparison := transfer.BuildOptions(result.Routes, result.AirportCode, result.Airport, result.Destination)
	if len(comparison.Options) > 1 {
		summary += "\n\nCompare modes and choose what suits you — see cheapest / fastest / best-value / most-luggage-friendly in the structured 'comparison'."
	}
	response := airportTransferResponse{AirportTransferResult: result, Comparison: comparison}

	content, err := buildAnnotatedContentBlocks(summary, result)
	if err != nil {
		return nil, nil, err
	}
	return content, response, nil
}

// airportTransferResponse extends the legacy AirportTransferResult with the
// door-to-door comparison card. The embedded pointer promotes all original
// JSON fields, so existing consumers are unaffected; `comparison` is additive.
type airportTransferResponse struct {
	*trip.AirportTransferResult
	Comparison models.TransferComparison `json:"comparison"`
}

func groundRoutesHaveProvider(routes []models.GroundRoute, provider string) bool {
	for _, route := range routes {
		if strings.EqualFold(route.Provider, provider) {
			return true
		}
	}
	return false
}

func buildGroundRouteSummary(header string, routes []models.GroundRoute) string {
	var sb strings.Builder
	sb.WriteString(header)
	sb.WriteString(":\n\n")

	limit := len(routes)
	if limit > 10 {
		limit = 10
	}
	for i, r := range routes[:limit] {
		price := fmt.Sprintf("%s %.2f", r.Currency, r.Price)
		if r.Price <= 0 {
			price = "price unavailable"
		} else if r.PriceMax > 0 && r.PriceMax != r.Price {
			price = fmt.Sprintf("%s %.2f-%.2f", r.Currency, r.Price, r.PriceMax)
		}
		dur := fmt.Sprintf("%dh%02dm", r.Duration/60, r.Duration%60)
		transfers := "direct"
		if r.Transfers > 0 {
			transfers = fmt.Sprintf("%d transfers", r.Transfers)
		}

		depTime := safeTimeSlice(r.Departure.Time)
		arrTime := safeTimeSlice(r.Arrival.Time)

		_, _ = fmt.Fprintf(&sb, "%d. **%s** %s | %s | %s | %s %s→%s",
			i+1, price, r.Type, dur, transfers, r.Provider, depTime, arrTime)

		if r.SeatsLeft != nil && *r.SeatsLeft <= 10 {
			_, _ = fmt.Fprintf(&sb, " | %d seats left", *r.SeatsLeft)
		}
		if len(r.Amenities) > 0 {
			_, _ = fmt.Fprintf(&sb, " | %s", strings.Join(r.Amenities, ", "))
		}
		sb.WriteString("\n")
	}
	if len(routes) > 10 {
		_, _ = fmt.Fprintf(&sb, "\n... and %d more routes", len(routes)-10)
	}
	return sb.String()
}

// safeTimeSlice extracts HH:MM from an ISO 8601 timestamp, or returns the raw string.
func safeTimeSlice(t string) string {
	if len(t) >= 16 {
		return t[11:16]
	}
	return t
}
