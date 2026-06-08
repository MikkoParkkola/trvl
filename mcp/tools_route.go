package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/route"
)

func searchRouteTool() ToolDef {
	return ToolDef{
		Name:        "search_route",
		Title:       "Multi-Modal Route Search",
		Description: "Find optimal multi-modal itineraries combining flights, trains, buses, and ferries. Searches hub cities across Europe for transfer connections. Returns Pareto-optimal options ranked by price, duration, or transfers.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"origin":                  {Type: "string", Description: "Origin city name or IATA code (e.g. Helsinki, HEL)"},
				"destination":             {Type: "string", Description: "Destination city name or IATA code (e.g. Dubrovnik, DBV)"},
				"date":                    {Type: "string", Description: "Travel date (YYYY-MM-DD)"},
				"depart_after":            {Type: "string", Description: "Earliest departure time (HH:MM or ISO 8601)"},
				"arrive_by":               {Type: "string", Description: "Latest arrival time (HH:MM or ISO 8601)"},
				"max_transfers":           {Type: "integer", Description: "Maximum mode changes (default: 3)"},
				"max_price":               {Type: "number", Description: "Maximum total price (0 = no limit)"},
				"prefer":                  {Type: "string", Description: "Preferred transport mode: train, bus, ferry, flight"},
				"avoid":                   {Type: "string", Description: "Avoid transport mode: flight, bus, train, ferry"},
				"currency":                {Type: "string", Description: "Display currency (default: EUR)"},
				"sort":                    {Type: "string", Description: "Sort by: price, duration, transfers (default: price)"},
				"allow_browser_fallbacks": {Type: "boolean", Description: "Allow browser/curl/cookie-assisted ground-provider fallbacks (default: false)"},
			},
			Required: []string{"origin", "destination", "date"},
		},
		OutputSchema: routeSearchOutputSchema(),
		Annotations: &ToolAnnotations{
			Title:          "Multi-Modal Route Search",
			ReadOnlyHint:   true,
			OpenWorldHint:  true,
			IdempotentHint: true,
		},
	}
}

func routeSearchOutputSchema() interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"success":     schemaBool(),
			"origin":      schemaString(),
			"destination": schemaString(),
			"date":        schemaString(),
			"count":       schemaInt(),
			"itineraries": routeItinerariesOutputSchema(),
			"error":       schemaString(),
		},
		"required": []string{"success", "origin", "destination", "date", "count"},
	}
}

func routeItinerariesOutputSchema() interface{} {
	return schemaArray(map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"legs":           schemaArray(map[string]interface{}{"type": "object"}),
			"total_price":    schemaNum(),
			"currency":       schemaString(),
			"total_duration": schemaInt(),
			"transfers":      schemaInt(),
			"depart_time":    schemaString(),
			"arrive_time":    schemaString(),
		},
	})
}

func handleSearchRoute(ctx context.Context, args map[string]any, elicit ElicitFunc, sampling SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	origin := argString(args, "origin")
	dest := argString(args, "destination")
	date := argString(args, "date")

	if origin == "" || dest == "" || date == "" {
		return nil, nil, fmt.Errorf("origin, destination, and date are required")
	}

	sendProgress(progress, 0, 100, fmt.Sprintf("Resolving %s and %s...", origin, dest))

	opts := route.Options{
		DepartAfter:           argString(args, "depart_after"),
		ArriveBy:              argString(args, "arrive_by"),
		MaxTransfers:          argInt(args, "max_transfers", 3),
		MaxPrice:              argFloat(args, "max_price", 0),
		Currency:              argString(args, "currency"),
		Prefer:                argString(args, "prefer"),
		Avoid:                 argString(args, "avoid"),
		SortBy:                argString(args, "sort"),
		AllowBrowserFallbacks: argBool(args, "allow_browser_fallbacks", false),
	}

	sendProgress(progress, 10, 100, "Searching direct connections...")
	sendProgress(progress, 20, 100, "Searching hub connections (flights, trains, ferries)...")

	result, err := route.SearchRoute(ctx, origin, dest, date, opts)
	if err != nil {
		return nil, nil, toolExecutionError("Route search", err)
	}

	sendProgress(progress, 90, 100, "Ranking itineraries...")

	if !result.Success {
		msg := fmt.Sprintf("No multi-modal routes found from %s to %s on %s", result.Origin, result.Destination, date)
		if result.Error != "" {
			msg += ": " + result.Error
		}
		sendProgress(progress, 100, 100, "Done")
		return []ContentBlock{{Type: "text", Text: msg}}, result, nil
	}

	sendProgress(progress, 100, 100, fmt.Sprintf("Found %d routes", result.Count))

	summary := buildRouteSummary(result)
	content, err := buildAnnotatedContentBlocks(summary, result)
	if err != nil {
		return nil, nil, err
	}
	return content, result, nil
}

// sendProgress calls progress if non-nil. Fire-and-forget; nil-safe.
func sendProgress(progress ProgressFunc, current, total float64, message string) {
	if progress != nil {
		progress(current, total, message)
	}
}

func buildRouteSummary(result *models.RouteSearchResult) string {
	var sb strings.Builder
	_, _ = fmt.Fprintf(&sb, "Found %d multi-modal routes from %s to %s on %s\n\n",
		result.Count, result.Origin, result.Destination, result.Date)

	for i, it := range result.Itineraries {
		if i >= 10 {
			_, _ = fmt.Fprintf(&sb, "... and %d more\n", result.Count-10)
			break
		}

		transferStr := "direct"
		if it.Transfers > 0 {
			transferStr = fmt.Sprintf("%d transfers", it.Transfers)
		}

		_, _ = fmt.Fprintf(&sb, "Option %d: %s %.0f · %dh%02dm · %s\n",
			i+1, it.Currency, it.TotalPrice,
			it.TotalDuration/60, it.TotalDuration%60,
			transferStr)

		for _, leg := range it.Legs {
			price := "-"
			if leg.Price > 0 {
				price = fmt.Sprintf("%s %.0f", leg.Currency, leg.Price)
			}
			_, _ = fmt.Fprintf(&sb, "  %s %s → %s (%s) %s\n",
				leg.Mode, leg.From, leg.To, leg.Provider, price)
		}
		sb.WriteByte('\n')
	}

	return sb.String()
}
