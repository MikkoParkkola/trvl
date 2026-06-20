package mcp

// Innovation #8: unified arbitrage report MCP tool. Aggregates trvl's
// currency, cabin-class, and hotel arbitrage engines into one ranked report.

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/arbreport"
	"github.com/MikkoParkkola/trvl/internal/cabinarb"
	"github.com/MikkoParkkola/trvl/internal/hotelarb"
)

func arbitrageReportTool() ToolDef {
	return ToolDef{
		Name:        "arbitrage_report",
		Title:       "Unified Arbitrage Report",
		Description: "Aggregate trvl's arbitrage engines (airline currency arbitrage, cabin-class upgrades, hotel rate re-book) into one ranked report for a trip. Each opportunity carries type, description, estimated saving, currency, and confidence. Engines that lack inputs for the trip context are skipped and listed, never fabricated.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"origin":      {Type: "string", Description: "Origin IATA airport code (e.g. HEL)"},
				"destination": {Type: "string", Description: "Destination IATA airport code (e.g. LHR)"},
				"depart_date": {Type: "string", Description: "Departure date (YYYY-MM-DD); required for the currency engine"},
				"return_date": {Type: "string", Description: "Return date (YYYY-MM-DD), optional"},
				"currency":    {Type: "string", Description: "Trip currency (default EUR)"},
				"travelers":   {Type: "integer", Description: "Number of travelers (default 1)"},
				"cabin_fares": {Type: "array", Description: "Cabin fare ladder entries as 'cabin:price[:carrier]' (cabins: economy, premium_economy, business, first). Enables the cabin engine.", Items: &Property{Type: "string"}},
				"rebooks":     {Type: "array", Description: "Hotel re-book candidates as 'name:original:current[:currency]'. Enables the hotel engine.", Items: &Property{Type: "string"}},
			},
			Required: []string{"origin", "destination", "depart_date"},
		},
		OutputSchema: arbitrageReportOutputSchema(),
		Annotations: &ToolAnnotations{
			Title:          "Unified Arbitrage Report",
			ReadOnlyHint:   true,
			OpenWorldHint:  true,
			IdempotentHint: true,
		},
	}
}

func arbitrageReportOutputSchema() interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"origin":      schemaString(),
			"destination": schemaString(),
			"depart_date": schemaString(),
			"return_date": schemaString(),
			"currency":    schemaString(),
			"travelers":   schemaInt(),
			"count":       schemaInt(),
			"opportunities": schemaArray(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"engine":           schemaString(),
					"type":             schemaString(),
					"description":      schemaString(),
					"estimated_saving": schemaNum(),
					"currency":         schemaString(),
					"confidence":       schemaString(),
				},
			}),
			"skipped": schemaArray(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"engine": schemaString(),
					"reason": schemaString(),
				},
			}),
		},
		"required": []string{"origin", "destination", "depart_date", "currency", "count", "opportunities", "skipped"},
	}
}

func handleArbitrageReport(ctx context.Context, args map[string]any, _ ElicitFunc, _ SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	origin := strings.ToUpper(argString(args, "origin"))
	destination := strings.ToUpper(argString(args, "destination"))
	departDate := argString(args, "depart_date")
	returnDate := argString(args, "return_date")
	currency := argString(args, "currency")
	travelers := argInt(args, "travelers", 1)

	fares, err := parseCabinFareStrings(argStringSlice(args, "cabin_fares"))
	if err != nil {
		return nil, nil, err
	}
	rebooks, err := parseRebookStrings(argStringSlice(args, "rebooks"))
	if err != nil {
		return nil, nil, err
	}

	sendProgress(progress, 0, 100, fmt.Sprintf("Aggregating arbitrage engines for %s→%s...", origin, destination))

	report := arbreport.Aggregate(ctx, arbreport.Params{
		Origin:       origin,
		Destination:  destination,
		DepartDate:   departDate,
		ReturnDate:   returnDate,
		Currency:     currency,
		Travelers:    travelers,
		CabinFares:   fares,
		HotelRebooks: rebooks,
	})

	sendProgress(progress, 100, 100, fmt.Sprintf("Found %d arbitrage opportunit%s", report.Count, arbReportPlural(report.Count)))

	if report.Opportunities == nil {
		report.Opportunities = []arbreport.Opportunity{}
	}
	if report.Skipped == nil {
		report.Skipped = []arbreport.SkippedEngine{}
	}

	summary := buildArbReportSummary(report)
	content, err := buildAnnotatedContentBlocks(summary, report)
	if err != nil {
		return nil, nil, err
	}
	return content, report, nil
}

