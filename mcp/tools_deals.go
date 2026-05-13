package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/deals"
)

func searchDealsTool() ToolDef {
	return ToolDef{
		Name:        "search_deals",
		Title:       "Travel Deals Search",
		Description: "Search travel deals from free RSS feeds (Secret Flying, Fly4Free, Holiday Pirates, The Points Guy). Returns error fares, flash sales, and package deals. No API key required.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"origins":   {Type: "string", Description: "Comma-separated origin airports or cities to filter by (e.g. HEL,AMS)"},
				"max_price": {Type: "number", Description: "Maximum price filter (0 = no limit)"},
				"type":      {Type: "string", Description: "Filter by deal type: error_fare, deal, flash_sale, package"},
				"hours":     {Type: "number", Description: "Only show deals from last N hours (default: 48)"},
			},
			Required: []string{"origins"},
		},
		OutputSchema: dealsSearchOutputSchema(),
		Annotations: &ToolAnnotations{
			Title:          "Travel Deals Search",
			ReadOnlyHint:   true,
			OpenWorldHint:  true,
			IdempotentHint: true,
		},
	}
}

func dealsSearchOutputSchema() interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"success": schemaBool(),
			"count":   schemaInt(),
			"deals": schemaArray(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"title":       schemaString(),
					"price":       schemaNum(),
					"currency":    schemaString(),
					"origin":      schemaString(),
					"destination": schemaString(),
					"airline":     schemaString(),
					"type":        schemaString(),
					"source":      schemaString(),
					"url":         schemaString(),
					"published":   schemaString(),
					"summary":     schemaString(),
				},
			}),
			"error": schemaString(),
		},
		"required": []string{"success", "count"},
	}
}

func handleSearchDeals(ctx context.Context, args map[string]any, elicit ElicitFunc, sampling SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	originsRaw := argString(args, "origins")
	if originsRaw == "" {
		return nil, nil, fmt.Errorf("origins is required")
	}

	filter := deals.DealFilter{
		MaxPrice: argFloat(args, "max_price", 0),
		Type:     argString(args, "type"),
		HoursAgo: argInt(args, "hours", 48),
	}

	origins := strings.Split(originsRaw, ",")
	for i, o := range origins {
		origins[i] = strings.TrimSpace(o)
	}
	filter.Origins = origins

	result, err := deals.FetchDeals(ctx, nil, filter)
	if err != nil {
		return nil, nil, toolExecutionError("Deals search", err)
	}

	if !result.Success {
		if result.Error != "" {
			return nil, nil, toolResultError("Deals search", result.Error)
		}
		msg := fmt.Sprintf("No deals found for origins %s", originsRaw)
		return []ContentBlock{{Type: "text", Text: msg}}, result, nil
	}

	// Build summary.
	var sb strings.Builder
	_, _ = fmt.Fprintf(&sb, "Found %d travel deals for %s:\n\n", result.Count, originsRaw)

	limit := result.Count
	if limit > 10 {
		limit = 10
	}
	for i, d := range result.Deals[:limit] {
		price := "-"
		if d.Price > 0 {
			price = fmt.Sprintf("%s %.0f", d.Currency, d.Price)
		}
		route := ""
		if d.Origin != "" && d.Destination != "" {
			route = fmt.Sprintf("%s->%s", d.Origin, d.Destination)
		}
		_, _ = fmt.Fprintf(&sb, "%d. **%s** %s | %s | %s", i+1, price, route, d.Type, d.Source)
		if d.Airline != "" {
			_, _ = fmt.Fprintf(&sb, " | %s", d.Airline)
		}
		sb.WriteString("\n")
		if d.Title != "" {
			_, _ = fmt.Fprintf(&sb, "   %s\n", d.Title)
		}
	}
	if result.Count > 10 {
		_, _ = fmt.Fprintf(&sb, "\n... and %d more deals", result.Count-10)
	}

	content := []ContentBlock{
		{Type: "text", Text: sb.String(), Annotations: &ContentAnnotation{Audience: []string{"user"}, Priority: 1.0}},
		{Type: "text", Text: "Structured data attached.", Annotations: &ContentAnnotation{Audience: []string{"assistant"}, Priority: 0.5}},
	}

	return content, result, nil
}
