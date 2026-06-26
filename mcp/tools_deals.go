package mcp

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/deals"
	"github.com/MikkoParkkola/trvl/internal/destinations"
)

func searchDealsTool() ToolDef {
	return ToolDef{
		Name:        "search_deals",
		Title:       "Travel Deals Search",
		Description: "Search travel deals from free RSS feeds (Secret Flying, Fly4Free, Holiday Pirates, The Points Guy). Returns error fares, flash sales, and package deals. No API key required.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"origins":   {Type: "string", Description: "Comma-separated origin airports or cities to filter by (e.g. HEL,AMS). Empty/omitted returns deals from all origins."},
				"max_price": {Type: "number", Description: "Maximum price filter (0 = no limit)"},
				"type":      {Type: "string", Description: "Filter by deal type: error_fare, deal, flash_sale, package"},
				"hours":     {Type: "number", Description: "Only show deals from last N hours (default: 48)"},
				"currency":  {Type: "string", Description: "Convert deal prices to this currency (e.g. EUR, USD). Empty = leave prices as-is"},
			},
			Required: []string{},
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
	currency := argString(args, "currency")

	filter := deals.DealFilter{
		MaxPrice: argFloat(args, "max_price", 0),
		Type:     argString(args, "type"),
		HoursAgo: argInt(args, "hours", 48),
	}

	// Empty/absent origins => nil Origins => deals from all origins (mirror CLI --from).
	filter.Origins = parseOrigins(originsRaw)

	result, err := deals.FetchDeals(ctx, nil, filter)
	if err != nil {
		return nil, nil, toolExecutionError("Deals search", err)
	}

	if !result.Success {
		if result.Error != "" {
			return nil, nil, toolResultError("Deals search", result.Error)
		}
		msg := "No deals found"
		if originsRaw != "" {
			msg = fmt.Sprintf("No deals found for origins %s", originsRaw)
		}
		return []ContentBlock{{Type: "text", Text: msg}}, result, nil
	}

	// Convert prices when a target currency is requested (mirror CLI --currency).
	convertDealPrices(ctx, currency, result)

	// Build summary.
	var sb strings.Builder
	scope := "all origins"
	if originsRaw != "" {
		scope = originsRaw
	}
	_, _ = fmt.Fprintf(&sb, "Found %d travel deals for %s:\n\n", result.Count, scope)

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

	content, err := buildAnnotatedContentBlocks(sb.String(), result)
	if err != nil {
		return nil, nil, err
	}

	return content, result, nil
}

// parseOrigins splits a comma-separated origins argument into trimmed entries,
// mirroring the CLI `--from` flag. An empty/absent value returns nil so the deal
// filter applies no origin restriction (deals from all origins).
func parseOrigins(raw string) []string {
	if raw == "" {
		return nil
	}
	origins := strings.Split(raw, ",")
	for i, o := range origins {
		origins[i] = strings.TrimSpace(o)
	}
	return origins
}

// convertDealPrices rewrites deal prices into targetCurrency in place, mirroring
// the CLI's `--currency` path (cmd/trvl/deals.go). A deal is converted only when
// it has a positive price in a different currency; conversion failures leave the
// original price/currency untouched via destinations.ConvertCurrency. An empty
// targetCurrency is a no-op so callers can pass through unconditionally.
func convertDealPrices(ctx context.Context, targetCurrency string, result *deals.DealsResult) {
	if targetCurrency == "" || result == nil {
		return
	}
	for i := range result.Deals {
		d := &result.Deals[i]
		if d.Currency != targetCurrency && d.Price > 0 {
			converted, cur := destinations.ConvertCurrency(ctx, d.Price, d.Currency, targetCurrency)
			d.Price = math.Round(converted)
			d.Currency = cur
		}
	}
}