func parseCabinFareStrings(items []string) ([]cabinarb.CabinFare, error) {
	out := make([]cabinarb.CabinFare, 0, len(items))
	for _, arg := range items {
		parts := strings.SplitN(arg, ":", 3)
		if len(parts) < 2 {
			return nil, fmt.Errorf("invalid cabin fare %q: expected cabin:price[:carrier]", arg)
		}
		cabin := strings.TrimSpace(parts[0])
		if cabin == "" {
			return nil, fmt.Errorf("invalid cabin fare %q: cabin must not be empty", arg)
		}
		price, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
		if err != nil {
			return nil, fmt.Errorf("invalid cabin fare %q: price must be a number: %w", arg, err)
		}
		carrier := ""
		if len(parts) == 3 {
			carrier = strings.TrimSpace(parts[2])
		}
		out = append(out, cabinarb.CabinFare{Cabin: cabinarb.Cabin(cabin), Price: price, Carrier: carrier})
	}
	return out, nil
}

func parseRebookStrings(items []string) ([]arbreport.HotelRebook, error) {
	out := make([]arbreport.HotelRebook, 0, len(items))
	for _, arg := range items {
		parts := strings.SplitN(arg, ":", 4)
		if len(parts) < 3 {
			return nil, fmt.Errorf("invalid rebook %q: expected name:original:current[:currency]", arg)
		}
		name := strings.TrimSpace(parts[0])
		if name == "" {
			return nil, fmt.Errorf("invalid rebook %q: hotel name must not be empty", arg)
		}
		original, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
		if err != nil {
			return nil, fmt.Errorf("invalid rebook %q: original price must be a number: %w", arg, err)
		}
		current, err := strconv.ParseFloat(strings.TrimSpace(parts[2]), 64)
		if err != nil {
			return nil, fmt.Errorf("invalid rebook %q: current price must be a number: %w", arg, err)
		}
		cur := "EUR"
		if len(parts) == 4 && strings.TrimSpace(parts[3]) != "" {
			cur = strings.ToUpper(strings.TrimSpace(parts[3]))
		}
		out = append(out, arbreport.HotelRebook{
			Hold: hotelarb.Hold{
				HotelName:     name,
				OriginalPrice: original,
				Currency:      cur,
				Refundable:    true,
			},
			Quote: hotelarb.PriceQuote{Price: current, Currency: cur},
		})
	}
	return out, nil
}

func buildArbReportSummary(report arbreport.ArbReport) string {
	var sb strings.Builder
	_, _ = fmt.Fprintf(&sb, "Unified arbitrage report for %s→%s on %s (%s):\n\n",
		report.Origin, report.Destination, report.DepartDate, report.Currency)
	if report.Count == 0 {
		sb.WriteString("No arbitrage opportunities found for this trip context.\n")
	} else {
		for i, o := range report.Opportunities {
			_, _ = fmt.Fprintf(&sb, "%d. [%s] %s — saves %.2f %s (%s confidence)\n",
				i+1, o.Engine, o.Description, o.EstimatedSaving, o.Currency, o.Confidence)
		}
	}
	if len(report.Skipped) > 0 {
		sb.WriteString("\nSkipped engines:\n")
		for _, s := range report.Skipped {
			_, _ = fmt.Fprintf(&sb, "  - %s: %s\n", s.Engine, s.Reason)
		}
	}
	return sb.String()
}

func arbReportPlural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
