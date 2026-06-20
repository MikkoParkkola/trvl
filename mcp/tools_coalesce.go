package mcp

// Innovation #5: unified trip coalescer MCP tool. Fans flights, hotels, and
// ground search out concurrently for a single trip and returns one combined
// plan with a floor cost estimate. Reuses the existing search engines via
// internal/tripcoalesce; one domain failing yields a partial plan, never an
// aborted call.

import (
	"context"
	"fmt"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/tripcoalesce"
)

func coalesceTripTool() ToolDef {
	return ToolDef{
		Name:        "coalesce_trip",
		Title:       "Unified Trip Coalescer",
		Description: "Plan one origin→destination trip by fanning trvl's flight, hotel, and ground searches out concurrently and assembling a single combined plan: the cheapest priced option per domain plus a floor total-cost estimate. Each domain is failure-isolated — one provider failing or timing out yields a partial plan with the others intact, never a fabricated empty.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"origin":                  {Type: "string", Description: "Origin IATA airport code (e.g. HEL); also seeds the ground 'from'"},
				"destination":             {Type: "string", Description: "Destination IATA airport code (e.g. LHR); also seeds the ground 'to' and hotel location"},
				"depart_date":             {Type: "string", Description: "Departure date (YYYY-MM-DD)"},
				"return_date":             {Type: "string", Description: "Return date (YYYY-MM-DD), optional; used for round-trip flights and hotel checkout"},
				"hotel_location":          {Type: "string", Description: "Hotel search location override (default: destination)"},
				"nights":                  {Type: "integer", Description: "Nights for the hotel cost estimate (0 = per-night only)"},
				"travelers":               {Type: "integer", Description: "Number of travelers (default 1)"},
				"currency":                {Type: "string", Description: "Trip currency (default EUR)"},
				"allow_browser_fallbacks": {Type: "boolean", Description: "Allow browser/cookie-assisted ground providers (default false)"},
			},
			Required: []string{"origin", "destination", "depart_date"},
		},
		OutputSchema: coalesceTripOutputSchema(),
		Annotations: &ToolAnnotations{
			Title:          "Unified Trip Coalescer",
			ReadOnlyHint:   true,
			OpenWorldHint:  true,
			IdempotentHint: true,
		},
	}
}

func coalesceTripOutputSchema() interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"origin":              schemaString(),
			"destination":         schemaString(),
			"depart_date":         schemaString(),
			"return_date":         schemaString(),
			"currency":            schemaString(),
			"total_cost_estimate": schemaNum(),
			"cost_breakdown": schemaArray(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"domain":   schemaString(),
					"label":    schemaString(),
					"amount":   schemaNum(),
					"currency": schemaString(),
				},
			}),
			"statuses": schemaArray(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"domain":     schemaString(),
					"ok":         schemaBool(),
					"count":      schemaInt(),
					"error":      schemaString(),
					"elapsed_ms": schemaInt(),
				},
			}),
			"notes": schemaStringArray(),
		},
		"required": []string{"origin", "destination", "depart_date", "currency", "total_cost_estimate", "statuses"},
	}
}

func handleCoalesceTrip(ctx context.Context, args map[string]any, _ ElicitFunc, _ SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	origin := strings.ToUpper(argString(args, "origin"))
	destination := strings.ToUpper(argString(args, "destination"))
	departDate := argString(args, "depart_date")
	if origin == "" || destination == "" || departDate == "" {
		return nil, nil, fmt.Errorf("origin, destination, and depart_date are required")
	}

	sendProgress(progress, 0, 100, fmt.Sprintf("Coalescing flights + hotels + ground for %s→%s...", origin, destination))

	plan := tripcoalesce.Plan(ctx, tripcoalesce.Params{
		Origin:                origin,
		Destination:           destination,
		DepartDate:            departDate,
		ReturnDate:            argString(args, "return_date"),
		HotelLocation:         argString(args, "hotel_location"),
		Nights:                argInt(args, "nights", 0),
		Travelers:             argInt(args, "travelers", 1),
		Currency:              argString(args, "currency"),
		AllowBrowserFallbacks: argBool(args, "allow_browser_fallbacks", false),
	})

	okCount := 0
	for _, s := range plan.Statuses {
		if s.OK {
			okCount++
		}
	}
	sendProgress(progress, 100, 100, fmt.Sprintf("%d/%d domains returned", okCount, len(plan.Statuses)))

	if plan.Notes == nil {
		plan.Notes = []string{}
	}

	summary := buildCoalesceTripSummary(plan)
	content, err := buildAnnotatedContentBlocks(summary, plan)
	if err != nil {
		return nil, nil, err
	}
	return content, plan, nil
}

func buildCoalesceTripSummary(plan *tripcoalesce.TripPlan) string {
	var sb strings.Builder
	_, _ = fmt.Fprintf(&sb, "Trip plan %s→%s on %s (%s):\n\n", plan.Origin, plan.Destination, plan.DepartDate, plan.Currency)

	if plan.CheapestFlight != nil {
		_, _ = fmt.Fprintf(&sb, "✈ Flights: cheapest %s %.2f (%d stops)\n",
			plan.CheapestFlight.Currency, plan.CheapestFlight.Price, plan.CheapestFlight.Stops)
	} else {
		_, _ = fmt.Fprintf(&sb, "✈ Flights: %s\n", coalesceDomainNote(plan, "flights"))
	}
	if plan.CheapestHotel != nil {
		_, _ = fmt.Fprintf(&sb, "🏨 Hotels: cheapest %s %.2f/night (%s)\n",
			plan.CheapestHotel.Currency, plan.CheapestHotel.Price, plan.CheapestHotel.Name)
	} else {
		_, _ = fmt.Fprintf(&sb, "🏨 Hotels: %s\n", coalesceDomainNote(plan, "hotels"))
	}
	if plan.CheapestGround != nil {
		_, _ = fmt.Fprintf(&sb, "🚆 Ground: cheapest %s %.2f via %s\n",
			plan.CheapestGround.Currency, plan.CheapestGround.Price, plan.CheapestGround.Provider)
	} else {
		_, _ = fmt.Fprintf(&sb, "🚆 Ground: %s\n", coalesceDomainNote(plan, "ground"))
	}

	if plan.TotalCostEstimate > 0 {
		_, _ = fmt.Fprintf(&sb, "\nFloor estimate: %s %.2f\n", plan.Currency, plan.TotalCostEstimate)
	}
	if len(plan.Notes) > 0 {
		_, _ = fmt.Fprintf(&sb, "\nNotes: %s\n", strings.Join(plan.Notes, "; "))
	}
	return sb.String()
}

func coalesceDomainNote(plan *tripcoalesce.TripPlan, domain string) string {
	for _, s := range plan.Statuses {
		if s.Domain != domain {
			continue
		}
		if s.Error != "" {
			return "unavailable (" + s.Error + ")"
		}
		if !s.OK {
			return "unavailable"
		}
		return "no priced results"
	}
	return "no results"
}
